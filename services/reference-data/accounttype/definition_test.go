package accounttype_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validParams() accounttype.NewDefinitionParams {
	return accounttype.NewDefinitionParams{
		Code:              "CUSTOMER_CURRENT",
		DisplayName:       "Customer Current Account",
		Description:       "Standard customer current account",
		NormalBalance:     "CREDIT",
		BehaviorClass:     "CUSTOMER",
		InstrumentCode:    "GBP",
		DefaultSagaPrefix: "payment",
		AttributeSchema:   json.RawMessage(`{}`),
		Attributes:        map[string]any{},
	}
}

func TestNewDefinition_NormalizesLowercaseToUppercase(t *testing.T) {
	params := validParams()
	params.Code = "customer_current"
	params.NormalBalance = "credit"
	params.BehaviorClass = "customer"
	params.InstrumentCode = "gbp"

	def, err := accounttype.NewDefinition(params)
	require.NoError(t, err)

	assert.Equal(t, "CUSTOMER_CURRENT", def.Code)
	assert.Equal(t, accounttype.NormalBalanceCredit, def.NormalBalance)
	assert.Equal(t, accounttype.BehaviorClassCustomer, def.BehaviorClass)
	assert.Equal(t, "GBP", def.InstrumentCode)
}

func TestNewDefinition_InitialStatusIsDraft(t *testing.T) {
	def, err := accounttype.NewDefinition(validParams())
	require.NoError(t, err)

	assert.Equal(t, accounttype.StatusDraft, def.Status)
}

func TestNewDefinition_AssignsNewUUID(t *testing.T) {
	def, err := accounttype.NewDefinition(validParams())
	require.NoError(t, err)

	assert.NotEqual(t, uuid.Nil, def.ID)
}

func TestNewDefinition_RejectsInvalidBehaviorClass(t *testing.T) {
	params := validParams()
	params.BehaviorClass = "INVALID_CLASS"

	_, err := accounttype.NewDefinition(params)
	require.ErrorIs(t, err, accounttype.ErrInvalidBehaviorClass)
}

func TestNewDefinition_RejectsInvalidNormalBalance(t *testing.T) {
	params := validParams()
	params.NormalBalance = "NEITHER"

	_, err := accounttype.NewDefinition(params)
	require.ErrorIs(t, err, accounttype.ErrInvalidNormalBalance)
}

func TestNewDefinition_AcceptsAllValidBehaviorClasses(t *testing.T) {
	classes := []string{
		"CUSTOMER", "CLEARING", "NOSTRO", "VOSTRO",
		"HOLDING", "SUSPENSE", "REVENUE", "EXPENSE", "INVENTORY",
	}
	for _, class := range classes {
		params := validParams()
		params.BehaviorClass = class
		_, err := accounttype.NewDefinition(params)
		assert.NoError(t, err, "expected %s to be valid", class)
	}
}

func TestNewDefinition_PairConstraint_BothNilOK(t *testing.T) {
	params := validParams()
	params.DefaultConversionMethodID = nil
	params.DefaultConversionMethodVersion = nil

	_, err := accounttype.NewDefinition(params)
	require.NoError(t, err)
}

func TestNewDefinition_PairConstraint_BothSetOK(t *testing.T) {
	params := validParams()
	id := uuid.New()
	v := 1
	params.DefaultConversionMethodID = &id
	params.DefaultConversionMethodVersion = &v

	_, err := accounttype.NewDefinition(params)
	require.NoError(t, err)
}

func TestNewDefinition_PairConstraint_IDWithoutVersion(t *testing.T) {
	params := validParams()
	id := uuid.New()
	params.DefaultConversionMethodID = &id
	params.DefaultConversionMethodVersion = nil

	_, err := accounttype.NewDefinition(params)
	require.Error(t, err)
}

func TestNewDefinition_PairConstraint_VersionWithoutID(t *testing.T) {
	params := validParams()
	v := 1
	params.DefaultConversionMethodID = nil
	params.DefaultConversionMethodVersion = &v

	_, err := accounttype.NewDefinition(params)
	require.Error(t, err)
}

func TestStatusIsValid_KnownValues(t *testing.T) {
	assert.True(t, accounttype.StatusDraft.IsValid())
	assert.True(t, accounttype.StatusActive.IsValid())
	assert.True(t, accounttype.StatusDeprecated.IsValid())
}

func TestStatusIsValid_UnknownValue(t *testing.T) {
	assert.False(t, accounttype.Status("UNKNOWN").IsValid())
	assert.False(t, accounttype.Status("").IsValid())
}

func TestStatusCanTransitionTo_DraftToActive(t *testing.T) {
	assert.True(t, accounttype.StatusDraft.CanTransitionTo(accounttype.StatusActive))
}

func TestStatusCanTransitionTo_ActiveToDeprecated(t *testing.T) {
	assert.True(t, accounttype.StatusActive.CanTransitionTo(accounttype.StatusDeprecated))
}

func TestStatusCanTransitionTo_DraftToDeprecatedForbidden(t *testing.T) {
	assert.False(t, accounttype.StatusDraft.CanTransitionTo(accounttype.StatusDeprecated))
}

func TestStatusCanTransitionTo_ActiveToDraftForbidden(t *testing.T) {
	assert.False(t, accounttype.StatusActive.CanTransitionTo(accounttype.StatusDraft))
}

func TestStatusCanTransitionTo_DeprecatedAllowsReactivation(t *testing.T) {
	assert.False(t, accounttype.StatusDeprecated.CanTransitionTo(accounttype.StatusDraft))
	assert.True(t, accounttype.StatusDeprecated.CanTransitionTo(accounttype.StatusActive))
}

func TestStatusCanTransitionTo_SameStatusForbidden(t *testing.T) {
	assert.False(t, accounttype.StatusDraft.CanTransitionTo(accounttype.StatusDraft))
	assert.False(t, accounttype.StatusActive.CanTransitionTo(accounttype.StatusActive))
}

func TestBehaviorClassIsValid_KnownValues(t *testing.T) {
	known := []accounttype.BehaviorClass{
		accounttype.BehaviorClassCustomer,
		accounttype.BehaviorClassClearing,
		accounttype.BehaviorClassNostro,
		accounttype.BehaviorClassVostro,
		accounttype.BehaviorClassHolding,
		accounttype.BehaviorClassSuspense,
		accounttype.BehaviorClassRevenue,
		accounttype.BehaviorClassExpense,
		accounttype.BehaviorClassInventory,
	}
	for _, b := range known {
		assert.True(t, b.IsValid(), "expected %s to be valid", b)
	}
}

func TestBehaviorClassIsValid_UnknownValue(t *testing.T) {
	assert.False(t, accounttype.BehaviorClass("UNKNOWN").IsValid())
}

func TestNormalBalanceIsValid_KnownValues(t *testing.T) {
	assert.True(t, accounttype.NormalBalanceDebit.IsValid())
	assert.True(t, accounttype.NormalBalanceCredit.IsValid())
}

func TestNormalBalanceIsValid_UnknownValue(t *testing.T) {
	assert.False(t, accounttype.NormalBalance("BOTH").IsValid())
	assert.False(t, accounttype.NormalBalance("").IsValid())
}

func newCompiler(t *testing.T) accounttype.DefinitionCompiler {
	t.Helper()
	c, err := refcel.NewCompiler()
	require.NoError(t, err)
	return c
}

func TestNewDefinition_CEL_ValidExpressions(t *testing.T) {
	c := newCompiler(t)
	params := validParams()
	params.Compiler = c
	params.ValidationCEL = `parse_decimal(amount) > 0.0`
	params.BucketingCEL = `bucket_key([attributes["type"]])`
	params.EligibilityCEL = `party.status == 'ACTIVE'`

	def, err := accounttype.NewDefinition(params)
	require.NoError(t, err)
	assert.Equal(t, params.ValidationCEL, def.ValidationCEL)
	assert.Equal(t, params.BucketingCEL, def.BucketingCEL)
	assert.Equal(t, params.EligibilityCEL, def.EligibilityCEL)
}

func TestNewDefinition_CEL_InvalidValidationCEL(t *testing.T) {
	c := newCompiler(t)
	params := validParams()
	params.Compiler = c
	params.ValidationCEL = `undefined_var > 0`

	_, err := accounttype.NewDefinition(params)
	require.Error(t, err)
	assert.True(t, errors.Is(err, accounttype.ErrInvalidCEL))
}

func TestNewDefinition_CEL_InvalidBucketingCEL(t *testing.T) {
	c := newCompiler(t)
	params := validParams()
	params.Compiler = c
	params.BucketingCEL = `amount` // amount is not in bucket key env

	_, err := accounttype.NewDefinition(params)
	require.Error(t, err)
	assert.True(t, errors.Is(err, accounttype.ErrInvalidCEL))
}

func TestNewDefinition_CEL_InvalidEligibilityCEL(t *testing.T) {
	c := newCompiler(t)
	params := validParams()
	params.Compiler = c
	params.EligibilityCEL = `amount > 0` // amount is not in eligibility env

	_, err := accounttype.NewDefinition(params)
	require.Error(t, err)
	assert.True(t, errors.Is(err, accounttype.ErrInvalidCEL))
}

func TestNewDefinition_CEL_NilCompilerSkipsValidation(t *testing.T) {
	params := validParams()
	params.Compiler = nil
	params.EligibilityCEL = `this is not valid CEL !!!`

	// Without a compiler, CEL is not validated
	_, err := accounttype.NewDefinition(params)
	require.NoError(t, err)
}
