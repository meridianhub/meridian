package accounttype_test

import (
	"encoding/json"
	"testing"

	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCELTestDefinition creates a definition with the provided CEL fields.
func newCELTestDefinition(code, validationCEL, eligibilityCEL string) *accounttype.Definition {
	return &accounttype.Definition{
		Code:            code,
		Version:         1,
		DisplayName:     "CEL Test " + code,
		NormalBalance:   accounttype.NormalBalanceCredit,
		BehaviorClass:   accounttype.BehaviorClassCustomer,
		InstrumentCode:  "GBP",
		AttributeSchema: json.RawMessage(`{}`),
		Attributes:      map[string]any{},
		ValidationCEL:   validationCEL,
		EligibilityCEL:  eligibilityCEL,
	}
}

// ---------------------------------------------------------------------------
// ValidateTransaction
// ---------------------------------------------------------------------------

func TestValidateTransaction(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "cel-validate-tx")

	t.Run("no CEL expression always returns valid", func(t *testing.T) {
		def := newTestDefinition("NO_CEL_VALIDATE", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))

		result, err := reg.ValidateTransaction(ctx, "NO_CEL_VALIDATE", 1, accounttype.AttributeBag{})
		require.NoError(t, err)
		assert.True(t, result.Valid)
		assert.Empty(t, result.Errors)
	})

	t.Run("expression passes with positive amount", func(t *testing.T) {
		def := newCELTestDefinition("CEL_VALID_AMOUNT", `parse_decimal(amount) > 0.0`, "")
		require.NoError(t, reg.CreateDraft(ctx, def))

		result, err := reg.ValidateTransaction(ctx, "CEL_VALID_AMOUNT", 1, accounttype.AttributeBag{
			Amount: "100.00",
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)
		assert.Empty(t, result.Errors)
	})

	t.Run("expression fails with zero amount", func(t *testing.T) {
		def := newCELTestDefinition("CEL_ZERO_AMOUNT", `parse_decimal(amount) > 0.0`, "")
		require.NoError(t, reg.CreateDraft(ctx, def))

		result, err := reg.ValidateTransaction(ctx, "CEL_ZERO_AMOUNT", 1, accounttype.AttributeBag{
			Amount: "0.00",
		})
		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)
	})

	t.Run("definition not found returns ErrNotFound", func(t *testing.T) {
		_, err := reg.ValidateTransaction(ctx, "NONEXISTENT_VALIDATE", 99, accounttype.AttributeBag{})
		require.ErrorIs(t, err, accounttype.ErrNotFound)
	})

	t.Run("second call uses cached program", func(t *testing.T) {
		def := newCELTestDefinition("CEL_CACHE_TEST", `parse_decimal(amount) > 0.0`, "")
		require.NoError(t, reg.CreateDraft(ctx, def))

		result1, err := reg.ValidateTransaction(ctx, "CEL_CACHE_TEST", 1, accounttype.AttributeBag{Amount: "50.00"})
		require.NoError(t, err)

		result2, err := reg.ValidateTransaction(ctx, "CEL_CACHE_TEST", 1, accounttype.AttributeBag{Amount: "50.00"})
		require.NoError(t, err)

		assert.Equal(t, result1.Valid, result2.Valid)
	})
}

// ---------------------------------------------------------------------------
// CheckEligibility
// ---------------------------------------------------------------------------

func TestCheckEligibility(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "cel-check-elig")

	t.Run("no CEL expression always returns eligible", func(t *testing.T) {
		def := newTestDefinition("NO_CEL_ELIG", "GBP")
		require.NoError(t, reg.CreateDraft(ctx, def))

		result, err := reg.CheckEligibility(ctx, "NO_CEL_ELIG", 1, accounttype.AttributeBag{})
		require.NoError(t, err)
		assert.True(t, result.Valid)
		assert.Empty(t, result.Errors)
	})

	t.Run("expression passes when attribute matches", func(t *testing.T) {
		def := newCELTestDefinition("CEL_ELIG_MATCH", "", `party.status == 'ACTIVE'`)
		require.NoError(t, reg.CreateDraft(ctx, def))

		result, err := reg.CheckEligibility(ctx, "CEL_ELIG_MATCH", 1, accounttype.AttributeBag{
			Attributes: map[string]string{"status": "ACTIVE"},
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)
	})

	t.Run("expression fails when attribute mismatches", func(t *testing.T) {
		def := newCELTestDefinition("CEL_ELIG_MISMATCH", "", `party.status == 'ACTIVE'`)
		require.NoError(t, reg.CreateDraft(ctx, def))

		result, err := reg.CheckEligibility(ctx, "CEL_ELIG_MISMATCH", 1, accounttype.AttributeBag{
			Attributes: map[string]string{"status": "SUSPENDED"},
		})
		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)
	})

	t.Run("nil attributes defaults to empty map", func(t *testing.T) {
		def := newCELTestDefinition("CEL_NIL_ATTRS", "", `true`)
		require.NoError(t, reg.CreateDraft(ctx, def))

		result, err := reg.CheckEligibility(ctx, "CEL_NIL_ATTRS", 1, accounttype.AttributeBag{
			Attributes: nil,
		})
		require.NoError(t, err)
		assert.True(t, result.Valid)
	})

	t.Run("definition not found returns ErrNotFound", func(t *testing.T) {
		_, err := reg.CheckEligibility(ctx, "NONEXISTENT_ELIG", 1, accounttype.AttributeBag{})
		require.ErrorIs(t, err, accounttype.ErrNotFound)
	})
}

// ---------------------------------------------------------------------------
// GetProductFeatures
// ---------------------------------------------------------------------------

func TestGetProductFeatures(t *testing.T) {
	reg, pool := setupTestAccountTypeRegistry(t)
	ctx := setupAccountTypeTenantContext(t, pool, "cel-product-features")

	t.Run("returns stored attributes", func(t *testing.T) {
		def := &accounttype.Definition{
			Code:            "FEAT_TEST",
			Version:         1,
			DisplayName:     "Feature Test",
			NormalBalance:   accounttype.NormalBalanceCredit,
			BehaviorClass:   accounttype.BehaviorClassCustomer,
			InstrumentCode:  "GBP",
			AttributeSchema: json.RawMessage(`{}`),
			Attributes:      map[string]any{"tier": "gold", "limit": float64(5000)},
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		features, err := reg.GetProductFeatures(ctx, "FEAT_TEST", 1)
		require.NoError(t, err)
		assert.Equal(t, "gold", features["tier"])
		assert.Equal(t, float64(5000), features["limit"])
	})

	t.Run("nil attributes returns empty map", func(t *testing.T) {
		def := &accounttype.Definition{
			Code:            "FEAT_NIL",
			Version:         1,
			DisplayName:     "Feature Nil Test",
			NormalBalance:   accounttype.NormalBalanceCredit,
			BehaviorClass:   accounttype.BehaviorClassCustomer,
			InstrumentCode:  "GBP",
			AttributeSchema: json.RawMessage(`{}`),
			Attributes:      nil,
		}
		require.NoError(t, reg.CreateDraft(ctx, def))

		features, err := reg.GetProductFeatures(ctx, "FEAT_NIL", 1)
		require.NoError(t, err)
		assert.NotNil(t, features)
		assert.Empty(t, features)
	})

	t.Run("definition not found returns ErrNotFound", func(t *testing.T) {
		_, err := reg.GetProductFeatures(ctx, "NONEXISTENT_FEAT", 1)
		require.ErrorIs(t, err, accounttype.ErrNotFound)
	})
}
