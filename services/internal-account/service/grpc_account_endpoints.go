package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	ibaobservability "github.com/meridianhub/meridian/services/internal-account/observability"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	vf "github.com/meridianhub/meridian/shared/pkg/valuationfeature"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// InitiateInternalAccount creates a new internal account.
func (s *Service) InitiateInternalAccount(ctx context.Context, req *pb.InitiateInternalAccountRequest) (*pb.InitiateInternalAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("initiate_internal_account", operationStatus, time.Since(start))
	}()

	// 1. Determine product_type_code (required for new accounts).
	productTypeCode := req.ProductTypeCode

	var accountType domain.AccountType
	var clearingPurpose domain.ClearingPurpose
	var dimension string
	var productTypeVersion int
	var productTypeDef *accounttype.Definition

	// 2. Product type resolution via cache (if cache is configured and code is available)
	if s.accountTypeCache != nil && productTypeCode != "" {
		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			operationStatus = operationStatusFailed
			return nil, status.Error(codes.InvalidArgument, "tenant context required")
		}

		cached, err := s.accountTypeCache.GetOrLoad(ctx, tenantID, productTypeCode)
		if err != nil {
			operationStatus = operationStatusFailed
			s.logger.Warn("product type resolution failed",
				"product_type_code", productTypeCode,
				"error", err)
			return nil, status.Errorf(codes.InvalidArgument, "product type not found: %s", productTypeCode)
		}

		if cached.Definition == nil {
			operationStatus = operationStatusFailed
			return nil, status.Errorf(codes.Internal, "product type %s has no definition", productTypeCode)
		}

		def := cached.Definition

		// 3. BehaviorClass gating: must NOT be CUSTOMER
		if !internalBehaviorClasses[def.BehaviorClass] {
			operationStatus = operationStatusFailed
			return nil, status.Errorf(codes.InvalidArgument,
				"product type %s has behavior class %s which is not an internal account type",
				productTypeCode, def.BehaviorClass)
		}

		// 4. EligibilityCEL evaluation (skip if empty or "true")
		if cached.EligibilityProgram != nil && def.EligibilityCEL != "" && def.EligibilityCEL != "true" {
			// For internal accounts, eligibility checks evaluate without a party.
			// The CEL environment receives account-level context only.
			activation := map[string]interface{}{
				"instrument_code": req.InstrumentCode,
				"account_code":    req.AccountCode,
			}
			out, _, evalErr := cached.EligibilityProgram.Eval(activation)
			if evalErr != nil {
				operationStatus = operationStatusFailed
				return nil, status.Errorf(codes.Internal, "eligibility evaluation failed: %v", evalErr)
			}
			eligible, isBool := out.Value().(bool)
			if !isBool || !eligible {
				operationStatus = operationStatusFailed
				return nil, status.Errorf(codes.FailedPrecondition,
					"account not eligible per product type %s eligibility rules", productTypeCode)
			}
		}

		// 5. Attribute validation against AttributeSchema (if defined)
		if cached.CompiledSchema != nil {
			attrsMap := map[string]interface{}{}
			if req.Attributes != nil {
				attrsMap = req.Attributes.AsMap()
			}
			attrsJSON, err := json.Marshal(attrsMap)
			if err != nil {
				operationStatus = operationStatusFailed
				return nil, status.Errorf(codes.InvalidArgument, "invalid attributes: %v", err)
			}
			var attrs interface{}
			if err := json.Unmarshal(attrsJSON, &attrs); err != nil {
				operationStatus = operationStatusFailed
				return nil, status.Errorf(codes.InvalidArgument, "invalid attributes JSON: %v", err)
			}
			if err := cached.CompiledSchema.Validate(attrs); err != nil {
				operationStatus = operationStatusFailed
				return nil, status.Errorf(codes.InvalidArgument, "attributes validation failed: %v", err)
			}
		}

		// Derive account type from BehaviorClass via mapping
		accountType = behaviorClassToAccountType[def.BehaviorClass]
		productTypeVersion = def.Version
		productTypeDef = def

		// Derive dimension from instrument via Reference Data (if available)
	} else if s.accountTypeCache == nil && req.ProductTypeCode != "" {
		// product_type_code was explicitly provided but cache is not configured
		operationStatus = operationStatusFailed
		return nil, status.Error(codes.FailedPrecondition,
			"product type resolution not available; configure account type cache")
	} else if productTypeCode == "" {
		// product_type_code is required - the deprecated account_type enum has been removed
		operationStatus = opStatusInvalidAccountType
		return nil, status.Error(codes.InvalidArgument,
			"product_type_code is required; the deprecated account_type enum has been removed")
	}

	// Convert clearing purpose from proto to domain
	var err error
	clearingPurpose, err = protoToClearingPurpose(req.ClearingPurpose)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "invalid clearing purpose: %v", err)
	}

	// Validate instrument exists and is ACTIVE via Reference Data service (if client is configured)
	if s.referenceDataClient != nil {
		validationStart := time.Now()
		refDataCtx, refDataCancel := context.WithTimeout(ctx, 5*time.Second)
		defer refDataCancel()

		refDataResp, err := s.referenceDataClient.RetrieveInstrument(refDataCtx, &referencedatav1.RetrieveInstrumentRequest{
			Code: req.InstrumentCode,
		})
		if err != nil {
			validationDuration := time.Since(validationStart)
			errCode := status.Code(err)
			s.logger.Warn("instrument validation failed",
				"instrument_code", req.InstrumentCode,
				"error", err)

			if errCode == codes.NotFound {
				operationStatus = opStatusInstrumentNotFound
				ibaobservability.RecordInstrumentValidation("not_found", validationDuration)
				return nil, status.Errorf(codes.InvalidArgument, "instrument not found: %s", req.InstrumentCode)
			}
			if errCode == codes.DeadlineExceeded || errCode == codes.Canceled {
				operationStatus = opStatusInstrumentValidationErr
				ibaobservability.RecordInstrumentValidation("timeout", validationDuration)
				return nil, status.Errorf(codes.DeadlineExceeded, "instrument validation timed out for: %s", req.InstrumentCode)
			}
			operationStatus = opStatusInstrumentValidationErr
			ibaobservability.RecordInstrumentValidation("error", validationDuration)
			return nil, status.Errorf(codes.Internal, "failed to validate instrument: %v", err)
		}

		// Guard against nil instrument in response (defensive programming)
		if refDataResp.Instrument == nil {
			validationDuration := time.Since(validationStart)
			operationStatus = opStatusInstrumentValidationErr
			s.logger.Error("reference data service returned nil instrument",
				"instrument_code", req.InstrumentCode)
			ibaobservability.RecordInstrumentValidation("error", validationDuration)
			return nil, status.Errorf(codes.Internal, "reference data service returned invalid response for instrument: %s", req.InstrumentCode)
		}

		// Validate instrument status is ACTIVE
		if refDataResp.Instrument.Status != referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE {
			validationDuration := time.Since(validationStart)
			operationStatus = opStatusInstrumentNotActive
			s.logger.Warn("instrument not active",
				"instrument_code", req.InstrumentCode,
				"status", refDataResp.Instrument.Status.String())
			ibaobservability.RecordInstrumentValidation("not_active", validationDuration)
			return nil, status.Errorf(codes.InvalidArgument, "instrument %s is not active (status: %s)",
				req.InstrumentCode, refDataResp.Instrument.Status.String())
		}

		// Extract dimension from validated instrument (strip DIMENSION_ prefix for domain consistency)
		dimension = strings.TrimPrefix(refDataResp.Instrument.Dimension.String(), "DIMENSION_")
		ibaobservability.RecordInstrumentValidation("success", time.Since(validationStart))
	}

	// Generate account ID using full UUID for uniqueness
	accountID := fmt.Sprintf("IBA-%s", uuid.New().String())

	// Create domain entity with dimension from Reference Data
	account, err := domain.NewInternalAccount(
		accountID,
		req.AccountCode,
		req.Name,
		accountType,
		clearingPurpose,
		req.InstrumentCode,
		dimension,
	)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, mapDomainErrorToGRPC(err)
	}

	// Set product type fields on the account via builder rebuild
	if productTypeCode != "" {
		account = domain.NewInternalAccountBuilder().
			WithID(account.ID()).
			WithAccountID(account.AccountID()).
			WithAccountCode(account.AccountCode()).
			WithName(account.Name()).
			WithAccountType(account.AccountType()).
			WithClearingPurpose(account.ClearingPurpose()).
			WithInstrumentCode(account.InstrumentCode()).
			WithDimension(account.Dimension()).
			WithStatus(account.Status()).
			WithOrgPartyID(account.OrgPartyID()).
			WithCounterparty(account.Counterparty()).
			WithAttributes(account.Attributes()).
			WithProductTypeCode(productTypeCode).
			WithProductTypeVersion(productTypeVersion).
			WithVersion(account.Version()).
			WithCreatedAt(account.CreatedAt()).
			WithUpdatedAt(account.UpdatedAt()).
			Build()
	}

	// Handle counterparty details for NOSTRO/VOSTRO accounts
	if req.CounterpartyDetails != nil {
		counterparty, err := domain.NewCounterpartyDetailsWithOptions(
			req.CounterpartyDetails.CounterpartyId,
			req.CounterpartyDetails.CounterpartyName,
			req.CounterpartyDetails.CounterpartyExternalRef,
			req.CounterpartyDetails.Attributes,
		)
		if err != nil {
			operationStatus = operationStatusFailed
			return nil, status.Errorf(codes.InvalidArgument, "invalid counterparty details: %v", err)
		}
		account, err = account.UpdateCounterparty(counterparty)
		if err != nil {
			operationStatus = operationStatusFailed
			return nil, mapDomainErrorToGRPC(err)
		}
	} else if accountType.RequiresCorrespondent() {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "counterparty details required for %s accounts", accountType)
	}

	// Persist via repository (atomically with outbox event when publisher is configured)
	if err := s.saveAccountWithEvent(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrDuplicateCode) {
			operationStatus = opStatusDuplicateCode
			return nil, status.Errorf(codes.AlreadyExists, "account code already exists: %s", req.AccountCode)
		}
		operationStatus = operationStatusFailed
		s.logger.Error("failed to save account", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to create account: %v", err)
	}

	// Seed ValuationFeatures from product type templates using upsert semantics.
	if s.valuationFeatureRepo != nil && productTypeDef != nil && len(productTypeDef.ValuationMethods) > 0 {
		seeder := vf.NewProductTypeSeeder(s.valuationFeatureRepo)
		if err := seeder.SeedFromProductType(ctx, account.ID(), productTypeDef, time.Now().UTC()); err != nil {
			s.logger.Warn("failed to seed valuation features from product type",
				"account_id", accountID,
				"product_type_code", productTypeCode,
				"error", err)
		}
	}

	// Record metric for account creation
	ibaobservability.RecordAccountCreated(accountType.String())

	s.logger.Info("created internal account",
		"account_id", accountID,
		"account_code", req.AccountCode,
		"account_type", accountType.String(),
		"product_type_code", productTypeCode)

	return &pb.InitiateInternalAccountResponse{
		AccountId: accountID,
		Facility:  toProtoFacility(account),
	}, nil
}

// UpdateInternalAccount modifies account settings.
func (s *Service) UpdateInternalAccount(ctx context.Context, req *pb.UpdateInternalAccountRequest) (*pb.UpdateInternalAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("update_internal_account", operationStatus, time.Since(start))
	}()

	// Load existing account
	accountUUID, err := uuid.Parse(req.AccountId)
	if err != nil {
		// Try finding by account_id string if not a UUID
		account, err := s.findAccountByID(ctx, req.AccountId)
		if err != nil {
			operationStatus = opStatusAccountNotFound
			return nil, err
		}
		return s.updateAccount(ctx, account, req, &operationStatus)
	}

	account, err := s.repo.FindByID(ctx, accountUUID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	return s.updateAccount(ctx, account, req, &operationStatus)
}

func (s *Service) updateAccount(ctx context.Context, account domain.InternalAccount, req *pb.UpdateInternalAccountRequest, operationStatus *string) (*pb.UpdateInternalAccountResponse, error) {
	// Check version for optimistic locking if provided
	if req.ExpectedVersion > 0 && int64(req.ExpectedVersion) != account.Version() {
		*operationStatus = opStatusVersionConflict
		return nil, status.Errorf(codes.Aborted, "version mismatch: expected %d, got %d", req.ExpectedVersion, account.Version())
	}

	// Cannot update closed accounts
	if account.Status() == domain.AccountStatusClosed {
		*operationStatus = operationStatusFailed
		return nil, status.Error(codes.FailedPrecondition, "cannot update closed account")
	}

	var err error

	// Update name if provided
	if req.Name != "" {
		account, err = account.Rename(req.Name)
		if err != nil {
			*operationStatus = operationStatusFailed
			return nil, mapDomainErrorToGRPC(err)
		}
	}

	// Update counterparty details if provided
	if req.CounterpartyDetails != nil {
		counterparty, err := domain.NewCounterpartyDetailsWithOptions(
			req.CounterpartyDetails.CounterpartyId,
			req.CounterpartyDetails.CounterpartyName,
			req.CounterpartyDetails.CounterpartyExternalRef,
			req.CounterpartyDetails.Attributes,
		)
		if err != nil {
			*operationStatus = operationStatusFailed
			return nil, status.Errorf(codes.InvalidArgument, "invalid counterparty details: %v", err)
		}
		account, err = account.UpdateCounterparty(counterparty)
		if err != nil {
			*operationStatus = operationStatusFailed
			return nil, mapDomainErrorToGRPC(err)
		}
	}

	// Persist changes
	if err := s.repo.Save(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			*operationStatus = opStatusVersionConflict
			return nil, status.Error(codes.Aborted, "account was modified by another transaction")
		}
		*operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	return &pb.UpdateInternalAccountResponse{
		Facility: toProtoFacility(account),
	}, nil
}

// ControlInternalAccount performs lifecycle state transitions.
func (s *Service) ControlInternalAccount(ctx context.Context, req *pb.ControlInternalAccountRequest) (*pb.ControlInternalAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("control_internal_account", operationStatus, time.Since(start))
	}()

	// Load existing account
	account, err := s.findAccountByID(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusAccountNotFound
		return nil, err
	}

	// Capture previous status for metrics
	previousStatus := account.Status()

	// Execute state transition based on control action
	switch req.ControlAction {
	case pb.ControlAction_CONTROL_ACTION_SUSPEND:
		account, err = account.Suspend(req.Reason)
	case pb.ControlAction_CONTROL_ACTION_ACTIVATE:
		account, err = account.Activate()
	case pb.ControlAction_CONTROL_ACTION_CLOSE:
		account, err = account.Close(req.Reason)
	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "control action must be specified")
	default:
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "invalid control action: %v", req.ControlAction)
	}

	if err != nil {
		if errors.Is(err, domain.ErrInvalidStatusTransition) {
			operationStatus = opStatusInvalidStatusTransition
			return nil, status.Errorf(codes.FailedPrecondition, "invalid status transition: %v", err)
		}
		operationStatus = operationStatusFailed
		return nil, mapDomainErrorToGRPC(err)
	}

	// Persist changes
	if err := s.repo.Save(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			operationStatus = opStatusVersionConflict
			return nil, status.Error(codes.Aborted, "account was modified by another transaction")
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	// Record status change metric
	ibaobservability.RecordAccountStatusChange(string(previousStatus), string(account.Status()))

	s.logger.Info("executed control action on internal account",
		"account_id", req.AccountId,
		"action", req.ControlAction.String(),
		"new_status", string(account.Status()))

	return &pb.ControlInternalAccountResponse{
		Facility:        toProtoFacility(account),
		ActionTimestamp: timestamppb.Now(),
	}, nil
}

// saveAccountWithEvent saves the account and optionally publishes a FacilityCreatedEvent to the outbox
// in the same database transaction for atomicity.
func (s *Service) saveAccountWithEvent(ctx context.Context, account domain.InternalAccount) error {
	if s.outboxPublisher == nil || s.db == nil {
		return s.repo.Save(ctx, account)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.repo.SaveInTx(ctx, account, tx); err != nil {
			return err
		}
		proto := &eventsv1.FacilityCreatedEvent{
			EventId:        uuid.New().String(),
			AccountId:      account.AccountID(),
			AccountCode:    account.AccountCode(),
			AccountType:    account.AccountType().String(),
			InstrumentCode: account.InstrumentCode(),
			Timestamp:      timestamppb.New(time.Now().UTC()),
		}
		return s.outboxPublisher.Publish(ctx, tx, proto, events.PublishConfig{
			EventType:     "internal-account.facility-created.v1",
			AggregateID:   account.AccountID(),
			AggregateType: "InternalAccount",
			Topic:         topics.InternalAccountFacilityCreatedV1,
			CorrelationID: uuid.New().String(),
		})
	})
}
