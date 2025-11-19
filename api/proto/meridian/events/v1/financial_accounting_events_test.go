package eventsv1_test

import (
	"testing"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	money "google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestFinancialBookingLogInitiatedEvent_Serialization(t *testing.T) {
	event := &eventsv1.FinancialBookingLogInitiatedEvent{
		BookingLogId:            "booking-log-123",
		FinancialAccountType:    commonv1.AccountType_ACCOUNT_TYPE_DEBIT,
		ProductServiceReference: "product-456",
		BusinessUnitReference:   "bu-789",
		BaseCurrency:            commonv1.Currency_CURRENCY_GBP,
		CorrelationId:           "correlation-abc",
		CausationId:             "causation-def",
		Timestamp:               timestamppb.New(time.Now()),
		Version:                 1,
	}

	// Marshal to bytes
	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	// Unmarshal back
	decoded := &eventsv1.FinancialBookingLogInitiatedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	// Verify fields
	if decoded.BookingLogId != event.BookingLogId {
		t.Errorf("BookingLogId mismatch: got %v, want %v", decoded.BookingLogId, event.BookingLogId)
	}
	if decoded.FinancialAccountType != event.FinancialAccountType {
		t.Errorf("FinancialAccountType mismatch: got %v, want %v", decoded.FinancialAccountType, event.FinancialAccountType)
	}
	if decoded.BaseCurrency != event.BaseCurrency {
		t.Errorf("BaseCurrency mismatch: got %v, want %v", decoded.BaseCurrency, event.BaseCurrency)
	}
	if decoded.Version != event.Version {
		t.Errorf("Version mismatch: got %v, want %v", decoded.Version, event.Version)
	}
}

func TestLedgerPostingCapturedEvent_Serialization(t *testing.T) {
	event := &eventsv1.LedgerPostingCapturedEvent{
		PostingId:        "posting-123",
		BookingLogId:     "booking-log-456",
		PostingDirection: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        100,
			Nanos:        50000000,
		},
		AccountId:     "account-789",
		ValueDate:     timestamppb.New(time.Now()),
		Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		CorrelationId: "correlation-xyz",
		CausationId:   "causation-abc",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       1,
	}

	// Marshal to bytes
	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	// Unmarshal back
	decoded := &eventsv1.LedgerPostingCapturedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	// Verify fields
	if decoded.PostingId != event.PostingId {
		t.Errorf("PostingId mismatch: got %v, want %v", decoded.PostingId, event.PostingId)
	}
	if decoded.PostingDirection != event.PostingDirection {
		t.Errorf("PostingDirection mismatch: got %v, want %v", decoded.PostingDirection, event.PostingDirection)
	}
	if decoded.PostingAmount.Units != event.PostingAmount.Units {
		t.Errorf("PostingAmount.Units mismatch: got %v, want %v", decoded.PostingAmount.Units, event.PostingAmount.Units)
	}
	if decoded.AccountId != event.AccountId {
		t.Errorf("AccountId mismatch: got %v, want %v", decoded.AccountId, event.AccountId)
	}
}

func TestFinancialBookingLogPostedEvent_Serialization(t *testing.T) {
	event := &eventsv1.FinancialBookingLogPostedEvent{
		BookingLogId: "booking-log-123",
		PostingCount: 4,
		TotalDebits: &money.Money{
			CurrencyCode: "GBP",
			Units:        500,
			Nanos:        0,
		},
		TotalCredits: &money.Money{
			CurrencyCode: "GBP",
			Units:        500,
			Nanos:        0,
		},
		Reason:        "Monthly closing",
		PostedBy:      "user-123",
		CorrelationId: "correlation-abc",
		CausationId:   "causation-def",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       1,
	}

	// Marshal to bytes
	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	// Unmarshal back
	decoded := &eventsv1.FinancialBookingLogPostedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	// Verify fields
	if decoded.BookingLogId != event.BookingLogId {
		t.Errorf("BookingLogId mismatch: got %v, want %v", decoded.BookingLogId, event.BookingLogId)
	}
	if decoded.PostingCount != event.PostingCount {
		t.Errorf("PostingCount mismatch: got %v, want %v", decoded.PostingCount, event.PostingCount)
	}
	if decoded.TotalDebits.Units != event.TotalDebits.Units {
		t.Errorf("TotalDebits.Units mismatch: got %v, want %v", decoded.TotalDebits.Units, event.TotalDebits.Units)
	}
	if decoded.PostedBy != event.PostedBy {
		t.Errorf("PostedBy mismatch: got %v, want %v", decoded.PostedBy, event.PostedBy)
	}
}

func TestBalanceValidationFailedEvent_Serialization(t *testing.T) {
	event := &eventsv1.BalanceValidationFailedEvent{
		BookingLogId: "booking-log-123",
		TotalDebits: &money.Money{
			CurrencyCode: "GBP",
			Units:        500,
			Nanos:        0,
		},
		TotalCredits: &money.Money{
			CurrencyCode: "GBP",
			Units:        490,
			Nanos:        0,
		},
		Variance: &money.Money{
			CurrencyCode: "GBP",
			Units:        10,
			Nanos:        0,
		},
		Reason:        "Debits and credits do not balance",
		CorrelationId: "correlation-abc",
		CausationId:   "causation-def",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       1,
	}

	// Marshal to bytes
	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	// Unmarshal back
	decoded := &eventsv1.BalanceValidationFailedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	// Verify fields
	if decoded.BookingLogId != event.BookingLogId {
		t.Errorf("BookingLogId mismatch: got %v, want %v", decoded.BookingLogId, event.BookingLogId)
	}
	if decoded.Variance.Units != event.Variance.Units {
		t.Errorf("Variance.Units mismatch: got %v, want %v", decoded.Variance.Units, event.Variance.Units)
	}
	if decoded.Reason != event.Reason {
		t.Errorf("Reason mismatch: got %v, want %v", decoded.Reason, event.Reason)
	}
}

func TestFinancialBookingLogClosedEvent_Serialization(t *testing.T) {
	event := &eventsv1.FinancialBookingLogClosedEvent{
		BookingLogId:  "booking-log-123",
		Reason:        "End of fiscal year",
		ClosedBy:      "admin-456",
		CorrelationId: "correlation-abc",
		CausationId:   "causation-def",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       1,
	}

	// Marshal to bytes
	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	// Unmarshal back
	decoded := &eventsv1.FinancialBookingLogClosedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	// Verify fields
	if decoded.BookingLogId != event.BookingLogId {
		t.Errorf("BookingLogId mismatch: got %v, want %v", decoded.BookingLogId, event.BookingLogId)
	}
	if decoded.Reason != event.Reason {
		t.Errorf("Reason mismatch: got %v, want %v", decoded.Reason, event.Reason)
	}
	if decoded.ClosedBy != event.ClosedBy {
		t.Errorf("ClosedBy mismatch: got %v, want %v", decoded.ClosedBy, event.ClosedBy)
	}
}
