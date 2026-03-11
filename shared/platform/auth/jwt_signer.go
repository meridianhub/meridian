// Package auth provides JWT authentication, signing, and JWKS key management.
package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	// ErrPrivateKeyNil is returned when a nil private key is provided.
	ErrPrivateKeyNil = errors.New("private key cannot be nil")
	// ErrInvalidPEM is returned when the PEM block cannot be decoded.
	ErrInvalidPEM = errors.New("failed to decode PEM block")
	// ErrNotRSAKey is returned when the PEM does not contain an RSA private key.
	ErrNotRSAKey = errors.New("PEM does not contain an RSA private key")
)

// JWTSigner signs JWT tokens using an RSA private key.
type JWTSigner struct {
	privateKey *rsa.PrivateKey
	keyID      string
	issuer     string
}

// JWTSignerConfig holds configuration for creating a JWTSigner.
type JWTSignerConfig struct {
	// PrivateKeyPEM is the RSA private key in PEM format.
	// If empty, a new 2048-bit key is generated (suitable for dev/test).
	PrivateKeyPEM string
	// KeyID is the "kid" header value for signed tokens. Defaults to "meridian-1".
	KeyID string
	// Issuer is the "iss" claim. Defaults to "meridian".
	Issuer string
}

// NewJWTSigner creates a signer from configuration.
// If PrivateKeyPEM is empty, a development key is auto-generated.
func NewJWTSigner(cfg JWTSignerConfig) (*JWTSigner, error) {
	var privateKey *rsa.PrivateKey

	if cfg.PrivateKeyPEM != "" {
		var err error
		privateKey, err = parseRSAPrivateKey(cfg.PrivateKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("jwt signer: %w", err)
		}
	} else {
		var err error
		privateKey, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, fmt.Errorf("jwt signer: generate key: %w", err)
		}
	}

	keyID := cfg.KeyID
	if keyID == "" {
		keyID = "meridian-1"
	}

	issuer := cfg.Issuer
	if issuer == "" {
		issuer = "meridian"
	}

	return &JWTSigner{
		privateKey: privateKey,
		keyID:      keyID,
		issuer:     issuer,
	}, nil
}

// SignClaims signs custom claims into a JWT token string.
// The claims map is merged with standard registered claims (iss, iat, exp, sub).
// The "sub" key in customClaims sets the subject; all other keys become custom claims.
func (s *JWTSigner) SignClaims(customClaims map[string]interface{}, ttl time.Duration) (string, error) {
	now := time.Now()

	// Build MapClaims with registered claims
	mapClaims := jwt.MapClaims{
		"iss": s.issuer,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}

	// Merge custom claims
	for k, v := range customClaims {
		mapClaims[k] = v
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, mapClaims)
	token.Header["kid"] = s.keyID

	signed, err := token.SignedString(s.privateKey)
	if err != nil {
		return "", fmt.Errorf("jwt signer: sign token: %w", err)
	}

	return signed, nil
}

// PublicKey returns the RSA public key for verification.
func (s *JWTSigner) PublicKey() *rsa.PublicKey {
	return &s.privateKey.PublicKey
}

// KeyID returns the key ID used in token headers.
func (s *JWTSigner) KeyID() string {
	return s.keyID
}

// Issuer returns the issuer claim value.
func (s *JWTSigner) Issuer() string {
	return s.issuer
}

// JWKS returns the JSON Web Key Set containing the public key.
func (s *JWTSigner) JWKS() JWKS {
	pub := s.privateKey.PublicKey
	return JWKS{
		Keys: []JWK{
			{
				Kid: s.keyID,
				Kty: "RSA",
				Use: "sig",
				Alg: "RS256",
				N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			},
		},
	}
}

// ServeJWKS returns an http.HandlerFunc that serves the JWKS endpoint.
func (s *JWTSigner) ServeJWKS() http.HandlerFunc {
	jwks := s.JWKS()
	body, _ := json.Marshal(jwks)

	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(body)
	}
}

// parseRSAPrivateKey decodes a PEM-encoded RSA private key.
// Supports both PKCS#1 and PKCS#8 formats.
func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, ErrInvalidPEM
	}

	// Try PKCS#8 first (more common in modern tools)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, ErrNotRSAKey
		}
		return rsaKey, nil
	}

	// Fall back to PKCS#1
	rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotRSAKey, err)
	}

	return rsaKey, nil
}
