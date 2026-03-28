package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	ibaobservability "github.com/meridianhub/meridian/services/internal-account/observability"
	vf "github.com/meridianhub/meridian/shared/pkg/valuationfeature"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
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

	// 1. Resolve product type, validate eligibility, and validate attributes.
	productTypeCode := req.ProductTypeCode
	accountType, productTypeVersion, productTypeDef, err := s.resolveProductType(ctx, req)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, err
	}

	// Convert clearing purpose from proto to domain
	var clearingPurpose domain.ClearingPurpose
	clearingPurpose, err = protoToClearingPurpose(req.ClearingPurpose)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "invalid clearing purpose: %v", err)
	}

	// 2. Validate instrument exists and is ACTIVE via Reference Data service.
	dimension, opStatus, err := s.validateInstrument(ctx, req.InstrumentCode)
	if err != nil {
		operationStatus = opStatus
		return nil, err
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
		account = rebuildWithProductType(account, productTypeCode, productTypeVersion)
	}

	// Handle counterparty details for NOSTRO/VOSTRO accounts
	account, err = s.applyCounterpartyDetails(account, req.CounterpartyDetails, accountType)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, err
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

// rebuildWithProductType rebuilds an account with product type fields set.
func rebuildWithProductType(account domain.InternalAccount, productTypeCode string, productTypeVersion int) domain.InternalAccount {
	return domain.NewInternalAccountBuilder().
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

// applyCounterpartyDetails validates and applies counterparty details to the account.
func (s *Service) applyCounterpartyDetails(account domain.InternalAccount, details *pb.CounterpartyDetails, accountType domain.AccountType) (domain.InternalAccount, error) {
	if details != nil {
		counterparty, err := domain.NewCounterpartyDetailsWithOptions(
			details.CounterpartyId,
			details.CounterpartyName,
			details.CounterpartyExternalRef,
			details.Attributes,
		)
		if err != nil {
			return account, status.Errorf(codes.InvalidArgument, "invalid counterparty details: %v", err)
		}
		account, err = account.UpdateCounterparty(counterparty)
		if err != nil {
			return account, mapDomainErrorToGRPC(err)
		}
		return account, nil
	}

	if accountType.RequiresCorrespondent() {
		return account, status.Errorf(codes.InvalidArgument, "counterparty details required for %s accounts", accountType)
	}
	return account, nil
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
