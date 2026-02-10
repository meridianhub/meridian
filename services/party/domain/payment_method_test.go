package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPaymentMethod_Valid(t *testing.T) {
	partyID := uuid.New()
	pm, err := NewPaymentMethod(
		partyID,
		PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_1234567890ab",
		PaymentMethodTypeCard,
		false,
		&PaymentMethodMetadata{Last4: "4242", Brand: "visa", ExpMonth: 12, ExpYear: 2027},
	)

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, pm.ID())
	assert.Equal(t, partyID, pm.PartyID())
	assert.Equal(t, PaymentProviderStripe, pm.Provider())
	assert.Equal(t, "cus_1234567890ab", pm.ProviderCustomerID())
	assert.Equal(t, "pm_1234567890ab", pm.ProviderMethodID())
	assert.Equal(t, PaymentMethodTypeCard, pm.MethodType())
	assert.False(t, pm.IsDefault())
	assert.Equal(t, PaymentMethodStatusActive, pm.Status())
	assert.Equal(t, int64(1), pm.Version())
	assert.NotNil(t, pm.Metadata())
	assert.Equal(t, "4242", pm.Metadata().Last4)
}

func TestNewPaymentMethod_DefaultTrue(t *testing.T) {
	pm, err := NewPaymentMethod(
		uuid.New(),
		PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_1234567890ab",
		PaymentMethodTypeCard,
		true,
		nil,
	)

	require.NoError(t, err)
	assert.True(t, pm.IsDefault())
}

func TestNewPaymentMethod_InvalidProvider(t *testing.T) {
	_, err := NewPaymentMethod(
		uuid.New(),
		PaymentProvider("PAYPAL"),
		"cus_1234567890ab",
		"pm_1234567890ab",
		PaymentMethodTypeCard,
		false,
		nil,
	)

	assert.ErrorIs(t, err, ErrInvalidProvider)
}

func TestNewPaymentMethod_InvalidProviderCustomerID(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"no prefix", "1234567890ab"},
		{"wrong prefix", "usr_1234567890ab"},
		{"too short", "cus_abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewPaymentMethod(
				uuid.New(),
				PaymentProviderStripe,
				tt.id,
				"pm_1234567890ab",
				PaymentMethodTypeCard,
				false,
				nil,
			)
			assert.ErrorIs(t, err, ErrInvalidProviderCustomer)
		})
	}
}

func TestNewPaymentMethod_InvalidProviderMethodID(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"no prefix", "1234567890ab"},
		{"wrong prefix", "card_1234567890ab"},
		{"too short", "pm_abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewPaymentMethod(
				uuid.New(),
				PaymentProviderStripe,
				"cus_1234567890ab",
				tt.id,
				PaymentMethodTypeCard,
				false,
				nil,
			)
			assert.ErrorIs(t, err, ErrInvalidProviderMethod)
		})
	}
}

func TestNewPaymentMethod_InvalidMethodType(t *testing.T) {
	_, err := NewPaymentMethod(
		uuid.New(),
		PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_1234567890ab",
		PaymentMethodType("CRYPTO"),
		false,
		nil,
	)

	assert.ErrorIs(t, err, ErrInvalidMethodType)
}

func TestNewPaymentMethod_AllMethodTypes(t *testing.T) {
	types := []PaymentMethodType{
		PaymentMethodTypeCard,
		PaymentMethodTypeBankAccount,
		PaymentMethodTypeSEPA,
	}

	for _, mt := range types {
		t.Run(string(mt), func(t *testing.T) {
			pm, err := NewPaymentMethod(
				uuid.New(),
				PaymentProviderStripe,
				"cus_1234567890ab",
				"pm_1234567890ab",
				mt,
				false,
				nil,
			)
			require.NoError(t, err)
			assert.Equal(t, mt, pm.MethodType())
		})
	}
}

func TestPaymentMethod_SetDefault(t *testing.T) {
	pm := createTestPaymentMethod(t)

	err := pm.SetDefault(true)
	require.NoError(t, err)
	assert.True(t, pm.IsDefault())
	assert.Equal(t, int64(2), pm.Version())

	err = pm.SetDefault(false)
	require.NoError(t, err)
	assert.False(t, pm.IsDefault())
	assert.Equal(t, int64(3), pm.Version())
}

func TestPaymentMethod_SetDefault_RemovedMethod(t *testing.T) {
	pm := createTestPaymentMethod(t)
	err := pm.Remove()
	require.NoError(t, err)

	err = pm.SetDefault(true)
	assert.ErrorIs(t, err, ErrPaymentMethodRemoved)
}

func TestPaymentMethod_SetDefault_ExpiredMethod(t *testing.T) {
	pm := createTestPaymentMethod(t)
	err := pm.Expire()
	require.NoError(t, err)

	err = pm.SetDefault(true)
	assert.ErrorIs(t, err, ErrPaymentMethodExpired)
}

func TestPaymentMethod_SetDefault_ExpiredMethod_UnsetAllowed(t *testing.T) {
	pm := createTestPaymentMethod(t)

	// Set as default first
	err := pm.SetDefault(true)
	require.NoError(t, err)

	// Expire it (auto-unsets default)
	err = pm.Expire()
	require.NoError(t, err)
	assert.False(t, pm.IsDefault())

	// Unsetting default on expired method is fine
	err = pm.SetDefault(false)
	require.NoError(t, err)
}

func TestPaymentMethod_Expire(t *testing.T) {
	pm := createTestPaymentMethod(t)

	// Set as default first
	err := pm.SetDefault(true)
	require.NoError(t, err)

	err = pm.Expire()
	require.NoError(t, err)
	assert.Equal(t, PaymentMethodStatusExpired, pm.Status())
	assert.False(t, pm.IsDefault(), "expiring should unset default")
}

func TestPaymentMethod_Expire_AlreadyRemoved(t *testing.T) {
	pm := createTestPaymentMethod(t)
	err := pm.Remove()
	require.NoError(t, err)

	err = pm.Expire()
	assert.ErrorIs(t, err, ErrPaymentMethodRemoved)
}

func TestPaymentMethod_Remove(t *testing.T) {
	pm := createTestPaymentMethod(t)

	// Set as default first
	err := pm.SetDefault(true)
	require.NoError(t, err)

	err = pm.Remove()
	require.NoError(t, err)
	assert.Equal(t, PaymentMethodStatusRemoved, pm.Status())
	assert.False(t, pm.IsDefault(), "removing should unset default")
}

func TestPaymentMethod_Remove_AlreadyRemoved(t *testing.T) {
	pm := createTestPaymentMethod(t)
	err := pm.Remove()
	require.NoError(t, err)

	err = pm.Remove()
	assert.ErrorIs(t, err, ErrPaymentMethodRemoved)
}

func TestPaymentMethod_Remove_FromExpired(t *testing.T) {
	pm := createTestPaymentMethod(t)
	err := pm.Expire()
	require.NoError(t, err)

	err = pm.Remove()
	require.NoError(t, err)
	assert.Equal(t, PaymentMethodStatusRemoved, pm.Status())
}

func TestPaymentMethod_VersionIncrements(t *testing.T) {
	pm := createTestPaymentMethod(t)
	assert.Equal(t, int64(1), pm.Version())

	_ = pm.SetDefault(true) // version 2
	assert.Equal(t, int64(2), pm.Version())

	_ = pm.Expire() // version 3
	assert.Equal(t, int64(3), pm.Version())
}

func TestPaymentProviderIsValid(t *testing.T) {
	assert.True(t, PaymentProviderStripe.IsValid())
	assert.False(t, PaymentProvider("PAYPAL").IsValid())
	assert.False(t, PaymentProvider("").IsValid())
}

func TestPaymentMethodTypeIsValid(t *testing.T) {
	assert.True(t, PaymentMethodTypeCard.IsValid())
	assert.True(t, PaymentMethodTypeBankAccount.IsValid())
	assert.True(t, PaymentMethodTypeSEPA.IsValid())
	assert.False(t, PaymentMethodType("CRYPTO").IsValid())
	assert.False(t, PaymentMethodType("").IsValid())
}

func TestPaymentMethodStatusIsValid(t *testing.T) {
	assert.True(t, PaymentMethodStatusActive.IsValid())
	assert.True(t, PaymentMethodStatusExpired.IsValid())
	assert.True(t, PaymentMethodStatusRemoved.IsValid())
	assert.False(t, PaymentMethodStatus("PENDING").IsValid())
	assert.False(t, PaymentMethodStatus("").IsValid())
}

func TestReconstructPaymentMethod(t *testing.T) {
	id := uuid.New()
	partyID := uuid.New()
	meta := &PaymentMethodMetadata{Last4: "1234", Brand: "mastercard"}

	pm := ReconstructPaymentMethod(
		id, partyID, PaymentProviderStripe,
		"cus_1234567890ab", "pm_1234567890ab",
		PaymentMethodTypeCard, true, meta,
		PaymentMethodStatusActive,
		pmTestTime, pmTestTime, 5,
	)

	assert.Equal(t, id, pm.ID())
	assert.Equal(t, partyID, pm.PartyID())
	assert.True(t, pm.IsDefault())
	assert.Equal(t, int64(5), pm.Version())
	assert.Equal(t, "1234", pm.Metadata().Last4)
}

var pmTestTime = func() time.Time {
	t, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	return t
}()

// createTestPaymentMethod is a helper to create a valid payment method for tests
func createTestPaymentMethod(t *testing.T) *PaymentMethod {
	t.Helper()
	pm, err := NewPaymentMethod(
		uuid.New(),
		PaymentProviderStripe,
		"cus_1234567890ab",
		"pm_1234567890ab",
		PaymentMethodTypeCard,
		false,
		nil,
	)
	require.NoError(t, err)
	return pm
}
