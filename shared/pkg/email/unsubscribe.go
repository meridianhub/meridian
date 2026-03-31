package email

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Sentinel errors for unsubscribe token operations.
var (
	ErrInvalidTokenFormat    = errors.New("email: invalid unsubscribe token format")
	ErrInvalidTokenSignature = errors.New("email: invalid unsubscribe token signature")
	ErrEmptyHMACKey          = errors.New("email: unsubscribe HMAC key is empty")
)

// UnsubscribeConfig holds configuration for generating RFC 2369/8058 unsubscribe headers.
type UnsubscribeConfig struct {
	// HMACKey is the secret key used to sign unsubscribe tokens.
	HMACKey []byte
	// BaseURL is the base URL for unsubscribe links (e.g., "https://app.meridian.example").
	BaseURL string
}

// UnsubscribeParams identifies the subscription to unsubscribe from.
type UnsubscribeParams struct {
	TenantID string
	PartyID  string
	Channel  string
	Category string
}

// GenerateUnsubscribeToken creates an HMAC-signed token encoding the unsubscribe parameters.
// The token format is: base64url(tenantID|partyID|channel|category|hmac_signature)
// The "|" delimiter is safe because all fields are controlled values (UUIDs, enum constants),
// not free-text user input.
func GenerateUnsubscribeToken(key []byte, params UnsubscribeParams) string {
	payload := strings.Join([]string{params.TenantID, params.PartyID, params.Channel, params.Category}, "|")
	sig := computeHMAC(key, payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + sig))
}

// VerifyUnsubscribeToken verifies and decodes an unsubscribe token.
// Returns the decoded parameters if the signature is valid.
func VerifyUnsubscribeToken(key []byte, token string) (UnsubscribeParams, error) {
	if len(key) == 0 {
		return UnsubscribeParams{}, ErrEmptyHMACKey
	}

	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return UnsubscribeParams{}, fmt.Errorf("email: invalid unsubscribe token encoding: %w", err)
	}

	parts := strings.Split(string(decoded), "|")
	if len(parts) != 5 {
		return UnsubscribeParams{}, ErrInvalidTokenFormat
	}

	payload := strings.Join(parts[:4], "|")
	expectedSig := computeHMAC(key, payload)
	if !hmac.Equal([]byte(parts[4]), []byte(expectedSig)) {
		return UnsubscribeParams{}, ErrInvalidTokenSignature
	}

	return UnsubscribeParams{
		TenantID: parts[0],
		PartyID:  parts[1],
		Channel:  parts[2],
		Category: parts[3],
	}, nil
}

func computeHMAC(key []byte, message string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// BuildUnsubscribeURL returns the unsubscribe URL for OPERATIONAL and MARKETING
// emails. Returns empty string for TRANSACTIONAL emails, unknown categories, or
// if config is not set.
func BuildUnsubscribeURL(cfg *UnsubscribeConfig, params UnsubscribeParams) string {
	if cfg == nil || len(cfg.HMACKey) == 0 || cfg.BaseURL == "" {
		return ""
	}
	switch params.Category {
	case CategoryOperational, CategoryMarketing:
		// These categories get unsubscribe URLs
	default:
		return ""
	}

	token := GenerateUnsubscribeToken(cfg.HMACKey, params)
	return fmt.Sprintf("%s/unsubscribe?token=%s", cfg.BaseURL, url.QueryEscape(token))
}

// BuildUnsubscribeHeaders returns RFC 2369 List-Unsubscribe and RFC 8058
// List-Unsubscribe-Post headers for OPERATIONAL and MARKETING emails only.
// Returns nil for TRANSACTIONAL emails, unknown categories, or if config is not set.
func BuildUnsubscribeHeaders(cfg *UnsubscribeConfig, params UnsubscribeParams) map[string]string {
	unsubURL := BuildUnsubscribeURL(cfg, params)
	if unsubURL == "" {
		return nil
	}

	return map[string]string{
		"List-Unsubscribe":      fmt.Sprintf("<%s>", unsubURL),
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}
