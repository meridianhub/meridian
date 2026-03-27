package service

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
)

// initiateBookingLogParams holds validated parameters for InitiateFinancialBookingLog.
type initiateBookingLogParams struct {
	AccountType             string
	ProductServiceReference string
	BusinessUnitReference   string
	ChartOfAccountsRules    string
	BaseCurrency            domain.Currency
}

// validateInitiateBookingLogRequest validates and extracts parameters from an InitiateFinancialBookingLogRequest.
func (s *FinancialAccountingService) validateInitiateBookingLogRequest(
	ctx context.Context,
	req *financialaccountingv1.InitiateFinancialBookingLogRequest,
) (*initiateBookingLogParams, error) {
	if req.FinancialAccountType == "" {
		return nil, status.Error(codes.InvalidArgument, "financial_account_type must be specified")
	}
	if req.ProductServiceReference == "" {
		return nil, status.Error(codes.InvalidArgument, "product_service_reference is required")
	}
	if req.BusinessUnitReference == "" {
		return nil, status.Error(codes.InvalidArgument, "business_unit_reference is required")
	}
	if req.ChartOfAccountsRules == "" {
		return nil, status.Error(codes.InvalidArgument, "chart_of_accounts_rules is required")
	}
	if req.BaseInstrumentCode == "" {
		return nil, status.Error(codes.InvalidArgument, "base_instrument_code must be specified")
	}
	if s.instrumentResolver != nil {
		if _, err := s.instrumentResolver.Resolve(ctx, req.BaseInstrumentCode); err != nil {
			if errors.Is(err, refdata.ErrUnknownInstrument) {
				return nil, status.Errorf(codes.InvalidArgument, "unknown base_instrument_code: %s", req.BaseInstrumentCode)
			}
			return nil, status.Errorf(codes.Unavailable, "instrument lookup failed for %s, please retry", req.BaseInstrumentCode)
		}
	}

	return &initiateBookingLogParams{
		AccountType:             fromProtoAccountType(req.FinancialAccountType),
		ProductServiceReference: req.ProductServiceReference,
		BusinessUnitReference:   req.BusinessUnitReference,
		ChartOfAccountsRules:    req.ChartOfAccountsRules,
		BaseCurrency:            domain.Currency(req.BaseInstrumentCode),
	}, nil
}

// capturePostingParams holds validated parameters for CaptureLedgerPosting.
type capturePostingParams struct {
	PostingAmount        domain.Money
	Direction            domain.PostingDirection
	AccountID            string
	ValueDate            time.Time
	AccountServiceDomain commonv1.AccountServiceDomain
}

// validateCapturePostingRequest validates the non-idempotency fields of a CaptureLedgerPostingRequest.
func validateCapturePostingRequest(req *financialaccountingv1.CaptureLedgerPostingRequest) (*capturePostingParams, error) {
	postingAmount, err := fromProtoMoney(req.GetPostingAmount())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid posting_amount: %v", err)
	}
	if req.PostingDirection == commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "posting_direction must be specified")
	}
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.ValueDate == nil {
		return nil, status.Error(codes.InvalidArgument, "value_date is required")
	}
	if _, ok := commonv1.AccountServiceDomain_name[int32(req.AccountServiceDomain)]; !ok {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account_service_domain: %d", req.AccountServiceDomain)
	}

	return &capturePostingParams{
		PostingAmount:        postingAmount,
		Direction:            fromProtoPostingDirection(req.PostingDirection),
		AccountID:            req.AccountId,
		ValueDate:            req.ValueDate.AsTime(),
		AccountServiceDomain: req.AccountServiceDomain,
	}, nil
}

// validateControlRequest validates the non-idempotency fields of a ControlFinancialBookingLogRequest.
func validateControlRequest(req *financialaccountingv1.ControlFinancialBookingLogRequest) (domain.ControlAction, error) {
	if req.ControlAction == financialaccountingv1.ControlAction_CONTROL_ACTION_UNSPECIFIED {
		return "", status.Error(codes.InvalidArgument, "control_action must be specified")
	}
	if req.Reason == "" {
		return "", status.Error(codes.InvalidArgument, "reason is required for control operations")
	}

	var domainAction domain.ControlAction
	switch req.ControlAction {
	case financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND:
		domainAction = domain.ControlActionSuspend
	case financialaccountingv1.ControlAction_CONTROL_ACTION_RESUME:
		domainAction = domain.ControlActionResume
	case financialaccountingv1.ControlAction_CONTROL_ACTION_TERMINATE:
		domainAction = domain.ControlActionTerminate
	case financialaccountingv1.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		return "", status.Error(codes.InvalidArgument, "control_action must be specified")
	default:
		return "", status.Errorf(codes.InvalidArgument, "invalid control_action: %d", req.ControlAction)
	}

	return domainAction, nil
}

// validateInstrumentCode validates an instrument code against the resolver if configured.
func (s *FinancialAccountingService) validateInstrumentCode(ctx context.Context, code string) error {
	if s.instrumentResolver == nil {
		return nil
	}
	if _, err := s.instrumentResolver.Resolve(ctx, code); err != nil {
		if errors.Is(err, refdata.ErrUnknownInstrument) {
			return status.Errorf(codes.InvalidArgument, "unknown instrument code: %s", code)
		}
		return status.Errorf(codes.Unavailable, "instrument lookup failed for %s, please retry", code)
	}
	return nil
}

// buildListPostingsParams builds repository query parameters from a ListLedgerPostingsRequest.
func (s *FinancialAccountingService) buildListPostingsParams(
	ctx context.Context,
	req *financialaccountingv1.ListLedgerPostingsRequest,
	pageSize int,
	pageToken string,
) (persistence.ListPostingsParams, error) {
	params := persistence.ListPostingsParams{
		PageSize:  pageSize,
		PageToken: pageToken,
	}

	if req.FinancialBookingLogId != "" {
		bookingLogID, err := parseUUID(req.FinancialBookingLogId)
		if err != nil {
			return params, status.Errorf(codes.InvalidArgument, "invalid financial_booking_log_id: %v", err)
		}
		params.BookingLogID = &bookingLogID
	}

	if len(req.AccountIds) > 0 {
		if len(req.AccountIds) > 100 {
			return params, status.Error(codes.InvalidArgument, "account_ids must not exceed 100 items")
		}
		params.AccountIDs = req.AccountIds
	} else if req.AccountId != "" {
		params.AccountID = req.AccountId
	}

	if req.PostingDirection != commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED {
		params.PostingDirection = fromProtoPostingDirection(req.PostingDirection).String()
	}

	if req.ValueDateFrom != nil {
		t := req.ValueDateFrom.AsTime()
		params.ValueDateFrom = &t
	}
	if req.ValueDateTo != nil {
		t := req.ValueDateTo.AsTime()
		params.ValueDateTo = &t
	}
	if params.ValueDateFrom != nil && params.ValueDateTo != nil && params.ValueDateFrom.After(*params.ValueDateTo) {
		return params, status.Error(codes.InvalidArgument, "value_date_from must be before or equal to value_date_to")
	}

	if req.Currency != "" {
		if err := s.validateInstrumentCode(ctx, req.Currency); err != nil {
			return params, err
		}
		params.Currency = req.Currency
	}
	if req.Status != commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
		params.Status = fromProtoTransactionStatus(req.Status).String()
	}

	return params, nil
}
