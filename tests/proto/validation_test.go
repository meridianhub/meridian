package proto_test

import (
	"fmt"
	"os"
	"testing"

	"buf.build/go/protovalidate"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// testValidator is the shared validator instance for all validation tests.
// It is created once in TestMain to avoid expensive repeated initialization.
// Validator creation costs ~524μs (see BenchmarkValidatorCreation).
var testValidator protovalidate.Validator

// TestMain initializes the shared validator before running tests.
// This follows the recommended pattern from production code (see deposit_consumer.go:44-46).
func TestMain(m *testing.M) {
	var err error
	testValidator, err = protovalidate.New()
	if err != nil {
		panic(fmt.Sprintf("failed to create validator: %v", err))
	}
	os.Exit(m.Run())
}

// TestErrorMessageValidation verifies Error message validation constraints.
func TestErrorMessageValidation(t *testing.T) {
	validator := testValidator

	// Valid error message
	validErr := &commonv1.Error{
		Code:    commonv1.ErrorCode_ERROR_CODE_INTERNAL,
		Message: "valid error message",
	}
	if err := validator.Validate(validErr); err != nil {
		t.Errorf("valid error should pass validation: %v", err)
	}

	// Invalid: empty message (violates min_len = 1)
	invalidErr := &commonv1.Error{
		Code:    commonv1.ErrorCode_ERROR_CODE_INTERNAL,
		Message: "",
	}
	if err := validator.Validate(invalidErr); err == nil {
		t.Error("empty message should fail validation")
	}
}

// TestFieldViolationValidation verifies FieldViolation validation constraints.
func TestFieldViolationValidation(t *testing.T) {
	validator := testValidator

	// Valid field violation
	validViolation := &commonv1.FieldViolation{
		Field:       "amount",
		Description: "must be positive",
	}
	if err := validator.Validate(validViolation); err != nil {
		t.Errorf("valid field violation should pass: %v", err)
	}

	// Invalid: empty field name
	invalidViolation := &commonv1.FieldViolation{
		Field:       "",
		Description: "error description",
	}
	if err := validator.Validate(invalidViolation); err == nil {
		t.Error("empty field name should fail validation")
	}

	// Invalid: empty description
	invalidViolation2 := &commonv1.FieldViolation{
		Field:       "field_name",
		Description: "",
	}
	if err := validator.Validate(invalidViolation2); err == nil {
		t.Error("empty description should fail validation")
	}
}

// TestRetryInfoValidation verifies RetryInfo validation constraints.
func TestRetryInfoValidation(t *testing.T) {
	validator := testValidator

	// Valid retry info
	validRetry := &commonv1.RetryInfo{
		Retryable:         true,
		RetryDelaySeconds: 30,
	}
	if err := validator.Validate(validRetry); err != nil {
		t.Errorf("valid retry info should pass: %v", err)
	}

	// Invalid: negative delay
	invalidRetry := &commonv1.RetryInfo{
		Retryable:         true,
		RetryDelaySeconds: -1,
	}
	if err := validator.Validate(invalidRetry); err == nil {
		t.Error("negative delay should fail validation")
	}

	// Invalid: delay exceeds max (3600)
	invalidRetry2 := &commonv1.RetryInfo{
		Retryable:         true,
		RetryDelaySeconds: 7200,
	}
	if err := validator.Validate(invalidRetry2); err == nil {
		t.Error("delay > 3600 should fail validation")
	}
}

// TestIdempotencyKeyValidation verifies IdempotencyKey validation constraints.
func TestIdempotencyKeyValidation(t *testing.T) {
	validator := testValidator

	// Valid idempotency key
	validKey := &commonv1.IdempotencyKey{
		Key:        "valid-key-123",
		TtlSeconds: 3600,
	}
	if err := validator.Validate(validKey); err != nil {
		t.Errorf("valid idempotency key should pass: %v", err)
	}

	// Invalid: empty key
	invalidKey := &commonv1.IdempotencyKey{
		Key:        "",
		TtlSeconds: 3600,
	}
	if err := validator.Validate(invalidKey); err == nil {
		t.Error("empty key should fail validation")
	}

	// Invalid: key too long (> 255)
	invalidKey2 := &commonv1.IdempotencyKey{
		Key:        string(make([]byte, 256)),
		TtlSeconds: 3600,
	}
	if err := validator.Validate(invalidKey2); err == nil {
		t.Error("key > 255 chars should fail validation")
	}

	// Invalid: key with invalid characters
	invalidKey3 := &commonv1.IdempotencyKey{
		Key:        "invalid@key#with$special%chars",
		TtlSeconds: 3600,
	}
	if err := validator.Validate(invalidKey3); err == nil {
		t.Error("key with special characters should fail validation")
	}

	// Invalid: ttl too large (> 86400)
	invalidKey4 := &commonv1.IdempotencyKey{
		Key:        "valid-key",
		TtlSeconds: 100000,
	}
	if err := validator.Validate(invalidKey4); err == nil {
		t.Error("ttl > 86400 should fail validation")
	}
}

// TestDateRangeValidation verifies DateRange validation constraints.
func TestDateRangeValidation(t *testing.T) {
	validator := testValidator

	// Valid date range
	validRange := &commonv1.DateRange{
		StartDate: "2025-01-01",
		EndDate:   "2025-12-31",
	}
	if err := validator.Validate(validRange); err != nil {
		t.Errorf("valid date range should pass: %v", err)
	}

	// Invalid: wrong date format
	invalidRange := &commonv1.DateRange{
		StartDate: "01/01/2025",
		EndDate:   "2025-12-31",
	}
	if err := validator.Validate(invalidRange); err == nil {
		t.Error("invalid date format should fail validation")
	}

	// Invalid: incomplete date
	invalidRange2 := &commonv1.DateRange{
		StartDate: "2025-01",
		EndDate:   "2025-12-31",
	}
	if err := validator.Validate(invalidRange2); err == nil {
		t.Error("incomplete date should fail validation")
	}
}

// TestPaginationValidation verifies Pagination validation constraints.
func TestPaginationValidation(t *testing.T) {
	validator := testValidator

	// Valid pagination
	validPagination := &commonv1.Pagination{
		PageSize:  50,
		PageToken: "next-page",
	}
	if err := validator.Validate(validPagination); err != nil {
		t.Errorf("valid pagination should pass: %v", err)
	}

	// Invalid: page_size too small (< 1)
	invalidPagination := &commonv1.Pagination{
		PageSize:  0,
		PageToken: "token",
	}
	if err := validator.Validate(invalidPagination); err == nil {
		t.Error("page_size < 1 should fail validation")
	}

	// Invalid: page_size too large (> 1000)
	invalidPagination2 := &commonv1.Pagination{
		PageSize:  1001,
		PageToken: "token",
	}
	if err := validator.Validate(invalidPagination2); err == nil {
		t.Error("page_size > 1000 should fail validation")
	}
}

// TestCaptureLedgerPostingRequestValidation verifies CaptureLedgerPostingRequest validation constraints.
func TestCaptureLedgerPostingRequestValidation(t *testing.T) {
	validator := testValidator
	now := timestamppb.Now()

	// Valid request
	validReq := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: "FBL-123",
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &quantityv1.InstrumentAmount{
			Amount:         "100",
			InstrumentCode: "GBP",
			Version:        1,
		},
		AccountId: "ACC-123",
		ValueDate: now,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key:        "test-key-123",
			TtlSeconds: 3600,
		},
	}
	if err := validator.Validate(validReq); err != nil {
		t.Errorf("valid request should pass: %v", err)
	}

	// Invalid: empty financial_booking_log_id
	invalidReq := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: "",
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &quantityv1.InstrumentAmount{
			Amount:         "100",
			InstrumentCode: "GBP",
			Version:        1,
		},
		AccountId: "ACC-123",
		ValueDate: now,
	}
	if err := validator.Validate(invalidReq); err == nil {
		t.Error("empty financial_booking_log_id should fail validation")
	}

	// Invalid: UNSPECIFIED posting direction
	invalidReq2 := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: "FBL-123",
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED,
		PostingAmount: &quantityv1.InstrumentAmount{
			Amount:         "100",
			InstrumentCode: "GBP",
			Version:        1,
		},
		AccountId: "ACC-123",
		ValueDate: now,
	}
	if err := validator.Validate(invalidReq2); err == nil {
		t.Error("UNSPECIFIED posting direction should fail validation")
	}

	// Invalid: empty account_id
	invalidReq3 := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: "FBL-123",
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &quantityv1.InstrumentAmount{
			Amount:         "100",
			InstrumentCode: "GBP",
			Version:        1,
		},
		AccountId: "",
		ValueDate: now,
	}
	if err := validator.Validate(invalidReq3); err == nil {
		t.Error("empty account_id should fail validation")
	}
}

// TestRetrieveLedgerPostingRequestValidation verifies RetrieveLedgerPostingRequest validation constraints.
func TestRetrieveLedgerPostingRequestValidation(t *testing.T) {
	validator := testValidator

	// Valid request
	validReq := &financialaccountingv1.RetrieveLedgerPostingRequest{
		Id: "LP-123",
	}
	if err := validator.Validate(validReq); err != nil {
		t.Errorf("valid request should pass: %v", err)
	}

	// Invalid: empty id
	invalidReq := &financialaccountingv1.RetrieveLedgerPostingRequest{
		Id: "",
	}
	if err := validator.Validate(invalidReq); err == nil {
		t.Error("empty id should fail validation")
	}
}

// TestUpdateLedgerPostingRequestValidation verifies UpdateLedgerPostingRequest validation constraints.
func TestUpdateLedgerPostingRequestValidation(t *testing.T) {
	validator := testValidator

	// Valid request (includes required idempotency_key)
	validReq := &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:     "LP-123",
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "test-key-update-posting",
		},
	}
	if err := validator.Validate(validReq); err != nil {
		t.Errorf("valid request should pass: %v", err)
	}

	// Invalid: empty id
	invalidReq := &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:     "",
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "test-key-update-posting",
		},
	}
	if err := validator.Validate(invalidReq); err == nil {
		t.Error("empty id should fail validation")
	}

	// Invalid: UNSPECIFIED status
	invalidReq2 := &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:     "LP-123",
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "test-key-update-posting",
		},
	}
	if err := validator.Validate(invalidReq2); err == nil {
		t.Error("UNSPECIFIED status should fail validation")
	}

	// Invalid: missing idempotency_key (required for state-machine mutations)
	invalidReq3 := &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:     "LP-123",
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
	}
	if err := validator.Validate(invalidReq3); err == nil {
		t.Error("missing idempotency_key should fail validation")
	}
}

// TestPostingDirectionEnumValidation verifies PostingDirection enum constraints.
func TestPostingDirectionEnumValidation(t *testing.T) {
	validator := testValidator
	now := timestamppb.Now()

	// Valid: DEBIT direction
	validReq := &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: "FBL-123",
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		PostingAmount: &quantityv1.InstrumentAmount{
			Amount:         "100",
			InstrumentCode: "GBP",
			Version:        1,
		},
		AccountId: "ACC-123",
		ValueDate: now,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key:        "test-key-456",
			TtlSeconds: 3600,
		},
	}
	if err := validator.Validate(validReq); err != nil {
		t.Errorf("valid DEBIT direction should pass: %v", err)
	}

	// Valid: CREDIT direction
	validReq.PostingDirection = commonv1.PostingDirection_POSTING_DIRECTION_CREDIT
	if err := validator.Validate(validReq); err != nil {
		t.Errorf("valid CREDIT direction should pass: %v", err)
	}

	// Invalid: UNSPECIFIED (not_in: [0])
	validReq.PostingDirection = commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED
	if err := validator.Validate(validReq); err == nil {
		t.Error("UNSPECIFIED direction should fail validation (not_in constraint)")
	}
}

// TestTransactionStatusEnumValidation verifies TransactionStatus enum constraints.
func TestTransactionStatusEnumValidation(t *testing.T) {
	validator := testValidator

	// Valid status (includes required idempotency_key)
	validReq := &financialaccountingv1.UpdateLedgerPostingRequest{
		Id:     "LP-123",
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: "test-key-status-validation",
		},
	}
	if err := validator.Validate(validReq); err != nil {
		t.Errorf("valid status should pass: %v", err)
	}

	// Invalid: UNSPECIFIED (not_in: [0])
	validReq.Status = commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED
	if err := validator.Validate(validReq); err == nil {
		t.Error("UNSPECIFIED status should fail validation (not_in constraint)")
	}
}
