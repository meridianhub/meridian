package valuationfeature

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// mockAccountResolver implements AccountResolver for testing.
type mockAccountResolver struct {
	accountUUID      uuid.UUID
	nativeInstrument string
	err              error
}

func (m *mockAccountResolver) ResolveAccount(_ context.Context, _ string) (uuid.UUID, string, error) {
	return m.accountUUID, m.nativeInstrument, m.err
}

func TestCreate_output_instrument_mismatch(t *testing.T) {
	resolver := &mockAccountResolver{
		accountUUID:      uuid.New(),
		nativeInstrument: "GBP",
	}

	_, err := Create(context.Background(), nil, resolver, CreateParams{
		AccountID:         "acc-123",
		InstrumentCode:    "USD",
		ValuationMethodID: uuid.New().String(),
		OutputInstrument:  "EUR", // mismatch with GBP
		CreatedBy:         "test",
	})

	assert.ErrorIs(t, err, ErrMethodOutputMismatch)
}

func TestCreate_invalid_method_id(t *testing.T) {
	resolver := &mockAccountResolver{
		accountUUID:      uuid.New(),
		nativeInstrument: "GBP",
	}

	_, err := Create(context.Background(), nil, resolver, CreateParams{
		AccountID:         "acc-123",
		InstrumentCode:    "USD",
		ValuationMethodID: "not-a-uuid",
		OutputInstrument:  "GBP",
		CreatedBy:         "test",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid valuation_method_id")
}

func TestCreate_invalid_parameters_json(t *testing.T) {
	resolver := &mockAccountResolver{
		accountUUID:      uuid.New(),
		nativeInstrument: "GBP",
	}

	_, err := Create(context.Background(), nil, resolver, CreateParams{
		AccountID:         "acc-123",
		InstrumentCode:    "USD",
		ValuationMethodID: uuid.New().String(),
		OutputInstrument:  "GBP",
		Parameters:        "{invalid json",
		CreatedBy:         "test",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid parameters JSON")
}

func TestCreate_resolver_error(t *testing.T) {
	resolver := &mockAccountResolver{
		err: errors.New("account not found"),
	}

	_, err := Create(context.Background(), nil, resolver, CreateParams{
		AccountID: "acc-123",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolve account")
}

func TestCreate_empty_instrument_code(t *testing.T) {
	resolver := &mockAccountResolver{
		accountUUID:      uuid.New(),
		nativeInstrument: "GBP",
	}

	_, err := Create(context.Background(), nil, resolver, CreateParams{
		AccountID:         "acc-123",
		InstrumentCode:    "", // empty
		ValuationMethodID: uuid.New().String(),
		OutputInstrument:  "GBP",
		CreatedBy:         "test",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create valuation feature")
}

func TestUpdate_invalid_feature_id(t *testing.T) {
	_, err := Update(context.Background(), nil, UpdateParams{
		FeatureID: "not-a-uuid",
		Action:    ActionActivate,
		UpdatedBy: "test",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid feature_id")
}

func TestGetByID_invalid_id(t *testing.T) {
	_, err := GetByID(context.Background(), nil, "not-a-uuid")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid feature_id")
}

func TestGetByAccountAndInstrument_resolver_error(t *testing.T) {
	resolver := &mockAccountResolver{
		err: errors.New("account not found"),
	}

	_, err := GetByAccountAndInstrument(context.Background(), nil, resolver, "acc-123", "USD", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolve account")
}

func TestList_resolver_error(t *testing.T) {
	resolver := &mockAccountResolver{
		err: errors.New("account not found"),
	}

	_, err := List(context.Background(), nil, resolver, ListParams{
		AccountID: "acc-123",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolve account")
}

func TestUpdateAction_constants(t *testing.T) {
	assert.Equal(t, UpdateAction(1), ActionActivate)
	assert.Equal(t, UpdateAction(2), ActionTerminate)
}

func TestErrInvalidAction(t *testing.T) {
	assert.NotNil(t, ErrInvalidAction)
	assert.Equal(t, "invalid valuation feature action", ErrInvalidAction.Error())
}

func TestErrMethodOutputMismatch(t *testing.T) {
	assert.NotNil(t, ErrMethodOutputMismatch)
}
