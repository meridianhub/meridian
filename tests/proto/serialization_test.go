package proto_test

import (
	"bytes"
	"testing"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestProtoSerialization verifies protobuf messages can be serialized and deserialized.
func TestProtoSerialization(t *testing.T) {
	t.Run("Error message serialization", func(t *testing.T) {
		original := &commonv1.Error{
			Code:    commonv1.ErrorCode_ERROR_CODE_NOT_FOUND,
			Message: "resource not found",
			TraceId: "trace-123",
		}

		data, err := proto.Marshal(original)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		decoded := &commonv1.Error{}
		if err := proto.Unmarshal(data, decoded); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if decoded.Code != original.Code {
			t.Errorf("code mismatch: got %v, want %v", decoded.Code, original.Code)
		}
		if decoded.Message != original.Message {
			t.Errorf("message mismatch: got %v, want %v", decoded.Message, original.Message)
		}
	})

	t.Run("MoneyAmount message serialization", func(t *testing.T) {
		original := &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        500000000,
			},
		}

		data, err := proto.Marshal(original)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		decoded := &commonv1.MoneyAmount{}
		if err := proto.Unmarshal(data, decoded); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if decoded.Amount.CurrencyCode != original.Amount.CurrencyCode {
			t.Errorf("currency mismatch: got %v, want %v", decoded.Amount.CurrencyCode, original.Amount.CurrencyCode)
		}
	})

	t.Run("LedgerPosting message serialization", func(t *testing.T) {
		now := timestamppb.Now()
		original := &financialaccountingv1.LedgerPosting{
			Id:                    "123e4567-e89b-12d3-a456-426614174000",
			FinancialBookingLogId: "223e4567-e89b-12d3-a456-426614174001",
			PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			PostingAmount: &quantityv1.InstrumentAmount{
				Amount:         "250.75",
				InstrumentCode: "USD",
				Version:        1,
			},
			AccountId: "ACC-98765",
			ValueDate: now,
			Status:    commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		}

		data, err := proto.Marshal(original)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		decoded := &financialaccountingv1.LedgerPosting{}
		if err := proto.Unmarshal(data, decoded); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if decoded.Id != original.Id {
			t.Errorf("id mismatch: got %v, want %v", decoded.Id, original.Id)
		}
		if decoded.PostingDirection != original.PostingDirection {
			t.Errorf("posting_direction mismatch: got %v, want %v", decoded.PostingDirection, original.PostingDirection)
		}
		if decoded.PostingAmount == nil {
			t.Fatal("posting_amount mismatch: got nil, want non-nil")
		}
		if decoded.PostingAmount.Amount != original.PostingAmount.Amount {
			t.Errorf("posting_amount.amount mismatch: got %v, want %v", decoded.PostingAmount.Amount, original.PostingAmount.Amount)
		}
		if decoded.PostingAmount.InstrumentCode != original.PostingAmount.InstrumentCode {
			t.Errorf("posting_amount.instrument_code mismatch: got %v, want %v", decoded.PostingAmount.InstrumentCode, original.PostingAmount.InstrumentCode)
		}
		if decoded.PostingAmount.Version != original.PostingAmount.Version {
			t.Errorf("posting_amount.version mismatch: got %v, want %v", decoded.PostingAmount.Version, original.PostingAmount.Version)
		}
	})

	t.Run("Event message serialization", func(t *testing.T) {
		now := timestamppb.Now()
		original := &eventsv1.LedgerPostingCapturedEvent{
			PostingId:        "LP-123",
			BookingLogId:     "FBL-456",
			PostingDirection: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			PostingAmount: &quantityv1.InstrumentAmount{
				Amount:         "1000.00",
				InstrumentCode: "GBP",
			},
			AccountId: "ACC-789",
			ValueDate: now,
		}

		data, err := proto.Marshal(original)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		decoded := &eventsv1.LedgerPostingCapturedEvent{}
		if err := proto.Unmarshal(data, decoded); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if decoded.PostingId != original.PostingId {
			t.Errorf("posting_id mismatch: got %v, want %v", decoded.PostingId, original.PostingId)
		}
	})
}

// TestProtoDeterministicSerialization verifies that deterministic marshaling works.
func TestProtoDeterministicSerialization(t *testing.T) {
	t.Run("deterministic marshaling", func(t *testing.T) {
		msg := &commonv1.Error{
			Code:    commonv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "test error",
			TraceId: "trace-xyz",
		}

		opts := proto.MarshalOptions{Deterministic: true}
		data1, err := opts.Marshal(msg)
		if err != nil {
			t.Fatalf("first marshal failed: %v", err)
		}

		data2, err := opts.Marshal(msg)
		if err != nil {
			t.Fatalf("second marshal failed: %v", err)
		}

		if !bytes.Equal(data1, data2) {
			t.Error("deterministic marshaling produced different results")
		}
	})
}

// TestProtoClone verifies that proto.Clone works correctly.
func TestProtoClone(t *testing.T) {
	t.Run("clone message", func(t *testing.T) {
		original := &commonv1.Error{
			Code:    commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "invalid input",
		}

		cloned := proto.Clone(original).(*commonv1.Error)

		if !proto.Equal(original, cloned) {
			t.Error("cloned message not equal to original")
		}

		cloned.Message = "modified message"

		if original.Message != "invalid input" {
			t.Error("original message was modified when clone was changed")
		}
	})
}

// TestProtoReset verifies that proto.Reset works correctly.
func TestProtoReset(t *testing.T) {
	t.Run("reset message", func(t *testing.T) {
		msg := &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        100,
				Nanos:        500000000,
			},
		}

		proto.Reset(msg)

		if msg.Amount != nil {
			t.Error("message was not reset correctly")
		}
	})
}

// TestProtoEqual verifies that proto.Equal works correctly.
func TestProtoEqual(t *testing.T) {
	t.Run("equal messages", func(t *testing.T) {
		msg1 := &commonv1.IdempotencyKey{
			Key:        "test-key",
			TtlSeconds: 3600,
		}
		msg2 := &commonv1.IdempotencyKey{
			Key:        "test-key",
			TtlSeconds: 3600,
		}

		if !proto.Equal(msg1, msg2) {
			t.Error("identical messages reported as not equal")
		}
	})

	t.Run("unequal messages", func(t *testing.T) {
		msg1 := &commonv1.IdempotencyKey{
			Key:        "test-key-1",
			TtlSeconds: 3600,
		}
		msg2 := &commonv1.IdempotencyKey{
			Key:        "test-key-2",
			TtlSeconds: 3600,
		}

		if proto.Equal(msg1, msg2) {
			t.Error("different messages reported as equal")
		}
	})
}
