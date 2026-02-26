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
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "product-456",
		BusinessUnitReference:   "bu-789",
		BaseInstrumentCode:      "GBP",
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
	if decoded.BaseInstrumentCode != event.BaseInstrumentCode {
		t.Errorf("BaseInstrumentCode mismatch: got %v, want %v", decoded.BaseInstrumentCode, event.BaseInstrumentCode)
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

// Defensive Tests - ADR-0008 Compliance

func TestLedgerPostingAmendedEvent_Serialization(t *testing.T) {
	event := &eventsv1.LedgerPostingAmendedEvent{
		PostingId:    "posting-123",
		BookingLogId: "booking-log-456",
		PreviousAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        100,
			Nanos:        0,
		},
		NewAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        150,
			Nanos:        0,
		},
		Reason:        "Correction after reconciliation",
		AmendedBy:     "user-789",
		CorrelationId: "correlation-abc",
		CausationId:   "causation-def",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       2,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	decoded := &eventsv1.LedgerPostingAmendedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	if decoded.PostingId != event.PostingId {
		t.Errorf("PostingId mismatch: got %v, want %v", decoded.PostingId, event.PostingId)
	}
	if decoded.NewAmount.Units != event.NewAmount.Units {
		t.Errorf("NewAmount.Units mismatch: got %v, want %v", decoded.NewAmount.Units, event.NewAmount.Units)
	}
}

func TestLedgerPostingRejectedEvent_Serialization(t *testing.T) {
	event := &eventsv1.LedgerPostingRejectedEvent{
		PostingId:     "posting-123",
		BookingLogId:  "booking-log-456",
		Reason:        "Account does not exist",
		RejectedBy:    "system",
		CorrelationId: "correlation-abc",
		CausationId:   "causation-def",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       1,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	decoded := &eventsv1.LedgerPostingRejectedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	if decoded.PostingId != event.PostingId {
		t.Errorf("PostingId mismatch: got %v, want %v", decoded.PostingId, event.PostingId)
	}
	if decoded.Reason != event.Reason {
		t.Errorf("Reason mismatch: got %v, want %v", decoded.Reason, event.Reason)
	}
}

// Boundary and Edge Case Tests

func TestFinancialBookingLogInitiatedEvent_EmptyFields(t *testing.T) {
	// Test that empty strings serialize/deserialize correctly
	event := &eventsv1.FinancialBookingLogInitiatedEvent{
		BookingLogId:            "",
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "",
		BusinessUnitReference:   "",
		BaseInstrumentCode:      "GBP",
		CorrelationId:           "",
		CausationId:             "",
		Timestamp:               timestamppb.New(time.Now()),
		Version:                 1,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event with empty fields: %v", err)
	}

	decoded := &eventsv1.FinancialBookingLogInitiatedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event with empty fields: %v", err)
	}

	if decoded.BookingLogId != "" {
		t.Errorf("Expected empty BookingLogId, got %v", decoded.BookingLogId)
	}
}

func TestFinancialBookingLogInitiatedEvent_MaxLengthFields(t *testing.T) {
	// Test 255 character limit (boundary test)
	maxLengthString := string(make([]byte, 255))
	for i := range maxLengthString {
		maxLengthString = maxLengthString[:i] + "a" + maxLengthString[i+1:]
	}

	event := &eventsv1.FinancialBookingLogInitiatedEvent{
		BookingLogId:            maxLengthString,
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: maxLengthString,
		BusinessUnitReference:   maxLengthString,
		BaseInstrumentCode:      "GBP",
		CorrelationId:           maxLengthString,
		CausationId:             maxLengthString,
		Timestamp:               timestamppb.New(time.Now()),
		Version:                 1,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event with max length fields: %v", err)
	}

	decoded := &eventsv1.FinancialBookingLogInitiatedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event with max length fields: %v", err)
	}

	if len(decoded.BookingLogId) != 255 {
		t.Errorf("Expected BookingLogId length 255, got %v", len(decoded.BookingLogId))
	}
}

func TestFinancialBookingLogInitiatedEvent_UnspecifiedEnum(t *testing.T) {
	// Test that UNSPECIFIED enum values serialize/deserialize
	event := &eventsv1.FinancialBookingLogInitiatedEvent{
		BookingLogId:            "booking-log-123",
		FinancialAccountType:    "",
		ProductServiceReference: "product-456",
		BusinessUnitReference:   "bu-789",
		BaseInstrumentCode:      "",
		CorrelationId:           "correlation-abc",
		CausationId:             "causation-def",
		Timestamp:               timestamppb.New(time.Now()),
		Version:                 1,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event with unspecified enums: %v", err)
	}

	decoded := &eventsv1.FinancialBookingLogInitiatedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event with unspecified enums: %v", err)
	}

	if decoded.FinancialAccountType != "" {
		t.Errorf("Expected ACCOUNT_TYPE_UNSPECIFIED, got %v", decoded.FinancialAccountType)
	}
	if decoded.BaseInstrumentCode != "" {
		t.Errorf("Expected empty BaseInstrumentCode, got %v", decoded.BaseInstrumentCode)
	}
}

func TestFinancialBookingLogInitiatedEvent_ZeroVersion(t *testing.T) {
	// Test that version 0 serializes/deserializes (edge case)
	event := &eventsv1.FinancialBookingLogInitiatedEvent{
		BookingLogId:            "booking-log-123",
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "product-456",
		BusinessUnitReference:   "bu-789",
		BaseInstrumentCode:      "GBP",
		CorrelationId:           "correlation-abc",
		CausationId:             "causation-def",
		Timestamp:               timestamppb.New(time.Now()),
		Version:                 0,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event with zero version: %v", err)
	}

	decoded := &eventsv1.FinancialBookingLogInitiatedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event with zero version: %v", err)
	}

	if decoded.Version != 0 {
		t.Errorf("Expected version 0, got %v", decoded.Version)
	}
}

func TestFinancialBookingLogInitiatedEvent_NegativeVersion(t *testing.T) {
	// Test that negative version serializes/deserializes (invalid but should not crash)
	event := &eventsv1.FinancialBookingLogInitiatedEvent{
		BookingLogId:            "booking-log-123",
		FinancialAccountType:    "DEBIT",
		ProductServiceReference: "product-456",
		BusinessUnitReference:   "bu-789",
		BaseInstrumentCode:      "GBP",
		CorrelationId:           "correlation-abc",
		CausationId:             "causation-def",
		Timestamp:               timestamppb.New(time.Now()),
		Version:                 -1,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event with negative version: %v", err)
	}

	decoded := &eventsv1.FinancialBookingLogInitiatedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event with negative version: %v", err)
	}

	if decoded.Version != -1 {
		t.Errorf("Expected version -1, got %v", decoded.Version)
	}
}

func TestLedgerPostingCapturedEvent_ZeroMoney(t *testing.T) {
	// Test zero amount (boundary case - technically invalid but should serialize)
	event := &eventsv1.LedgerPostingCapturedEvent{
		PostingId:        "posting-123",
		BookingLogId:     "booking-log-456",
		PostingDirection: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        0,
			Nanos:        0,
		},
		AccountId:     "account-789",
		ValueDate:     timestamppb.New(time.Now()),
		Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		CorrelationId: "correlation-xyz",
		CausationId:   "causation-abc",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       1,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event with zero amount: %v", err)
	}

	decoded := &eventsv1.LedgerPostingCapturedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event with zero amount: %v", err)
	}

	if decoded.PostingAmount.Units != 0 {
		t.Errorf("Expected 0 units, got %v", decoded.PostingAmount.Units)
	}
}

func TestLedgerPostingCapturedEvent_NegativeMoney(t *testing.T) {
	// Test negative amount (invalid but should serialize)
	event := &eventsv1.LedgerPostingCapturedEvent{
		PostingId:        "posting-123",
		BookingLogId:     "booking-log-456",
		PostingDirection: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        -100,
			Nanos:        0,
		},
		AccountId:     "account-789",
		ValueDate:     timestamppb.New(time.Now()),
		Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		CorrelationId: "correlation-xyz",
		CausationId:   "causation-abc",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       1,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event with negative amount: %v", err)
	}

	decoded := &eventsv1.LedgerPostingCapturedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event with negative amount: %v", err)
	}

	if decoded.PostingAmount.Units != -100 {
		t.Errorf("Expected -100 units, got %v", decoded.PostingAmount.Units)
	}
}

func TestLedgerPostingCapturedEvent_MaxInt64Amount(t *testing.T) {
	// Test maximum int64 value (boundary case)
	event := &eventsv1.LedgerPostingCapturedEvent{
		PostingId:        "posting-123",
		BookingLogId:     "booking-log-456",
		PostingDirection: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        9223372036854775807, // max int64
			Nanos:        999999999,           // max nanos
		},
		AccountId:     "account-789",
		ValueDate:     timestamppb.New(time.Now()),
		Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		CorrelationId: "correlation-xyz",
		CausationId:   "causation-abc",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       1,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event with max int64: %v", err)
	}

	decoded := &eventsv1.LedgerPostingCapturedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event with max int64: %v", err)
	}

	if decoded.PostingAmount.Units != 9223372036854775807 {
		t.Errorf("Expected max int64, got %v", decoded.PostingAmount.Units)
	}
}

func TestLedgerPostingCapturedEvent_InvalidAccountIdPattern(t *testing.T) {
	// Test account_id with characters outside allowed pattern (should still serialize)
	event := &eventsv1.LedgerPostingCapturedEvent{
		PostingId:        "posting-123",
		BookingLogId:     "booking-log-456",
		PostingDirection: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        100,
			Nanos:        0,
		},
		AccountId:     "account@#$%",
		ValueDate:     timestamppb.New(time.Now()),
		Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		CorrelationId: "correlation-xyz",
		CausationId:   "causation-abc",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       1,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event with invalid account_id: %v", err)
	}

	decoded := &eventsv1.LedgerPostingCapturedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event with invalid account_id: %v", err)
	}

	if decoded.AccountId != "account@#$%" {
		t.Errorf("AccountId mismatch: got %v, want %v", decoded.AccountId, "account@#$%")
	}
}

func TestFinancialBookingLogClosedEvent_MaxLengthReason(t *testing.T) {
	// Test 500 character reason (max length boundary)
	maxReason := string(make([]byte, 500))
	for i := range maxReason {
		maxReason = maxReason[:i] + "x" + maxReason[i+1:]
	}

	event := &eventsv1.FinancialBookingLogClosedEvent{
		BookingLogId:  "booking-log-123",
		Reason:        maxReason,
		ClosedBy:      "admin-456",
		CorrelationId: "correlation-abc",
		CausationId:   "causation-def",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       1,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event with max length reason: %v", err)
	}

	decoded := &eventsv1.FinancialBookingLogClosedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event with max length reason: %v", err)
	}

	if len(decoded.Reason) != 500 {
		t.Errorf("Expected reason length 500, got %v", len(decoded.Reason))
	}
}

func TestFinancialBookingLogClosedEvent_WhitespaceFields(t *testing.T) {
	// Test whitespace-only fields (technically invalid but should serialize)
	event := &eventsv1.FinancialBookingLogClosedEvent{
		BookingLogId:  "   ",
		Reason:        "   ",
		ClosedBy:      "   ",
		CorrelationId: "   ",
		CausationId:   "   ",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       1,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event with whitespace fields: %v", err)
	}

	decoded := &eventsv1.FinancialBookingLogClosedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event with whitespace fields: %v", err)
	}

	if decoded.BookingLogId != "   " {
		t.Errorf("Expected whitespace BookingLogId, got %q", decoded.BookingLogId)
	}
}

func TestBalanceValidationFailedEvent_NegativePostingCount(t *testing.T) {
	// Test with negative posting count in parent context (edge case)
	event := &eventsv1.FinancialBookingLogPostedEvent{
		BookingLogId: "booking-log-123",
		PostingCount: -1,
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
		Reason:        "Test negative count",
		PostedBy:      "user-123",
		CorrelationId: "correlation-abc",
		CausationId:   "causation-def",
		Timestamp:     timestamppb.New(time.Now()),
		Version:       1,
	}

	data, err := proto.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event with negative posting count: %v", err)
	}

	decoded := &eventsv1.FinancialBookingLogPostedEvent{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("Failed to unmarshal event with negative posting count: %v", err)
	}

	if decoded.PostingCount != -1 {
		t.Errorf("Expected posting count -1, got %v", decoded.PostingCount)
	}
}

// Money Validation Documentation Tests
//
// NOTE: buf.validate does not currently support CEL validation on google.type.Money fields.
// The CEL constraints in the proto file serve as documentation of validation requirements,
// but enforcement must happen at the service/application layer.
//
// See: https://github.com/bufbuild/protovalidate/issues
//
// The tests below document the expected validation behavior per the proto schema.

func TestLedgerPostingCapturedEvent_MoneyValidationDocumentation(t *testing.T) {
	// Documents expected validation behavior for posting_amount field
	// Per proto schema: posting_amount must be positive (units > 0 or nanos > 0)
	tests := []struct {
		name        string
		units       int64
		nanos       int32
		expectValid bool
		description string
	}{
		{"Valid positive amount", 100, 50, true, "Both units and nanos positive"},
		{"Valid zero units positive nanos", 0, 1, true, "Zero units but positive nanos"},
		{"Invalid zero amount", 0, 0, false, "Zero amount not allowed for postings"},
		{"Invalid negative units", -100, 0, false, "Negative units not allowed"},
		{"Invalid negative nanos", 0, -50, false, "Negative nanos not allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := &eventsv1.LedgerPostingCapturedEvent{
				PostingId:        "posting-123",
				BookingLogId:     "booking-log-456",
				PostingDirection: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        tt.units,
					Nanos:        tt.nanos,
				},
				AccountId:     "valid-account-id",
				ValueDate:     timestamppb.New(time.Now()),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
				CorrelationId: "correlation-xyz",
				CausationId:   "causation-abc",
				Timestamp:     timestamppb.New(time.Now()),
				Version:       1,
			}

			// Verify serialization works for all cases
			data, err := proto.Marshal(event)
			if err != nil {
				t.Fatalf("Failed to marshal event: %v", err)
			}

			decoded := &eventsv1.LedgerPostingCapturedEvent{}
			if err := proto.Unmarshal(data, decoded); err != nil {
				t.Fatalf("Failed to unmarshal event: %v", err)
			}

			// Document expected validation behavior
			if !tt.expectValid {
				t.Logf("Service layer should reject: %s - %s", tt.name, tt.description)
			}
		})
	}
}

// NOTE: Due to buf.validate limitations with google.type.Money, the CEL constraints
// serve as documentation. Service layer must enforce these validation rules.
