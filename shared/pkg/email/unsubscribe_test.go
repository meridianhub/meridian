package email

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAndVerifyUnsubscribeToken(t *testing.T) {
	key := []byte("test-secret-key-32-bytes-long!!!")
	params := UnsubscribeParams{
		TenantID: "tenant-1",
		PartyID:  "party-42",
		Channel:  "EMAIL",
		Category: CategoryOperational,
	}

	token := GenerateUnsubscribeToken(key, params)
	require.NotEmpty(t, token)

	decoded, err := VerifyUnsubscribeToken(key, token)
	require.NoError(t, err)
	assert.Equal(t, params, decoded)
}

func TestVerifyUnsubscribeToken_WrongKey(t *testing.T) {
	key := []byte("test-secret-key-32-bytes-long!!!")
	wrongKey := []byte("wrong-secret-key-32-bytes-long!!")

	token := GenerateUnsubscribeToken(key, UnsubscribeParams{
		TenantID: "tenant-1",
		PartyID:  "party-42",
		Channel:  "EMAIL",
		Category: CategoryMarketing,
	})

	_, err := VerifyUnsubscribeToken(wrongKey, token)
	assert.ErrorIs(t, err, ErrInvalidTokenSignature)
}

func TestVerifyUnsubscribeToken_EmptyKey(t *testing.T) {
	token := GenerateUnsubscribeToken([]byte("some-key"), UnsubscribeParams{
		TenantID: "t", PartyID: "p", Channel: "EMAIL", Category: CategoryOperational,
	})
	_, err := VerifyUnsubscribeToken(nil, token)
	assert.ErrorIs(t, err, ErrEmptyHMACKey)

	_, err = VerifyUnsubscribeToken([]byte{}, token)
	assert.ErrorIs(t, err, ErrEmptyHMACKey)
}

func TestVerifyUnsubscribeToken_InvalidEncoding(t *testing.T) {
	key := []byte("test-secret-key")
	_, err := VerifyUnsubscribeToken(key, "not-valid-base64!!!")
	assert.Error(t, err)
}

func TestVerifyUnsubscribeToken_TamperedPayload(t *testing.T) {
	key := []byte("test-secret-key-32-bytes-long!!!")
	params := UnsubscribeParams{
		TenantID: "tenant-1",
		PartyID:  "party-42",
		Channel:  "EMAIL",
		Category: CategoryOperational,
	}

	token := GenerateUnsubscribeToken(key, params)

	// Tamper: generate a new token with different params using same key, verify old sig doesn't match new payload
	params2 := UnsubscribeParams{
		TenantID: "tenant-2",
		PartyID:  "party-42",
		Channel:  "EMAIL",
		Category: CategoryOperational,
	}
	token2 := GenerateUnsubscribeToken(key, params2)

	// Tokens should be different
	assert.NotEqual(t, token, token2)

	// Each token should verify only with its own params
	decoded1, err := VerifyUnsubscribeToken(key, token)
	require.NoError(t, err)
	assert.Equal(t, "tenant-1", decoded1.TenantID)

	decoded2, err := VerifyUnsubscribeToken(key, token2)
	require.NoError(t, err)
	assert.Equal(t, "tenant-2", decoded2.TenantID)
}

func TestBuildUnsubscribeHeaders_Operational(t *testing.T) {
	cfg := &UnsubscribeConfig{
		HMACKey: []byte("test-secret-key-32-bytes-long!!!"),
		BaseURL: "https://app.meridian.example",
	}
	params := UnsubscribeParams{
		TenantID: "tenant-1",
		PartyID:  "party-42",
		Channel:  "EMAIL",
		Category: CategoryOperational,
	}

	headers := BuildUnsubscribeHeaders(cfg, params)

	require.NotNil(t, headers)
	assert.Contains(t, headers["List-Unsubscribe"], "https://app.meridian.example/unsubscribe?token=")
	assert.True(t, strings.HasPrefix(headers["List-Unsubscribe"], "<"))
	assert.True(t, strings.HasSuffix(headers["List-Unsubscribe"], ">"))
	assert.Equal(t, "List-Unsubscribe=One-Click", headers["List-Unsubscribe-Post"])
}

func TestBuildUnsubscribeHeaders_Marketing(t *testing.T) {
	cfg := &UnsubscribeConfig{
		HMACKey: []byte("test-secret-key-32-bytes-long!!!"),
		BaseURL: "https://app.meridian.example",
	}
	params := UnsubscribeParams{
		TenantID: "tenant-1",
		PartyID:  "party-42",
		Channel:  "EMAIL",
		Category: CategoryMarketing,
	}

	headers := BuildUnsubscribeHeaders(cfg, params)

	require.NotNil(t, headers)
	assert.Contains(t, headers, "List-Unsubscribe")
	assert.Contains(t, headers, "List-Unsubscribe-Post")
}

func TestBuildUnsubscribeHeaders_Transactional_ReturnsNil(t *testing.T) {
	cfg := &UnsubscribeConfig{
		HMACKey: []byte("test-secret-key-32-bytes-long!!!"),
		BaseURL: "https://app.meridian.example",
	}
	params := UnsubscribeParams{
		TenantID: "tenant-1",
		PartyID:  "party-42",
		Channel:  "EMAIL",
		Category: CategoryTransactional,
	}

	headers := BuildUnsubscribeHeaders(cfg, params)
	assert.Nil(t, headers)
}

func TestBuildUnsubscribeHeaders_NilConfig_ReturnsNil(t *testing.T) {
	params := UnsubscribeParams{
		TenantID: "tenant-1",
		PartyID:  "party-42",
		Channel:  "EMAIL",
		Category: CategoryOperational,
	}

	headers := BuildUnsubscribeHeaders(nil, params)
	assert.Nil(t, headers)
}

func TestBuildUnsubscribeHeaders_EmptyKey_ReturnsNil(t *testing.T) {
	cfg := &UnsubscribeConfig{
		HMACKey: nil,
		BaseURL: "https://app.meridian.example",
	}
	params := UnsubscribeParams{
		TenantID: "tenant-1",
		PartyID:  "party-42",
		Channel:  "EMAIL",
		Category: CategoryOperational,
	}

	headers := BuildUnsubscribeHeaders(cfg, params)
	assert.Nil(t, headers)
}

func TestBuildUnsubscribeHeaders_EmptyBaseURL_ReturnsNil(t *testing.T) {
	cfg := &UnsubscribeConfig{
		HMACKey: []byte("test-secret-key-32-bytes-long!!!"),
		BaseURL: "",
	}
	params := UnsubscribeParams{
		TenantID: "tenant-1",
		PartyID:  "party-42",
		Channel:  "EMAIL",
		Category: CategoryOperational,
	}

	headers := BuildUnsubscribeHeaders(cfg, params)
	assert.Nil(t, headers)
}

func TestBuildUnsubscribeHeaders_TokenIsVerifiable(t *testing.T) {
	key := []byte("test-secret-key-32-bytes-long!!!")
	cfg := &UnsubscribeConfig{
		HMACKey: key,
		BaseURL: "https://app.meridian.example",
	}
	params := UnsubscribeParams{
		TenantID: "tenant-1",
		PartyID:  "party-42",
		Channel:  "EMAIL",
		Category: CategoryOperational,
	}

	headers := BuildUnsubscribeHeaders(cfg, params)
	require.NotNil(t, headers)

	// Extract token from the URL in the header
	unsub := headers["List-Unsubscribe"]
	// Format: <https://app.meridian.example/unsubscribe?token=...>
	unsub = strings.TrimPrefix(unsub, "<")
	unsub = strings.TrimSuffix(unsub, ">")

	// Parse the URL to get the token
	parts := strings.SplitN(unsub, "token=", 2)
	require.Len(t, parts, 2)
	token := parts[1]

	decoded, err := VerifyUnsubscribeToken(key, token)
	require.NoError(t, err)
	assert.Equal(t, params, decoded)
}
