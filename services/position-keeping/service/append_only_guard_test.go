package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestPosition() *domain.Position {
	return &domain.Position{
		ID:             uuid.New(),
		AccountID:      "ACC-001",
		InstrumentCode: "GBP",
		BucketKey:      "default",
		Amount:         decimal.NewFromFloat(100.00),
		Dimension:      "Monetary",
		Attributes:     map[string]string{"key": "value"},
		ReferenceID:    uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		CreatedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CreatedBy:      "system",
	}
}

func copyPosition(p *domain.Position) *domain.Position {
	attrs := make(map[string]string, len(p.Attributes))
	for k, v := range p.Attributes {
		attrs[k] = v
	}
	return &domain.Position{
		ID:             p.ID,
		AccountID:      p.AccountID,
		InstrumentCode: p.InstrumentCode,
		BucketKey:      p.BucketKey,
		Amount:         p.Amount,
		Dimension:      p.Dimension,
		Attributes:     attrs,
		ReferenceID:    p.ReferenceID,
		CreatedAt:      p.CreatedAt,
		CreatedBy:      p.CreatedBy,
	}
}

func TestValidateImmutableFieldsUnchanged_AllFieldsSame(t *testing.T) {
	existing := newTestPosition()
	proposed := copyPosition(existing)

	err := validateImmutableFieldsUnchanged(existing, proposed)
	require.NoError(t, err)
}

func TestValidateImmutableFieldsUnchanged_AttributesChanged(t *testing.T) {
	existing := newTestPosition()
	proposed := copyPosition(existing)
	proposed.Attributes = map[string]string{"note": "allowed change"}

	err := validateImmutableFieldsUnchanged(existing, proposed)
	require.NoError(t, err, "attributes is a mutable field and should be allowed to change")
}

func TestValidateImmutableFieldsUnchanged_AmountChanged(t *testing.T) {
	existing := newTestPosition()
	proposed := copyPosition(existing)
	proposed.Amount = decimal.NewFromFloat(200.00)

	err := validateImmutableFieldsUnchanged(existing, proposed)
	assert.ErrorIs(t, err, domain.ErrPositionUpdateForbidden)
}

func TestValidateImmutableFieldsUnchanged_AccountIDChanged(t *testing.T) {
	existing := newTestPosition()
	proposed := copyPosition(existing)
	proposed.AccountID = "ACC-002"

	err := validateImmutableFieldsUnchanged(existing, proposed)
	assert.ErrorIs(t, err, domain.ErrPositionUpdateForbidden)
}

func TestValidateImmutableFieldsUnchanged_InstrumentCodeChanged(t *testing.T) {
	existing := newTestPosition()
	proposed := copyPosition(existing)
	proposed.InstrumentCode = "USD"

	err := validateImmutableFieldsUnchanged(existing, proposed)
	assert.ErrorIs(t, err, domain.ErrPositionUpdateForbidden)
}

func TestValidateImmutableFieldsUnchanged_BucketKeyChanged(t *testing.T) {
	existing := newTestPosition()
	proposed := copyPosition(existing)
	proposed.BucketKey = "other-bucket"

	err := validateImmutableFieldsUnchanged(existing, proposed)
	assert.ErrorIs(t, err, domain.ErrPositionUpdateForbidden)
}

func TestValidateImmutableFieldsUnchanged_ReferenceIDChanged(t *testing.T) {
	existing := newTestPosition()
	proposed := copyPosition(existing)
	proposed.ReferenceID = uuid.New()

	err := validateImmutableFieldsUnchanged(existing, proposed)
	assert.ErrorIs(t, err, domain.ErrPositionUpdateForbidden)
}

func TestValidateImmutableFieldsUnchanged_DimensionChanged(t *testing.T) {
	existing := newTestPosition()
	proposed := copyPosition(existing)
	proposed.Dimension = "Energy"

	err := validateImmutableFieldsUnchanged(existing, proposed)
	assert.ErrorIs(t, err, domain.ErrPositionUpdateForbidden)
}

func TestValidateImmutableFieldsUnchanged_CreatedAtChanged(t *testing.T) {
	existing := newTestPosition()
	proposed := copyPosition(existing)
	proposed.CreatedAt = time.Now()

	err := validateImmutableFieldsUnchanged(existing, proposed)
	assert.ErrorIs(t, err, domain.ErrPositionUpdateForbidden)
}

func TestValidateImmutableFieldsUnchanged_CreatedByChanged(t *testing.T) {
	existing := newTestPosition()
	proposed := copyPosition(existing)
	proposed.CreatedBy = "different-user"

	err := validateImmutableFieldsUnchanged(existing, proposed)
	assert.ErrorIs(t, err, domain.ErrPositionUpdateForbidden)
}
