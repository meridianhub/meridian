package persistence

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBillingRunToEntity_And_ToDomain(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	run := &domain.BillingRun{
		ID:            uuid.New(),
		TenantID:      "tenant-1",
		CycleStart:    now.Add(-24 * time.Hour),
		CycleEnd:      now,
		Status:        domain.BillingRunStatusInitiated,
		DunningLevel:  2,
		FailureReason: "some failure",
		LastRetryAt:   &now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	entity := billingRunToEntity(run)
	assert.Equal(t, run.ID, entity.ID)
	assert.Equal(t, "tenant-1", entity.TenantID)
	assert.Equal(t, string(domain.BillingRunStatusInitiated), entity.Status)
	assert.Equal(t, 2, entity.DunningLevel)
	require.NotNil(t, entity.FailureReason)
	assert.Equal(t, "some failure", *entity.FailureReason)

	back := billingRunToDomain(entity)
	assert.Equal(t, run.ID, back.ID)
	assert.Equal(t, run.TenantID, back.TenantID)
	assert.Equal(t, run.Status, back.Status)
	assert.Equal(t, run.DunningLevel, back.DunningLevel)
	assert.Equal(t, run.FailureReason, back.FailureReason)
}

func TestBillingRunToEntity_NoFailureReason(t *testing.T) {
	t.Parallel()

	run := &domain.BillingRun{
		ID:       uuid.New(),
		TenantID: "tenant-1",
		Status:   domain.BillingRunStatusCompleted,
	}

	entity := billingRunToEntity(run)
	assert.Nil(t, entity.FailureReason)

	back := billingRunToDomain(entity)
	assert.Empty(t, back.FailureReason)
}

func TestInvoiceToEntity_And_ToDomain(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	inv := &domain.Invoice{
		ID:            uuid.New(),
		BillingRunID:  uuid.New(),
		PartyID:       "party-1",
		AccountID:     "acc-1",
		InvoiceNumber: "INV-001",
		PeriodStart:   now.Add(-30 * 24 * time.Hour),
		PeriodEnd:     now,
		LineItems: []domain.InvoiceLineItem{
			{Description: "Usage", Quantity: decimal.NewFromInt(1), UnitPriceCents: 1000, TotalCents: 1000},
		},
		SubtotalCents:  1000,
		Currency:       "GBP",
		Status:         domain.InvoiceStatusDraft,
		PaymentOrderID: nil,
		CreatedAt:      now,
	}

	entity, err := invoiceToEntity(inv)
	require.NoError(t, err)
	assert.Equal(t, inv.ID, entity.ID)
	assert.Equal(t, "party-1", entity.PartyID)
	assert.Equal(t, "INV-001", entity.InvoiceNumber)
	assert.Equal(t, int64(1000), entity.SubtotalCents)

	// Verify line items are serialized as JSON
	var items []domain.InvoiceLineItem
	require.NoError(t, json.Unmarshal([]byte(entity.LineItems), &items))
	assert.Len(t, items, 1)
	assert.Equal(t, "Usage", items[0].Description)

	back, err := invoiceToDomain(entity)
	require.NoError(t, err)
	assert.Equal(t, inv.ID, back.ID)
	assert.Equal(t, inv.PartyID, back.PartyID)
	assert.Len(t, back.LineItems, 1)
	assert.Equal(t, int64(1000), back.SubtotalCents)
}

func TestInvoiceToDomain_InvalidJSON(t *testing.T) {
	t.Parallel()

	entity := &InvoiceEntity{
		ID:        uuid.New(),
		LineItems: "not-valid-json",
	}

	_, err := invoiceToDomain(entity)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal line items")
}

func TestIsDuplicateKeyError(t *testing.T) {
	t.Parallel()

	assert.False(t, isDuplicateKeyError(nil))
	assert.False(t, isDuplicateKeyError(errors.New("some random error")))
	assert.True(t, isDuplicateKeyError(errors.New("duplicate key value violates unique constraint")))
	assert.True(t, isDuplicateKeyError(errors.New("SQLSTATE 23505: unique_violation")))
}

func TestBillingRunEntity_TableName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "billing_run", BillingRunEntity{}.TableName())
}

func TestInvoiceEntity_TableName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "invoice", InvoiceEntity{}.TableName())
}

func TestSagaExecutionEntity_TableName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "saga_executions", sagaExecutionEntity{}.TableName())
}

func TestEncodeDecode_Cursor(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Nanosecond)
	id := uuid.New()
	cursor := Cursor{CreatedAt: now, ID: id}

	encoded := EncodeCursor(cursor)
	assert.NotEmpty(t, encoded)

	decoded, err := DecodeCursor(encoded)
	require.NoError(t, err)
	assert.Equal(t, id, decoded.ID)
	assert.WithinDuration(t, now, decoded.CreatedAt, time.Microsecond)
}

func TestDecodeCursor_Empty(t *testing.T) {
	t.Parallel()

	cursor, err := DecodeCursor("")
	require.NoError(t, err)
	assert.Equal(t, Cursor{}, cursor)
}

func TestDecodeCursor_InvalidBase64(t *testing.T) {
	t.Parallel()

	_, err := DecodeCursor("not-valid-base64!!!")
	assert.ErrorIs(t, err, ErrInvalidCursor)
}

func TestDecodeCursor_MalformedContent(t *testing.T) {
	t.Parallel()

	// Valid base64 but no pipe separator
	encoded := "bm9waXBl" // "nopipe" in base64
	_, err := DecodeCursor(encoded)
	assert.ErrorIs(t, err, ErrInvalidCursor)
}

func TestDecodeCursor_InvalidTimestamp(t *testing.T) {
	t.Parallel()

	// base64 of "bad-time|valid-uuid"
	importB64 := "YmFkLXRpbWV8MDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAw"
	_, err := DecodeCursor(importB64)
	assert.ErrorIs(t, err, ErrInvalidCursor)
}

func TestDecodeCursor_InvalidUUID(t *testing.T) {
	t.Parallel()

	// base64 of "2023-01-01T00:00:00Z|not-a-uuid"
	importB64 := "MjAyMy0wMS0wMVQwMDowMDowMFp8bm90LWEtdXVpZA=="
	_, err := DecodeCursor(importB64)
	assert.ErrorIs(t, err, ErrInvalidCursor)
}

// =============================================================================
// Payment Order toEntity / toDomain mapper tests
// =============================================================================

func newTestMoney(t *testing.T, currency string, amountCents int64) domain.Money {
	t.Helper()
	m, err := domain.NewMoney(currency, amountCents)
	require.NoError(t, err)
	return m
}

func TestToEntity_WithNullableFields(t *testing.T) {
	t.Parallel()

	po := &domain.PaymentOrder{
		ID:                  uuid.New(),
		DebtorAccountID:     "debtor-1",
		CreditorReference:   "cred-ref",
		Amount:              newTestMoney(t, "GBP", 1000),
		Status:              domain.PaymentOrderStatusCompleted,
		LienID:              "lien-1",
		LienExecutionStatus: domain.LienExecutionStatusSucceeded,
		LienExecutionError:  "some error",
		BucketID:            "bucket-42",
		PaymentAttributes:   map[string]string{"key": "value"},
		IdempotencyKey:      "idem-key",
	}

	entity := toEntity(po)
	assert.Equal(t, po.ID, entity.ID)
	assert.Equal(t, "debtor-1", entity.DebtorAccountID)
	assert.Equal(t, int64(1000), entity.AmountCents)
	assert.Equal(t, "GBP", entity.Currency)
	require.NotNil(t, entity.LienExecutionStatus)
	assert.Equal(t, string(domain.LienExecutionStatusSucceeded), *entity.LienExecutionStatus)
	require.NotNil(t, entity.LienExecutionError)
	assert.Equal(t, "some error", *entity.LienExecutionError)
	require.NotNil(t, entity.BucketID)
	assert.Equal(t, "bucket-42", *entity.BucketID)
	require.NotNil(t, entity.PaymentAttributes)
	assert.Contains(t, *entity.PaymentAttributes, "key")
}

func TestToEntity_WithoutNullableFields(t *testing.T) {
	t.Parallel()

	po := &domain.PaymentOrder{
		ID:              uuid.New(),
		DebtorAccountID: "debtor-1",
		Amount:          newTestMoney(t, "GBP", 500),
		Status:          domain.PaymentOrderStatusInitiated,
	}

	entity := toEntity(po)
	assert.Nil(t, entity.LienExecutionStatus)
	assert.Nil(t, entity.LienExecutionError)
	assert.Nil(t, entity.BucketID)
	assert.Nil(t, entity.PaymentAttributes)
}

func TestToDomain_WithNullableFields(t *testing.T) {
	t.Parallel()

	lienStatus := string(domain.LienExecutionStatusFailed)
	lienErr := "connection timeout"
	bucketID := "bucket-99"
	attrs := `{"source":"api"}`

	entity := &PaymentOrderEntity{
		ID:                  uuid.New(),
		DebtorAccountID:     "debtor-1",
		CreditorReference:   "cred-ref",
		AmountCents:         1000,
		Currency:            "GBP",
		Status:              "COMPLETED",
		LienExecutionStatus: &lienStatus,
		LienExecutionError:  &lienErr,
		BucketID:            &bucketID,
		PaymentAttributes:   &attrs,
		IdempotencyKey:      "key-1",
	}

	po, err := toDomain(entity)
	require.NoError(t, err)
	assert.Equal(t, domain.LienExecutionStatusFailed, po.LienExecutionStatus)
	assert.Equal(t, "connection timeout", po.LienExecutionError)
	assert.Equal(t, "bucket-99", po.BucketID)
	assert.Equal(t, "api", po.PaymentAttributes["source"])
}

func TestToDomain_WithoutNullableFields(t *testing.T) {
	t.Parallel()

	entity := &PaymentOrderEntity{
		ID:              uuid.New(),
		DebtorAccountID: "debtor-1",
		AmountCents:     500,
		Currency:        "GBP",
		Status:          "INITIATED",
		IdempotencyKey:  "key-2",
	}

	po, err := toDomain(entity)
	require.NoError(t, err)
	assert.Empty(t, po.LienExecutionStatus)
	assert.Empty(t, po.LienExecutionError)
	assert.Empty(t, po.BucketID)
	assert.Nil(t, po.PaymentAttributes)
}

func TestToDomain_InvalidPaymentAttributes(t *testing.T) {
	t.Parallel()

	badJSON := "not-valid-json"
	entity := &PaymentOrderEntity{
		ID:                uuid.New(),
		AmountCents:       100,
		Currency:          "GBP",
		Status:            "INITIATED",
		PaymentAttributes: &badJSON,
	}

	_, err := toDomain(entity)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal payment attributes")
}

func TestToDomain_InvalidCurrency(t *testing.T) {
	t.Parallel()

	entity := &PaymentOrderEntity{
		ID:          uuid.New(),
		AmountCents: 100,
		Currency:    "INVALID",
		Status:      "INITIATED",
	}

	_, err := toDomain(entity)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create payment order amount")
}
