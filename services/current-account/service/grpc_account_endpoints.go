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
	vf "github.com/meridianhub/meridian/shared/pkg/valuationfeature"
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

	accountID := fmt.Sprintf("ACC-%s", uuid.New().String()[:8])

	instrumentCode := req.InstrumentCode
	if instrumentCode == "" {
		operationStatus = operationStatusInvalidCurrency
		return nil, status.Errorf(codes.InvalidArgument, "instrument_code is required")
	}

	// Resolve dimension and precision from Reference Data service
	resolved, opStatus, err := s.resolveInstrument(ctx, instrumentCode, accountID)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}

	// Validate party exists and is active (if party client is configured)
	if s.partyClient != nil {
		if opStatus, err := s.validatePartyForAccountCreation(ctx, req.PartyId, accountID); err != nil {
			operationStatus = opStatus
			return nil, err
		}
	}

	// Build account options
	var opts []domain.AccountOption

	// Set org_party_id if provided (for org-scoped accounts)
	if req.OrgPartyId != "" {
		orgPartyUUID, err := validateOrgPartyID(req.OrgPartyId)
		if err != nil {
			operationStatus = "invalid_org_party_id"
			return nil, err
		}
		opts = append(opts, domain.WithOrgPartyID(orgPartyUUID))
	}

	// Resolve product type if provided
	var cachedType *CachedAccountType
	pt, opStatus, err := s.resolveProductType(ctx, req.ProductTypeCode, req.ProductTypeVersion, req.PartyId, req.Attributes, accountID)
	if err != nil {
		operationStatus = opStatus
		return nil, err
	}
	if pt != nil {
		cachedType = pt.cachedType
		opts = append(opts, pt.opts...)
	}

	// Create domain model with resolved instrument, dimension, and precision
	account, err := domain.NewCurrentAccountWithDimension(
		accountID,
		req.ExternalIdentifier,
		req.PartyId,
		instrumentCode,
		resolved.dimension,
		resolved.precision,
		opts...,
	)
	if err != nil {
		operationStatus = "domain_error"
		return nil, status.Errorf(codes.InvalidArgument, "failed to create account: %v", err)
	}

	// Save to database (context carries audit user info for created_by/updated_by fields)
	if err := s.repo.Save(ctx, account); err != nil {
		operationStatus = opStatusSaveFailed
		if errors.Is(err, persistence.ErrAccountExists) {
			return nil, status.Errorf(codes.AlreadyExists, "account already exists: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to create account: %v", err)
	}

	// Seed ValuationFeatures from product type templates (best-effort after account creation)
	if cachedType != nil && len(cachedType.Definition.ValuationMethods) > 0 && s.valuationFeatureRepo != nil {
		if err := s.seedValuationFeatures(ctx, account.ID(), cachedType.Definition); err != nil {
			s.logger.Error("failed to seed valuation features",
				"account_id", accountID,
				"product_type_code", req.ProductTypeCode,
				"error", err)
		}
	}

	caobservability.RecordBalance(safeMinorUnits(account.Balance()), instrumentCode)

	return &pb.InitiateCurrentAccountResponse{
		AccountId: accountID,
		Facility:  toProtoFacility(account),
	}, nil
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
	// Build filter params from request
	params, err := buildListAccountsParams(req)
	if err != nil {
		return nil, err
	}

	// Decode cursor
	cursor, err := persistence.DecodeAccountCursor(req.PageToken)
	if err != nil {
		s.logger.Warn("invalid page_token", "page_token", req.PageToken, "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
	}
	params.Cursor = cursor

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

// buildListAccountsParams validates the request and builds ListAccountsParams.
func buildListAccountsParams(req *pb.ListCurrentAccountsRequest) (persistence.ListAccountsParams, error) {
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 25
	}
	if pageSize > 100 {
		pageSize = 100
	}

	params := persistence.ListAccountsParams{
		Limit: pageSize,
		IBAN:  req.Iban,
	}

	if req.Status != pb.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED {
		statusStr, ok := protoToAccountStatus(req.Status)
		if !ok {
			return params, status.Errorf(codes.InvalidArgument, "invalid status: %v", req.Status)
		}
		params.Status = statusStr
	}

	if req.PartyId != "" {
		partyUUID, err := uuid.Parse(req.PartyId)
		if err != nil {
			return params, status.Errorf(codes.InvalidArgument, "invalid party_id: must be a valid UUID")
		}
		if partyUUID == uuid.Nil {
			return params, status.Errorf(codes.InvalidArgument, "invalid party_id: zero UUID is not allowed")
		}
		params.PartyID = partyUUID
	}

	if req.OrgPartyId != "" {
		orgPartyUUID, err := uuid.Parse(req.OrgPartyId)
		if err != nil {
			return params, status.Errorf(codes.InvalidArgument, "invalid org_party_id: must be a valid UUID")
		}
		if orgPartyUUID == uuid.Nil {
			return params, status.Errorf(codes.InvalidArgument, "invalid org_party_id: zero UUID is not allowed")
		}
		params.OrgPartyID = orgPartyUUID
	}

	return params, nil
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
	// Best-effort: return account without balance if Position Keeping is unavailable.
	hydratedAccount, err := s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		s.logger.Warn("failed to retrieve balance from Position Keeping, returning account without balance",
			"account_id", req.AccountId,
			"error", err)
	} else {
		account = hydratedAccount
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
