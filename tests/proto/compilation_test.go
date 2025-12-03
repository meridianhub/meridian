package proto_test

import (
	"testing"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"google.golang.org/genproto/googleapis/type/money"
)

// TestPackageImports verifies all generated proto packages can be imported.
func TestPackageImports(t *testing.T) {
	t.Run("common package imports", func(_ *testing.T) {
		// Verify common types can be imported
		_ = commonv1.ErrorCode_ERROR_CODE_UNSPECIFIED
		_ = commonv1.AccountType_ACCOUNT_TYPE_UNSPECIFIED
		_ = commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED
		_ = commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED
		_ = commonv1.Currency_CURRENCY_UNSPECIFIED
	})

	t.Run("financial_accounting package imports", func(_ *testing.T) {
		_ = financialaccountingv1.CaptureLedgerPostingRequest{}
		_ = financialaccountingv1.CaptureLedgerPostingResponse{}
		_ = financialaccountingv1.RetrieveLedgerPostingRequest{}
		_ = financialaccountingv1.RetrieveLedgerPostingResponse{}
	})

	t.Run("position_keeping package imports", func(_ *testing.T) {
		_ = positionkeepingv1.InitiateFinancialPositionLogRequest{}
		_ = positionkeepingv1.InitiateFinancialPositionLogResponse{}
	})

	t.Run("current_account package imports", func(_ *testing.T) {
		_ = currentaccountv1.InitiateCurrentAccountRequest{}
		_ = currentaccountv1.InitiateCurrentAccountResponse{}
	})

	t.Run("events package imports", func(_ *testing.T) {
		_ = eventsv1.LedgerPostingCapturedEvent{}
		_ = eventsv1.TransactionCapturedEvent{}
	})
}

// TestMessageInstantiation verifies key message types can be instantiated.
func TestMessageInstantiation(t *testing.T) {
	t.Run("Error message", func(_ *testing.T) {
		err := &commonv1.Error{
			Code:    commonv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "test error",
		}
		if err.Code != commonv1.ErrorCode_ERROR_CODE_INTERNAL {
			t.Errorf("expected ERROR_CODE_INTERNAL, got %v", err.Code)
		}
	})

	t.Run("MoneyAmount with google.type.Money", func(_ *testing.T) {
		moneyAmount := &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        500000000,
			},
		}
		if moneyAmount.Amount.CurrencyCode != "GBP" {
			t.Errorf("expected GBP, got %v", moneyAmount.Amount.CurrencyCode)
		}
	})

	t.Run("IdempotencyKey message", func(_ *testing.T) {
		key := &commonv1.IdempotencyKey{
			Key:        "test-key-12345",
			TtlSeconds: 3600,
		}
		if key.Key != "test-key-12345" {
			t.Errorf("expected 'test-key-12345', got %v", key.Key)
		}
	})
}

// TestEnumValues verifies enum constants are correctly defined.
func TestEnumValues(t *testing.T) {
	t.Run("ErrorCode enum", func(_ *testing.T) {
		if commonv1.ErrorCode_ERROR_CODE_INTERNAL != 1000 {
			t.Errorf("expected ERROR_CODE_INTERNAL = 1000, got %v", commonv1.ErrorCode_ERROR_CODE_INTERNAL)
		}
		if commonv1.ErrorCode_ERROR_CODE_INSUFFICIENT_FUNDS != 2000 {
			t.Errorf("expected ERROR_CODE_INSUFFICIENT_FUNDS = 2000, got %v", commonv1.ErrorCode_ERROR_CODE_INSUFFICIENT_FUNDS)
		}
	})

	t.Run("PostingDirection enum", func(_ *testing.T) {
		if commonv1.PostingDirection_POSTING_DIRECTION_DEBIT != 1 {
			t.Errorf("expected POSTING_DIRECTION_DEBIT = 1, got %v", commonv1.PostingDirection_POSTING_DIRECTION_DEBIT)
		}
	})

	t.Run("Currency enum", func(_ *testing.T) {
		if commonv1.Currency_CURRENCY_GBP != 1 {
			t.Errorf("expected CURRENCY_GBP = 1, got %v", commonv1.Currency_CURRENCY_GBP)
		}
	})
}
