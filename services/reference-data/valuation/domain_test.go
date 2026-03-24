package valuation_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reference-data/valuation"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// LifecycleStatus constants
// ---------------------------------------------------------------------------

func TestLifecycleStatus_Constants(t *testing.T) {
	assert.Equal(t, valuation.LifecycleStatus("INITIATED"), valuation.StatusInitiated)
	assert.Equal(t, valuation.LifecycleStatus("ACTIVE"), valuation.StatusActive)
	assert.Equal(t, valuation.LifecycleStatus("DEPRECATED"), valuation.StatusDeprecated)
}

func TestLifecycleStatus_StringRepresentation(t *testing.T) {
	assert.Equal(t, "INITIATED", string(valuation.StatusInitiated))
	assert.Equal(t, "ACTIVE", string(valuation.StatusActive))
	assert.Equal(t, "DEPRECATED", string(valuation.StatusDeprecated))
}

// ---------------------------------------------------------------------------
// Domain error sentinels
// ---------------------------------------------------------------------------

func TestValuationErrors_AreSentinels(t *testing.T) {
	errs := []error{
		valuation.ErrNotFound,
		valuation.ErrAlreadyExists,
		valuation.ErrSystemReadOnly,
		valuation.ErrNotInitiated,
		valuation.ErrNotActive,
		valuation.ErrInvalidCEL,
		valuation.ErrRequiredPolicyMissing,
	}
	for _, e := range errs {
		assert.NotNil(t, e, "error sentinel should not be nil")
		assert.True(t, errors.Is(e, e), "sentinel should match itself via errors.Is")
	}
}

func TestValuationErrors_AreDistinct(t *testing.T) {
	assert.NotEqual(t, valuation.ErrNotFound, valuation.ErrAlreadyExists)
	assert.NotEqual(t, valuation.ErrSystemReadOnly, valuation.ErrNotInitiated)
	assert.NotEqual(t, valuation.ErrNotActive, valuation.ErrInvalidCEL)
	assert.NotEqual(t, valuation.ErrRequiredPolicyMissing, valuation.ErrNotFound)
}

// ---------------------------------------------------------------------------
// Method struct
// ---------------------------------------------------------------------------

func TestMethod_CanBeConstructedWithRequiredFields(t *testing.T) {
	now := time.Now().UTC()
	m := &valuation.Method{
		ID:               uuid.New(),
		Name:             "gbp_to_usd",
		Version:          1,
		InputInstrument:  "GBP",
		OutputInstrument: "USD",
		LogicScript:      "def convert(ctx): return ctx.amount * 1.25",
		LifecycleStatus:  valuation.StatusInitiated,
		CreatedAt:        now,
		ValidFrom:        now,
	}

	assert.NotEqual(t, uuid.Nil, m.ID)
	assert.Equal(t, "gbp_to_usd", m.Name)
	assert.Equal(t, 1, m.Version)
	assert.Equal(t, "GBP", m.InputInstrument)
	assert.Equal(t, "USD", m.OutputInstrument)
	assert.Equal(t, valuation.StatusInitiated, m.LifecycleStatus)
	assert.False(t, m.IsSystem)
	assert.Nil(t, m.ActivatedAt)
	assert.Nil(t, m.DeprecatedAt)
	assert.Nil(t, m.ValidTo)
}

func TestMethod_RequiredPoliciesDefaultNil(t *testing.T) {
	m := &valuation.Method{}
	assert.Nil(t, m.RequiredPolicies)
}

func TestMethod_SystemFlagDefaultsFalse(t *testing.T) {
	m := &valuation.Method{
		ID: uuid.New(),
	}
	assert.False(t, m.IsSystem)
}

// ---------------------------------------------------------------------------
// Policy struct
// ---------------------------------------------------------------------------

func TestPolicy_CanBeConstructedWithRequiredFields(t *testing.T) {
	now := time.Now().UTC()
	p := &valuation.Policy{
		ID:              uuid.New(),
		Name:            "exchange_rate_policy",
		Version:         1,
		CelExpression:   `inputs.rate > 0.0`,
		OutputType:      "double",
		LifecycleStatus: valuation.StatusActive,
		CreatedAt:       now,
		ValidFrom:       now,
	}

	assert.NotEqual(t, uuid.Nil, p.ID)
	assert.Equal(t, "exchange_rate_policy", p.Name)
	assert.Equal(t, 1, p.Version)
	assert.Equal(t, "double", p.OutputType)
	assert.Equal(t, valuation.StatusActive, p.LifecycleStatus)
	assert.Nil(t, p.ActivatedAt)
	assert.Nil(t, p.DeprecatedAt)
}

// ---------------------------------------------------------------------------
// DryRunResult struct
// ---------------------------------------------------------------------------

func TestDryRunResult_SuccessCase(t *testing.T) {
	r := valuation.DryRunResult{
		Success:       true,
		Output:        "1.25",
		EstimatedCost: 5,
		Errors:        nil,
	}
	assert.True(t, r.Success)
	assert.Equal(t, "1.25", r.Output)
	assert.Equal(t, 5, r.EstimatedCost)
	assert.Empty(t, r.Errors)
}

func TestDryRunResult_FailureCase(t *testing.T) {
	r := valuation.DryRunResult{
		Success: false,
		Errors:  []string{"compilation error: undefined variable"},
	}
	assert.False(t, r.Success)
	assert.Len(t, r.Errors, 1)
	assert.Contains(t, r.Errors[0], "compilation error")
}
