package persistence

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToLienEntity_BasicFields(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)
	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           5000,
		InstrumentCode:        "GBP",
		BucketID:              "bucket-001",
		Status:                domain.LienStatusActive,
		PaymentOrderReference: "PO-001",
		TerminationReason:     "",
		ExpiresAt:             &expiresAt,
		CreatedAt:             now,
		UpdatedAt:             now,
		Version:               1,
	}

	entity, err := toLienEntity(lien)

	require.NoError(t, err)
	assert.Equal(t, lien.ID, entity.ID)
	assert.Equal(t, lien.AccountID, entity.AccountID)
	assert.Equal(t, int64(5000), entity.AmountCents)
	assert.Equal(t, "GBP", entity.InstrumentCode)
	assert.Equal(t, "bucket-001", entity.BucketID)
	assert.Equal(t, "ACTIVE", entity.Status)
	assert.Equal(t, "PO-001", entity.PaymentOrderReference)
	assert.Equal(t, "", entity.TerminationReason)
	assert.NotNil(t, entity.ExpiresAt)
	assert.Nil(t, entity.ReservedQuantity)
	assert.Nil(t, entity.ValuedAmount)
	assert.Nil(t, entity.ValuationAnalysis)
}

func TestToLienEntity_WithReservedQuantityAndValuedAmount(t *testing.T) {
	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10000,
		InstrumentCode:        "GBP",
		BucketID:              "bucket-002",
		Status:                domain.LienStatusActive,
		PaymentOrderReference: "PO-002",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
		ReservedQuantity: &domain.InstrumentAmount{
			Amount:         decimal.NewFromInt(100),
			InstrumentCode: "kWh",
		},
		ValuedAmount: &domain.InstrumentAmount{
			Amount:         decimal.NewFromInt(10000),
			InstrumentCode: "GBP",
		},
		ValuationAnalysis: json.RawMessage(`{"rate":"0.10","source":"grid"}`),
	}

	entity, err := toLienEntity(lien)

	require.NoError(t, err)
	assert.NotNil(t, entity.ReservedQuantity)
	assert.NotNil(t, entity.ValuedAmount)
	assert.NotNil(t, entity.ValuationAnalysis)
}

func TestToLienDomain_BasicFields(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)
	entity := &LienEntity{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           7500,
		InstrumentCode:        "USD",
		BucketID:              "bucket-003",
		Status:                "TERMINATED",
		PaymentOrderReference: "PO-003",
		TerminationReason:     "Order cancelled",
		ExpiresAt:             &expiresAt,
		CreatedAt:             now,
		UpdatedAt:             now,
		Version:               2,
	}

	lien, err := toLienDomain(entity)

	require.NoError(t, err)
	assert.Equal(t, entity.ID, lien.ID)
	assert.Equal(t, entity.AccountID, lien.AccountID)
	assert.Equal(t, int64(7500), lien.AmountCents)
	assert.Equal(t, "USD", lien.InstrumentCode)
	assert.Equal(t, domain.LienStatusTerminated, lien.Status)
	assert.Equal(t, "Order cancelled", lien.TerminationReason)
	assert.Nil(t, lien.ReservedQuantity)
	assert.Nil(t, lien.ValuedAmount)
	assert.Nil(t, lien.ValuationAnalysis)
}

func TestToLienDomain_WithJSONBFields(t *testing.T) {
	rqJSON, _ := json.Marshal(&domain.InstrumentAmount{
		Amount:         decimal.NewFromInt(50),
		InstrumentCode: "kWh",
	})
	vaJSON, _ := json.Marshal(&domain.InstrumentAmount{
		Amount:         decimal.NewFromInt(5000),
		InstrumentCode: "GBP",
	})
	analysisJSON := json.RawMessage(`{"rate":"0.10"}`)

	entity := &LienEntity{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           5000,
		InstrumentCode:        "GBP",
		BucketID:              "bucket-004",
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-004",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
		ReservedQuantity:      JSONBMap(rqJSON),
		ValuedAmount:          JSONBMap(vaJSON),
		ValuationAnalysis:     JSONBMap(analysisJSON),
	}

	lien, err := toLienDomain(entity)

	require.NoError(t, err)
	require.NotNil(t, lien.ReservedQuantity)
	assert.Equal(t, "kWh", lien.ReservedQuantity.InstrumentCode)
	assert.True(t, decimal.NewFromInt(50).Equal(lien.ReservedQuantity.Amount))

	require.NotNil(t, lien.ValuedAmount)
	assert.Equal(t, "GBP", lien.ValuedAmount.InstrumentCode)

	require.NotNil(t, lien.ValuationAnalysis)
}

func TestToLienDomain_InvalidReservedQuantityJSON(t *testing.T) {
	entity := &LienEntity{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           1000,
		InstrumentCode:        "GBP",
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-005",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
		ReservedQuantity:      JSONBMap(`{invalid json`),
	}

	_, err := toLienDomain(entity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved_quantity")
}

func TestToLienDomain_InvalidValuedAmountJSON(t *testing.T) {
	entity := &LienEntity{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           1000,
		InstrumentCode:        "GBP",
		Status:                "ACTIVE",
		PaymentOrderReference: "PO-006",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
		ValuedAmount:          JSONBMap(`{invalid json`),
	}

	_, err := toLienDomain(entity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "valued_amount")
}

func TestToLienEntity_RoundTrip(t *testing.T) {
	original := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           9999,
		InstrumentCode:        "EUR",
		BucketID:              "rt-bucket",
		Status:                domain.LienStatusExecuted,
		PaymentOrderReference: "PO-RT-001",
		TerminationReason:     "",
		CreatedAt:             time.Now().Truncate(time.Microsecond),
		UpdatedAt:             time.Now().Truncate(time.Microsecond),
		Version:               3,
		ReservedQuantity: &domain.InstrumentAmount{
			Amount:         decimal.NewFromFloat(42.5),
			InstrumentCode: "kWh",
		},
		ValuedAmount: &domain.InstrumentAmount{
			Amount:         decimal.NewFromInt(9999),
			InstrumentCode: "EUR",
		},
	}

	entity, err := toLienEntity(original)
	require.NoError(t, err)

	roundTripped, err := toLienDomain(entity)
	require.NoError(t, err)

	assert.Equal(t, original.ID, roundTripped.ID)
	assert.Equal(t, original.AccountID, roundTripped.AccountID)
	assert.Equal(t, original.AmountCents, roundTripped.AmountCents)
	assert.Equal(t, original.InstrumentCode, roundTripped.InstrumentCode)
	assert.Equal(t, original.Status, roundTripped.Status)
	assert.True(t, original.ReservedQuantity.Amount.Equal(roundTripped.ReservedQuantity.Amount))
	assert.Equal(t, original.ReservedQuantity.InstrumentCode, roundTripped.ReservedQuantity.InstrumentCode)
	assert.True(t, original.ValuedAmount.Amount.Equal(roundTripped.ValuedAmount.Amount))
}
