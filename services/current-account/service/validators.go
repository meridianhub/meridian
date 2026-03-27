package service

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	quantitypb "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	celutil "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// resolvedInstrument holds the result of instrument code resolution from Reference Data.
type resolvedInstrument struct {
	dimension string
	precision int
}

// resolveInstrument validates the instrument code against Reference Data and returns
// the resolved dimension and precision.
func (s *Service) resolveInstrument(ctx context.Context, instrumentCode, accountID string) (*resolvedInstrument, string, error) {
	if s.instrumentGetter == nil {
		s.logger.Error("instrumentGetter not configured, cannot create account",
			"instrument_code", instrumentCode,
			"account_id", accountID)
		return nil, "reference_data_unavailable",
			status.Errorf(codes.FailedPrecondition, "Reference Data service is required for account creation")
	}

	cachedInstrument, err := s.instrumentGetter.GetInstrument(ctx, instrumentCode, 0)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
			return nil, "request_canceled", status.Error(codes.Canceled, "request canceled")
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
			return nil, "instrument_lookup_timeout", status.Error(codes.DeadlineExceeded, "instrument lookup timed out")
		case errors.Is(err, registry.ErrNotFound):
			s.logger.Warn("instrument not found in Reference Data, cannot create account",
				"instrument_code", instrumentCode,
				"account_id", accountID)
			return nil, "instrument_not_found",
				status.Errorf(codes.InvalidArgument, "unknown instrument_code: %s", instrumentCode)
		default:
			s.logger.Error("instrument lookup failed due to transient error, cannot create account",
				"instrument_code", instrumentCode,
				"account_id", accountID,
				"error", err)
			caobservability.RecordExternalServiceError("reference_data", "get_instrument")
			return nil, "instrument_lookup_failed",
				status.Errorf(codes.Unavailable, "instrument lookup failed, please retry")
		}
	}

	dimension := mapRegistryDimension(string(cachedInstrument.Definition.Dimension))
	return &resolvedInstrument{
		dimension: dimension,
		precision: cachedInstrument.Definition.Precision,
	}, "", nil
}

// resolvedProductType holds the result of product type resolution.
type resolvedProductType struct {
	cachedType *CachedAccountType
	opts       []domain.AccountOption
}

// resolveProductType validates and resolves the product type for account creation.
// Returns nil resolvedProductType (not an error) when no product type code is provided.
func (s *Service) resolveProductType(
	ctx context.Context,
	productTypeCode string,
	productTypeVersion *int32,
	partyID string,
	attributes map[string]string,
	accountID string,
) (*resolvedProductType, string, error) {
	if productTypeCode == "" || s.accountTypeCache == nil {
		return nil, "", nil
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, "missing_tenant",
			status.Error(codes.FailedPrecondition, "tenant context is required for product type resolution")
	}

	cachedType, err := s.accountTypeCache.GetOrLoad(ctx, tenantID, productTypeCode)
	if err != nil {
		s.logger.Warn("product type resolution failed",
			"product_type_code", productTypeCode,
			"account_id", accountID,
			"error", err)
		return nil, "product_type_not_found",
			status.Errorf(codes.InvalidArgument, "product type not found: %s", productTypeCode)
	}

	// Gate: BehaviorClass must be CUSTOMER for current accounts
	if cachedType.Definition.BehaviorClass != accounttype.BehaviorClassCustomer {
		s.logger.Warn("product type has non-CUSTOMER behavior class",
			"product_type_code", productTypeCode,
			"behavior_class", cachedType.Definition.BehaviorClass,
			"account_id", accountID)
		return nil, "invalid_behavior_class",
			status.Errorf(codes.InvalidArgument,
				"product type %s has behavior class %s, expected CUSTOMER",
				productTypeCode, cachedType.Definition.BehaviorClass)
	}

	// Evaluate CEL eligibility if an eligibility program is configured
	if cachedType.EligibilityProgram != nil {
		if opStatus, err := s.checkEligibility(ctx, cachedType, partyID, attributes, productTypeCode, accountID); err != nil {
			return nil, opStatus, err
		}
	}

	// Validate attributes against JSON Schema if schema is defined
	if cachedType.CompiledSchema != nil {
		attrs := attributes
		if attrs == nil {
			attrs = map[string]string{}
		}
		if err := validateAttributes(cachedType.CompiledSchema, attrs); err != nil {
			return nil, "invalid_attributes",
				status.Errorf(codes.InvalidArgument, "attribute validation failed: %v", err)
		}
	}

	// Determine version
	version := cachedType.Definition.Version
	if productTypeVersion != nil {
		requested := int(*productTypeVersion)
		if requested > cachedType.Definition.Version {
			return nil, "invalid_version",
				status.Errorf(codes.InvalidArgument,
					"requested version %d exceeds latest version %d for product type %s",
					requested, cachedType.Definition.Version, productTypeCode)
		}
		version = requested
	}

	opts := []domain.AccountOption{
		domain.WithProductType(productTypeCode, version),
		domain.WithBehaviorClass(string(cachedType.Definition.BehaviorClass)),
	}

	s.logger.Info("product type resolved for account creation",
		"product_type_code", productTypeCode,
		"product_type_version", version,
		"behavior_class", cachedType.Definition.BehaviorClass,
		"account_id", accountID)

	return &resolvedProductType{cachedType: cachedType, opts: opts}, "", nil
}

// checkEligibility evaluates CEL eligibility for a party against a product type.
func (s *Service) checkEligibility(
	ctx context.Context,
	cachedType *CachedAccountType,
	partyID string,
	attributes map[string]string,
	productTypeCode, accountID string,
) (string, error) {
	if s.partyClient == nil {
		s.logger.Error("party client not configured but eligibility program requires it",
			"product_type_code", productTypeCode,
			"party_id", partyID,
			"account_id", accountID)
		return "eligibility_unavailable",
			status.Error(codes.FailedPrecondition, "party service is required for eligibility checks")
	}

	party, err := s.partyClient.GetParty(ctx, partyID)
	if err != nil {
		s.logger.Error("eligibility evaluation failed",
			"product_type_code", productTypeCode,
			"party_id", partyID,
			"account_id", accountID,
			"error", err)
		return "eligibility_check_failed",
			status.Errorf(codes.Internal, "eligibility check failed: %v", err)
	}

	partyType := stripEnumPrefix(party.GetPartyType().String(), "PARTY_TYPE_")
	partyStatus := stripEnumPrefix(party.GetStatus().String(), "PARTY_STATUS_")
	extRefType := stripEnumPrefix(party.GetExternalReferenceType().String(), "EXTERNAL_REFERENCE_TYPE_")

	eligible, evalErr := celutil.EvalEligibility(cachedType.EligibilityProgram, partyType, partyStatus, extRefType, attributes)
	if evalErr != nil {
		s.logger.Error("eligibility evaluation failed",
			"product_type_code", productTypeCode,
			"party_id", partyID,
			"account_id", accountID,
			"error", evalErr)
		return "eligibility_check_failed",
			status.Errorf(codes.Internal, "eligibility check failed: %v", evalErr)
	}
	if !eligible {
		s.logger.Warn("party not eligible for product type",
			"product_type_code", productTypeCode,
			"party_id", partyID,
			"account_id", accountID)
		return "party_not_eligible",
			status.Errorf(codes.FailedPrecondition,
				"party %s is not eligible for product type %s", partyID, productTypeCode)
	}

	return "", nil
}

// validatePartyForAccountCreation validates that the party exists and is active.
func (s *Service) validatePartyForAccountCreation(ctx context.Context, partyID, accountID string) (string, error) {
	partyValidationStart := time.Now()
	s.logger.Info("validating party for account creation",
		"party_id", partyID,
		"account_id", accountID)

	if err := s.partyClient.ValidateParty(ctx, partyID); err != nil {
		caobservability.RecordPartyValidationDuration(time.Since(partyValidationStart), false)

		if errors.Is(err, ErrPartyNotFound) {
			s.logger.Warn("party not found during account creation",
				"party_id", partyID,
				"account_id", accountID)
			return "party_not_found",
				status.Errorf(codes.InvalidArgument, "party not found: %s", partyID)
		}
		if errors.Is(err, ErrPartyNotActive) {
			s.logger.Warn("party not active during account creation",
				"party_id", partyID,
				"account_id", accountID)
			return "party_not_active",
				status.Errorf(codes.FailedPrecondition, "party not active: %s", partyID)
		}
		s.logger.Error("party validation failed during account creation",
			"party_id", partyID,
			"account_id", accountID,
			"error", err)
		caobservability.RecordExternalServiceError("party", "validate_party")
		return "party_validation_failed",
			status.Errorf(codes.Internal, "party validation failed: %v", err)
	}

	caobservability.RecordPartyValidationDuration(time.Since(partyValidationStart), true)
	s.logger.Info("party validated successfully",
		"party_id", partyID,
		"account_id", accountID)
	return "", nil
}

// resolveWithdrawalSource determines the account ID, amount, and pending withdrawal
// from either a withdrawal_id (pending withdrawal) or direct account_id + amount.
func (s *Service) resolveWithdrawalSource(ctx context.Context, req *pb.ExecuteWithdrawalRequest) (string, *commonpb.MoneyAmount, *domain.Withdrawal, string, error) {
	if req.WithdrawalId != "" {
		withdrawal, err := s.withdrawalRepo.FindByReference(ctx, req.WithdrawalId)
		if err != nil {
			if errors.Is(err, persistence.ErrWithdrawalNotFound) {
				return "", nil, nil, opStatusWithdrawalNotFound,
					status.Errorf(codes.NotFound, "withdrawal not found: %s", req.WithdrawalId)
			}
			s.logger.Error("failed to retrieve withdrawal",
				"withdrawal_id", req.WithdrawalId,
				"error", err)
			return "", nil, nil, opStatusRetrieveFailed,
				status.Errorf(codes.Internal, "failed to retrieve withdrawal: %v", err)
		}

		if !withdrawal.IsPending() {
			return "", nil, nil, "withdrawal_not_pending",
				status.Errorf(codes.FailedPrecondition,
					"withdrawal %s is not pending (status: %s)", req.WithdrawalId, withdrawal.Status)
		}

		account, err := s.repo.FindByUUID(ctx, withdrawal.AccountID)
		if err != nil {
			if errors.Is(err, persistence.ErrAccountNotFound) {
				return "", nil, nil, opStatusAccountNotFound,
					status.Errorf(codes.NotFound, "account not found for withdrawal: %s", req.WithdrawalId)
			}
			return "", nil, nil, opStatusRetrieveFailed,
				status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
		}

		s.logger.Info("executing pending withdrawal",
			"withdrawal_id", req.WithdrawalId,
			"account_id", account.AccountID())

		return account.AccountID(), toMoneyAmount(withdrawal.Amount), withdrawal, "", nil
	}

	// Direct withdrawal mode
	if req.AccountId == "" {
		return "", nil, nil, opStatusMissingAccountID,
			status.Error(codes.InvalidArgument, "account_id is required for direct withdrawal")
	}
	if req.Amount == nil || req.Amount.Amount == nil {
		return "", nil, nil, opStatusMissingAmount,
			status.Error(codes.InvalidArgument, "amount is required for direct withdrawal")
	}
	return req.AccountId, req.Amount, nil, "", nil
}

// validateOrgPartyID validates and parses an org_party_id string.
// Returns uuid.Nil if the input is empty (personal account).
func validateOrgPartyID(orgPartyID string) (uuid.UUID, error) {
	if orgPartyID == "" {
		return uuid.Nil, nil
	}
	orgPartyUUID, err := uuid.Parse(orgPartyID)
	if err != nil {
		return uuid.Nil, status.Errorf(codes.InvalidArgument, "invalid org_party_id: must be a valid UUID")
	}
	if orgPartyUUID == uuid.Nil {
		return uuid.Nil, status.Errorf(codes.InvalidArgument, "invalid org_party_id: zero UUID is not allowed")
	}
	return orgPartyUUID, nil
}

// parseDepositInput validates and converts a multi-asset InstrumentAmount input for deposits.
// Returns the domain Amount and any error with the corresponding operationStatus.
func parseDepositInput(input *quantitypb.InstrumentAmount, account domain.CurrentAccount) (domain.Amount, string, error) {
	if input.Amount == "" || input.InstrumentCode == "" {
		return domain.Amount{}, opStatusInvalidAmount,
			status.Error(codes.InvalidArgument, "input.amount and input.instrument_code are required when input is provided")
	}

	inputAmount, parseErr := decimal.NewFromString(input.Amount)
	if parseErr != nil {
		return domain.Amount{}, opStatusInvalidAmount,
			status.Errorf(codes.InvalidArgument, "invalid input amount: %v", parseErr)
	}
	if !inputAmount.IsPositive() {
		return domain.Amount{}, opStatusInvalidAmount,
			status.Error(codes.InvalidArgument, "deposit amount must be positive")
	}

	// Validate instrument code matches the account's instrument
	if input.InstrumentCode != account.Balance().InstrumentCode() {
		return domain.Amount{}, opStatusCurrencyMismatch,
			status.Errorf(codes.InvalidArgument,
				"instrument mismatch: expected %s, got %s",
				account.Balance().InstrumentCode(), input.InstrumentCode)
	}

	// Convert to minor units using the account's precision
	precision := account.Balance().Instrument().Precision
	// #nosec G115 - precision is bounded by instrument definition (0-9 in practice)
	minorUnits := inputAmount.Shift(int32(precision))
	if !minorUnits.Equal(minorUnits.Truncate(0)) {
		return domain.Amount{}, opStatusInvalidAmount,
			status.Errorf(codes.InvalidArgument,
				"deposit amount %q exceeds instrument precision %d",
				input.Amount, precision)
	}
	maxInt64 := decimal.NewFromInt(math.MaxInt64)
	if minorUnits.GreaterThan(maxInt64) {
		return domain.Amount{}, opStatusAmountOverflow,
			status.Error(codes.InvalidArgument, "deposit amount overflow")
	}
	if !minorUnits.IsPositive() {
		return domain.Amount{}, opStatusInvalidAmount,
			status.Error(codes.InvalidArgument, "deposit amount must be positive")
	}

	amount, amountErr := domain.NewAmountFromInstrument(
		account.Balance().InstrumentCode(),
		account.Dimension(),
		precision,
		minorUnits.IntPart(),
	)
	if amountErr != nil {
		return domain.Amount{}, opStatusInvalidAmount,
			status.Errorf(codes.InvalidArgument, "invalid amount: %v", amountErr)
	}

	return amount, "", nil
}

// parseDepositLegacyAmount validates and converts a legacy MoneyAmount for deposits.
func parseDepositLegacyAmount(reqAmount *commonpb.MoneyAmount, account domain.CurrentAccount) (domain.Amount, string, error) {
	if reqAmount == nil || reqAmount.Amount == nil {
		return domain.Amount{}, opStatusMissingAmount,
			status.Error(codes.InvalidArgument, "amount is required (provide amount or input)")
	}

	// Validate currency matches account currency
	if reqAmount.Amount.CurrencyCode != account.Balance().InstrumentCode() {
		return domain.Amount{}, opStatusCurrencyMismatch,
			status.Errorf(codes.InvalidArgument,
				"currency mismatch: expected %s, got %s",
				account.Balance().InstrumentCode(), reqAmount.Amount.CurrencyCode)
	}

	amount, amountErr := protoMoneyToAmount(reqAmount, account)
	if amountErr != nil {
		return domain.Amount{}, opStatusInvalidAmount,
			status.Errorf(codes.InvalidArgument, "invalid amount: %v", amountErr)
	}

	amountCents, overflowErr := amount.ToMinorUnits()
	if overflowErr != nil {
		return domain.Amount{}, opStatusAmountOverflow,
			status.Errorf(codes.InvalidArgument, "deposit amount overflow: %v", overflowErr)
	}
	if amountCents <= 0 {
		return domain.Amount{}, opStatusInvalidAmount,
			status.Errorf(codes.InvalidArgument,
				"deposit amount must be positive, got %d minor units", amountCents)
	}

	return amount, "", nil
}

// validateWithdrawalAmount validates and converts a MoneyAmount for withdrawal operations.
func validateWithdrawalAmount(reqAmount *commonpb.MoneyAmount, account domain.CurrentAccount) (domain.Amount, string, error) {
	// Validate currency matches account currency
	if reqAmount.Amount.CurrencyCode != account.Balance().InstrumentCode() {
		return domain.Amount{}, opStatusCurrencyMismatch,
			status.Errorf(codes.InvalidArgument,
				"currency mismatch: expected %s, got %s",
				account.Balance().InstrumentCode(), reqAmount.Amount.CurrencyCode)
	}

	amount, err := protoMoneyToAmount(reqAmount, account)
	if err != nil {
		return domain.Amount{}, operationStatusInvalidCurrency,
			status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}

	amountCents, err := amount.ToMinorUnits()
	if err != nil {
		return domain.Amount{}, opStatusAmountOverflow,
			status.Errorf(codes.InvalidArgument, "withdrawal amount overflow: %v", err)
	}
	if amountCents <= 0 {
		return domain.Amount{}, opStatusInvalidAmount,
			status.Errorf(codes.InvalidArgument,
				"withdrawal amount must be positive, got %d minor units", amountCents)
	}

	return amount, "", nil
}
