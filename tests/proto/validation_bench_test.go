package proto_test

import (
	"testing"

	"buf.build/go/protovalidate"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// BenchmarkValidatorCreation measures the cost of creating a new validator.
// This is important as validators should be created once and reused.
func BenchmarkValidatorCreation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := protovalidate.New()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSimpleMessageValidation measures validation of a simple message.
func BenchmarkSimpleMessageValidation(b *testing.B) {
	validator, err := protovalidate.New()
	if err != nil {
		b.Fatal(err)
	}

	msg := &commonv1.Error{
		Code:    commonv1.ErrorCode_ERROR_CODE_INTERNAL,
		Message: "test error message",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validator.Validate(msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkComplexMessageValidation measures validation of a complex nested message.
func BenchmarkComplexMessageValidation(b *testing.B) {
	validator, err := protovalidate.New()
	if err != nil {
		b.Fatal(err)
	}

	msg := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: "FBL-123",
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &quantityv1.InstrumentAmount{
			Amount:         "100.5",
			InstrumentCode: "GBP",
			Version:        1,
		},
		AccountId: "ACC-123",
		ValueDate: timestamppb.Now(),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key:        "bench-key-123",
			TtlSeconds: 3600,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validator.Validate(msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInvalidMessageValidation measures validation performance when messages fail.
// This is important as validation errors should be returned quickly.
func BenchmarkInvalidMessageValidation(b *testing.B) {
	validator, err := protovalidate.New()
	if err != nil {
		b.Fatal(err)
	}

	msg := &commonv1.Error{
		Code:    commonv1.ErrorCode_ERROR_CODE_INTERNAL,
		Message: "", // Invalid: empty message
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Expect error, don't check it in benchmark
		_ = validator.Validate(msg)
	}
}

// BenchmarkEnumValidation measures validation of enum constraints.
func BenchmarkEnumValidation(b *testing.B) {
	validator, err := protovalidate.New()
	if err != nil {
		b.Fatal(err)
	}

	msg := &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:     "LP-123",
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validator.Validate(msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStringPatternValidation measures validation of string patterns (regex).
func BenchmarkStringPatternValidation(b *testing.B) {
	validator, err := protovalidate.New()
	if err != nil {
		b.Fatal(err)
	}

	msg := &commonv1.DateRange{
		StartDate: "2025-01-01",
		EndDate:   "2025-12-31",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validator.Validate(msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRangeValidation measures validation of numeric ranges.
func BenchmarkRangeValidation(b *testing.B) {
	validator, err := protovalidate.New()
	if err != nil {
		b.Fatal(err)
	}

	msg := &commonv1.Pagination{
		PageSize:  50,
		PageToken: "token",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validator.Validate(msg); err != nil {
			b.Fatal(err)
		}
	}
}
