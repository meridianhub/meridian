package auth

import (
	"errors"
	"testing"

	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCompositeJWTValidator(t *testing.T) {
	v1 := &mockValidator{claims: &platformauth.Claims{UserID: "user-1"}}
	v2 := &mockValidator{claims: &platformauth.Claims{UserID: "user-2"}}

	composite := NewCompositeJWTValidator(v1, v2)

	assert.NotNil(t, composite)
	assert.Len(t, composite.validators, 2)
}

func TestCompositeJWTValidator_EmptyValidators_ReturnsError(t *testing.T) {
	composite := NewCompositeJWTValidator()

	claims, err := composite.ValidateToken("any-token")

	assert.Nil(t, claims)
	assert.ErrorIs(t, err, platformauth.ErrInvalidToken)
}

func TestCompositeJWTValidator_FirstValidatorSucceeds(t *testing.T) {
	expectedClaims := &platformauth.Claims{
		UserID:   "user-1",
		TenantID: "acme_corp",
	}
	v1 := &mockValidator{claims: expectedClaims}
	v2 := &mockValidator{claims: &platformauth.Claims{UserID: "user-2"}}

	composite := NewCompositeJWTValidator(v1, v2)

	claims, err := composite.ValidateToken("some-token")

	require.NoError(t, err)
	assert.Equal(t, expectedClaims, claims)
}

func TestCompositeJWTValidator_FirstFailsSecondSucceeds(t *testing.T) {
	expectedClaims := &platformauth.Claims{
		UserID:   "user-2",
		TenantID: "beta_corp",
	}
	v1 := &mockValidator{err: platformauth.ErrInvalidToken}
	v2 := &mockValidator{claims: expectedClaims}

	composite := NewCompositeJWTValidator(v1, v2)

	claims, err := composite.ValidateToken("some-token")

	require.NoError(t, err)
	assert.Equal(t, expectedClaims, claims)
}

func TestCompositeJWTValidator_AllFail_ReturnsLastError(t *testing.T) {
	firstErr := errors.New("first validator error")
	lastErr := errors.New("last validator error")
	v1 := &mockValidator{err: firstErr}
	v2 := &mockValidator{err: lastErr}

	composite := NewCompositeJWTValidator(v1, v2)

	claims, err := composite.ValidateToken("invalid-token")

	assert.Nil(t, claims)
	assert.ErrorIs(t, err, lastErr)
}

func TestCompositeJWTValidator_SingleValidator_Succeeds(t *testing.T) {
	expectedClaims := &platformauth.Claims{UserID: "solo-user"}
	v := &mockValidator{claims: expectedClaims}

	composite := NewCompositeJWTValidator(v)

	claims, err := composite.ValidateToken("token")

	require.NoError(t, err)
	assert.Equal(t, expectedClaims, claims)
}

func TestCompositeJWTValidator_SingleValidator_Fails(t *testing.T) {
	v := &mockValidator{err: platformauth.ErrInvalidToken}

	composite := NewCompositeJWTValidator(v)

	claims, err := composite.ValidateToken("expired-token")

	assert.Nil(t, claims)
	assert.ErrorIs(t, err, platformauth.ErrInvalidToken)
}

func TestCompositeJWTValidator_ThreeValidators_ThirdSucceeds(t *testing.T) {
	expectedClaims := &platformauth.Claims{UserID: "third-user"}
	errFirst := errors.New("first fails")
	errSecond := errors.New("second fails")
	v1 := &mockValidator{err: errFirst}
	v2 := &mockValidator{err: errSecond}
	v3 := &mockValidator{claims: expectedClaims}

	composite := NewCompositeJWTValidator(v1, v2, v3)

	claims, err := composite.ValidateToken("token")

	require.NoError(t, err)
	assert.Equal(t, expectedClaims, claims)
}
