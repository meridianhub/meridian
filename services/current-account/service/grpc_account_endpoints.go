package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	celutil "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	vf "github.com/meridianhub/meridian/shared/pkg/valuationfeature"
	"github.com/meridianhub/meridian/shared/platform/quantity/currency"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InitiateCurrentAccount creates a new current account facility.
// When product_type_code is provided, the account type definition is resolved from the
// Product Directory, BehaviorClass is validated as CUSTOMER, CEL eligibility is evaluated
// with party context, attributes are validated against the JSON Schema, and ValuationFeatures
// are seeded from the product type templates.
// When product_type_code is empty, backwards-compatible behavior is used (legacy path).
func (s *Service) InitiateCurrentAccount(ctx context.Context, req *pb.InitiateCurrentAccountRequest) (*pb.InitiateCurrentAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("initiate_account", operationStatus, time.Since(start))
	}()

	// Generate account ID
	accountID := fmt.Sprintf("ACC-%s", uuid.New().String()[:8])

	// Validate instrument_code is provided
	instrumentCode := req.InstrumentCode
	if instrumentCode == "" {
		operationStatus = operationStatusInvalidCurrency
		return nil, status.Errorf(codes.InvalidArgument, "instrument_code is required")
	}

	// Resolve dimension and precision from Reference Data service when available.
	// Dimension classifies the instrument type (e.g. "CURRENCY", "ENERGY", "COMPUTE").
	// When the getter is not configured, falls back to CURRENCY with precision derived
	// from the currency registry (so JPY/0-decimal currencies work correctly in the fallback path).
	dimension := "CURRENCY"
	precision := 2 // safe default, overridden below
	if s.instrumentGetter != nil {
		cachedInstrument, err := s.instrumentGetter.GetInstrument(ctx, instrumentCode, 0)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				// Instrument does not exist in Reference Data - caller supplied an invalid instrument_code
				operationStatus = "instrument_not_found"
				s.logger.Warn("instrument not found in Reference Data, cannot create account",
					"instrument_code", instrumentCode,
					"account_id", accountID)
				return nil, status.Errorf(codes.InvalidArgument, "unknown instrument_code: %s", instrumentCode)
			}
			// Transient failure (network error, service unavailable, timeout)
			operationStatus = "instrument_lookup_failed"
			s.logger.Error("instrument lookup failed due to transient error, cannot create account",
				"instrument_code", instrumentCode,
				"account_id", accountID,
				"error", err)
			caobservability.RecordExternalServiceError("reference_data", "get_instrument")
			return nil, status.Errorf(codes.Unavailable, "instrument lookup failed, please retry")
		}
		// Map from reference-data registry dimension ("MONETARY") to domain quantity
		// dimension ("CURRENCY"). The registry uses "MONETARY" while the domain quantity
		// package uses "CURRENCY" - other dimensions are identical across both packages.
		dimension = mapRegistryDimension(string(cachedInstrument.Definition.Dimension))
		precision = cachedInstrument.Definition.Precision
	} else {
		// Fallback path: no Reference Data service configured.
		// Derive precision from the currency registry for correctness (e.g. JPY needs 0, not 2).
		if inst, ok := currency.ByCode(strings.ToUpper(instrumentCode)); ok {
			precision = inst.Precision
		} else {
			s.logger.Warn("currency not found in local registry, using default precision",
				"instrument_code", instrumentCode,
				"default_precision", precision)
		}
	}

	// Validate party exists and is active (if party client is configured)
	if s.partyClient != nil {
		partyValidationStart := time.Now()
		s.logger.Info("validating party for account creation",
			"party_id", req.PartyId,
			"account_id", accountID)

		if err := s.partyClient.ValidateParty(ctx, req.PartyId); err != nil {
			caobservability.RecordPartyValidationDuration(time.Since(partyValidationStart), false)

			if errors.Is(err, ErrPartyNotFound) {
				operationStatus = "party_not_found"
				s.logger.Warn("party not found during account creation",
					"party_id", req.PartyId,
					"account_id", accountID)
				return nil, status.Errorf(codes.InvalidArgument, "party not found: %s", req.PartyId)
			}
			if errors.Is(err, ErrPartyNotActive) {
				operationStatus = "party_not_active"
				s.logger.Warn("party not active during account creation",
					"party_id", req.PartyId,
					"account_id", accountID)
				return nil, status.Errorf(codes.FailedPrecondition, "party not active: %s", req.PartyId)
			}
			operationStatus = "party_validation_failed"
			s.logger.Error("party validation failed during account creation",
				"party_id", req.PartyId,
				"account_id", accountID,
				"error", err)
			caobservability.RecordExternalServiceError("party", "validate_party")
			return nil, status.Errorf(codes.Internal, "party validation failed: %v", err)
		}

		caobservability.RecordPartyValidationDuration(time.Since(partyValidationStart), true)
		s.logger.Info("party validated successfully",
			"party_id", req.PartyId,
			"account_id", accountID)
	}

	// Resolve product type if provided
	var opts []domain.AccountOption
	var cachedType *CachedAccountType

	if req.ProductTypeCode != "" && s.accountTypeCache != nil {
		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			operationStatus = "missing_tenant"
			return nil, status.Error(codes.FailedPrecondition, "tenant context is required for product type resolution")
		}

		var err error
		cachedType, err = s.accountTypeCache.GetOrLoad(ctx, tenantID, req.ProductTypeCode)
		if err != nil {
			operationStatus = "product_type_not_found"
			s.logger.Warn("product type resolution failed",
				"product_type_code", req.ProductTypeCode,
				"account_id", accountID,
				"error", err)
			return nil, status.Errorf(codes.InvalidArgument, "product type not found: %s", req.ProductTypeCode)
		}

		// Gate: BehaviorClass must be CUSTOMER for current accounts
		if cachedType.Definition.BehaviorClass != accounttype.BehaviorClassCustomer {
			operationStatus = "invalid_behavior_class"
			s.logger.Warn("product type has non-CUSTOMER behavior class",
				"product_type_code", req.ProductTypeCode,
				"behavior_class", cachedType.Definition.BehaviorClass,
				"account_id", accountID)
			return nil, status.Errorf(codes.InvalidArgument,
				"product type %s has behavior class %s, expected CUSTOMER",
				req.ProductTypeCode, cachedType.Definition.BehaviorClass)
		}

		// Evaluate CEL eligibility if an eligibility program is configured
		if cachedType.EligibilityProgram != nil {
			if s.partyClient == nil {
				operationStatus = "eligibility_unavailable"
				s.logger.Error("party client not configured but eligibility program requires it",
					"product_type_code", req.ProductTypeCode,
					"party_id", req.PartyId,
					"account_id", accountID)
				return nil, status.Error(codes.FailedPrecondition, "party service is required for eligibility checks")
			}
			eligible, eligErr := s.evaluateEligibility(ctx, cachedType, req.PartyId, req.Attributes)
			if eligErr != nil {
				operationStatus = "eligibility_check_failed"
				s.logger.Error("eligibility evaluation failed",
					"product_type_code", req.ProductTypeCode,
					"party_id", req.PartyId,
					"account_id", accountID,
					"error", eligErr)
				return nil, status.Errorf(codes.Internal, "eligibility check failed: %v", eligErr)
			}
			if !eligible {
				operationStatus = "party_not_eligible"
				s.logger.Warn("party not eligible for product type",
					"product_type_code", req.ProductTypeCode,
					"party_id", req.PartyId,
					"account_id", accountID)
				return nil, status.Errorf(codes.FailedPrecondition,
					"party %s is not eligible for product type %s", req.PartyId, req.ProductTypeCode)
			}
		}

		// Validate attributes against JSON Schema if schema is defined.
		// Always validate when a schema exists - this catches missing required fields
		// even when no attributes are provided.
		if cachedType.CompiledSchema != nil {
			attrs := req.Attributes
			if attrs == nil {
				attrs = map[string]string{}
			}
			if err := validateAttributes(cachedType.CompiledSchema, attrs); err != nil {
				operationStatus = "invalid_attributes"
				return nil, status.Errorf(codes.InvalidArgument, "attribute validation failed: %v", err)
			}
		}

		// Determine version: use requested version or latest from definition.
		// When a specific version is requested, validate it does not exceed the
		// latest known version since the cache only holds the latest definition.
		version := cachedType.Definition.Version
		if req.ProductTypeVersion != nil {
			requested := int(*req.ProductTypeVersion)
			if requested > cachedType.Definition.Version {
				operationStatus = "invalid_version"
				return nil, status.Errorf(codes.InvalidArgument,
					"requested version %d exceeds latest version %d for product type %s",
					requested, cachedType.Definition.Version, req.ProductTypeCode)
			}
			version = requested
		}

		opts = append(opts, domain.WithProductType(req.ProductTypeCode, version))
		opts = append(opts, domain.WithBehaviorClass(string(cachedType.Definition.BehaviorClass)))

		s.logger.Info("product type resolved for account creation",
			"product_type_code", req.ProductTypeCode,
			"product_type_version", version,
			"behavior_class", cachedType.Definition.BehaviorClass,
			"account_id", accountID)
	}

	// Create domain model with resolved instrument, dimension, and precision
	account, err := domain.NewCurrentAccountWithDimension(
		accountID,
		req.ExternalIdentifier,
		req.PartyId,
		instrumentCode,
		dimension,
		precision,
		opts...,
	)
	if err != nil {
		operationStatus = "domain_error"
		return nil, status.Errorf(codes.InvalidArgument, "failed to create account: %v", err)
	}

	// Save to database (context carries audit user info for created_by/updated_by fields)
	if err := s.repo.Save(ctx, account); err != nil {
		operationStatus = opStatusSaveFailed
		return nil, status.Errorf(codes.Internal, "failed to create account: %v", err)
	}

	// Seed ValuationFeatures from product type templates (best-effort after account creation)
	if cachedType != nil && len(cachedType.Definition.ValuationMethods) > 0 && s.valuationFeatureRepo != nil {
		if err := s.seedValuationFeatures(ctx, account.ID(), cachedType.Definition); err != nil {
			// Log but do not fail account creation - VF seeding is best-effort
			s.logger.Error("failed to seed valuation features",
				"account_id", accountID,
				"product_type_code", req.ProductTypeCode,
				"error", err)
		}
	}

	// Record initial balance
	caobservability.RecordBalance(safeMinorUnits(account.Balance()), instrumentCode)

	// Convert to proto response
	return &pb.InitiateCurrentAccountResponse{
		AccountId: accountID,
		Facility:  toProtoFacility(account),
	}, nil
}

// evaluateEligibility retrieves party details and evaluates the CEL eligibility expression.
func (s *Service) evaluateEligibility(ctx context.Context, cachedType *CachedAccountType, partyID string, attributes map[string]string) (bool, error) {
	party, err := s.partyClient.GetParty(ctx, partyID)
	if err != nil {
		return false, fmt.Errorf("failed to retrieve party for eligibility: %w", err)
	}

	// Map proto enum values to the string keys expected by CEL expressions.
	// Proto enums use names like "PARTY_TYPE_PERSON" - strip prefix for CEL.
	partyType := stripEnumPrefix(party.GetPartyType().String(), "PARTY_TYPE_")
	partyStatus := stripEnumPrefix(party.GetStatus().String(), "PARTY_STATUS_")
	extRefType := stripEnumPrefix(party.GetExternalReferenceType().String(), "EXTERNAL_REFERENCE_TYPE_")

	return celutil.EvalEligibility(cachedType.EligibilityProgram, partyType, partyStatus, extRefType, attributes)
}

// stripEnumPrefix removes the proto enum prefix to produce CEL-friendly values.
// Example: "PARTY_TYPE_PERSON" -> "PERSON", "PARTY_STATUS_ACTIVE" -> "ACTIVE"
func stripEnumPrefix(value, prefix string) string {
	return strings.TrimPrefix(value, prefix)
}

// validateAttributes validates the request attributes against the compiled JSON Schema.
func validateAttributes(compiledSchema interface{ Validate(interface{}) error }, attributes map[string]string) error {
	// Convert map[string]string to map[string]interface{} for JSON Schema validation
	attrMap := make(map[string]interface{}, len(attributes))
	for k, v := range attributes {
		attrMap[k] = v
	}

	// JSON Schema validators expect deserialized JSON (map[string]interface{}).
	// Round-trip through JSON to ensure proper types.
	raw, err := json.Marshal(attrMap)
	if err != nil {
		return fmt.Errorf("failed to marshal attributes: %w", err)
	}

	var parsed interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("failed to unmarshal attributes: %w", err)
	}

	return compiledSchema.Validate(parsed)
}

// seedValuationFeatures seeds ValuationFeature records from product type templates.
// Delegates to the shared ProductTypeSeeder which filters ACTIVE templates and
// uses upsert semantics for saga retry safety.
func (s *Service) seedValuationFeatures(ctx context.Context, accountID uuid.UUID, productType *accounttype.Definition) error {
	seeder := vf.NewProductTypeSeeder(s.valuationFeatureRepo)
	return seeder.SeedFromProductType(ctx, accountID, productType, time.Now().UTC())
}

// ListCurrentAccounts returns a paginated list of current accounts with optional filtering.
func (s *Service) ListCurrentAccounts(ctx context.Context, req *pb.ListCurrentAccountsRequest) (*pb.ListCurrentAccountsResponse, error) {
	// Apply defaults and validate page size
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 25
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// Decode cursor
	cursor, err := persistence.DecodeAccountCursor(req.PageToken)
	if err != nil {
		s.logger.Warn("invalid page_token", "page_token", req.PageToken, "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
	}

	// Build filter params
	params := persistence.ListAccountsParams{
		Limit:  pageSize,
		Cursor: cursor,
		IBAN:   req.Iban,
	}

	if req.Status != pb.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED {
		statusStr, ok := protoToAccountStatus(req.Status)
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "invalid status: %v", req.Status)
		}
		params.Status = statusStr
	}

	// Execute query
	result, err := s.repo.ListAccounts(ctx, params)
	if err != nil {
		s.logger.Error("failed to list accounts", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list accounts: %v", err)
	}

	// Convert to proto
	accounts := make([]*pb.CurrentAccountFacility, 0, len(result.Accounts))
	for _, acc := range result.Accounts {
		accounts = append(accounts, toProtoFacility(acc))
	}

	return &pb.ListCurrentAccountsResponse{
		Accounts:      accounts,
		NextPageToken: result.NextCursor,
		TotalCount:    result.TotalCount,
	}, nil
}

// protoToAccountStatus converts a proto AccountStatus to a domain status string.
// Returns false for unrecognized enum values.
func protoToAccountStatus(s pb.AccountStatus) (string, bool) {
	switch s {
	case pb.AccountStatus_ACCOUNT_STATUS_ACTIVE:
		return string(domain.AccountStatusActive), true
	case pb.AccountStatus_ACCOUNT_STATUS_FROZEN:
		return string(domain.AccountStatusFrozen), true
	case pb.AccountStatus_ACCOUNT_STATUS_CLOSED:
		return string(domain.AccountStatusClosed), true
	case pb.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED:
		return "", true
	}
	return "", false
}

// RetrieveCurrentAccount gets current account details including balance from Position Keeping.
func (s *Service) RetrieveCurrentAccount(ctx context.Context, req *pb.RetrieveCurrentAccountRequest) (*pb.RetrieveCurrentAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("retrieve_account", operationStatus, time.Since(start))
	}()

	// Context carries organization for multi-tenant routing
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Hydrate account with balance from Position Keeping service.
	// Balance is no longer persisted locally - Position Keeping is the source of truth.
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to retrieve balance from Position Keeping",
			"account_id", req.AccountId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	return &pb.RetrieveCurrentAccountResponse{
		Facility: toProtoFacility(account),
	}, nil
}

// UpdateCurrentAccount modifies account configuration settings.
// BIAN: Update Control Record (UpCR) - Updates overdraft settings.
// Uses optimistic locking to prevent lost updates from concurrent modifications.
func (s *Service) UpdateCurrentAccount(ctx context.Context, req *pb.UpdateCurrentAccountRequest) (*pb.UpdateCurrentAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("update_account", operationStatus, time.Since(start))
	}()

	// Validate required fields
	if req.AccountId == "" {
		operationStatus = opStatusMissingAccountID
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}

	// Retrieve account (context carries organization for multi-tenant routing)
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Check if account is closed - cannot update closed accounts
	if account.Status() == domain.AccountStatusClosed {
		operationStatus = opStatusAccountClosed
		return nil, status.Errorf(codes.FailedPrecondition, "cannot update closed account: %s", req.AccountId)
	}

	// Track if any updates were made
	updated := false

	// Overdraft settings are no longer managed at the domain level.
	// Overdraft is now a product-type behavior defined in the Product Directory.
	// Requests to update overdraft fields are logged and ignored.
	if req.OverdraftLimit != nil || req.OverdraftEnabled != nil || req.OverdraftRate != nil {
		s.logger.Warn("overdraft update request ignored: overdraft is now product-type behavior",
			"account_id", req.AccountId)
	}

	// If no updates were made, return current state
	if !updated {
		s.logger.Debug("no changes to apply for UpdateCurrentAccount",
			"account_id", req.AccountId)
		return &pb.UpdateCurrentAccountResponse{
			Facility: toProtoFacility(account),
			Version:  account.Version(),
		}, nil
	}

	// Persist with optimistic locking
	if err := s.repo.Save(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			operationStatus = "version_conflict"
			s.logger.Warn("version conflict during account update",
				"account_id", req.AccountId)
			return nil, status.Errorf(codes.Aborted, "version conflict: account was modified by another transaction, please retry")
		}
		operationStatus = opStatusSaveFailed
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	s.logger.Info("account updated successfully",
		"account_id", req.AccountId,
		"new_version", account.Version())

	return &pb.UpdateCurrentAccountResponse{
		Facility: toProtoFacility(account),
		Version:  account.Version(),
	}, nil
}
