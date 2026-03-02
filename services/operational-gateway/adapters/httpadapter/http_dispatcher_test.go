package httpadapter_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/operational-gateway/adapters/httpadapter"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

// --- Test doubles ---

// stubSecretStore resolves secrets from an in-memory map.
type stubSecretStore struct {
	secrets map[string]string
	err     error
}

func (s *stubSecretStore) Resolve(_ context.Context, _, ref string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	v, ok := s.secrets[ref]
	if !ok {
		return "", fmt.Errorf("%w: %s", ports.ErrSecretNotFound, ref)
	}
	return v, nil
}

// stubTransformer is a minimal PayloadTransformer that returns fixed outbound body
// and derives inbound outcome from the HTTP status code.
type stubTransformer struct {
	outBody    []byte
	outHeaders map[string]string
	outErr     error
	inErr      error
}

func (t *stubTransformer) TransformOutbound(_ context.Context, _ *domain.Instruction, _ *ports.InstructionRoute) ([]byte, map[string]string, error) {
	if t.outErr != nil {
		return nil, nil, t.outErr
	}
	body := t.outBody
	if body == nil {
		body = []byte(`{"stub":true}`)
	}
	return body, t.outHeaders, nil
}

func (t *stubTransformer) TransformInbound(_ context.Context, statusCode int, body []byte, _ *ports.InstructionRoute) (*ports.InstructionOutcome, error) {
	if t.inErr != nil {
		return nil, t.inErr
	}
	if statusCode >= 200 && statusCode < 300 {
		return &ports.InstructionOutcome{ProviderStatus: "ACCEPTED"}, nil
	}
	return &ports.InstructionOutcome{
		ProviderStatus: "REJECTED",
		FailureReason:  fmt.Sprintf("provider returned HTTP %d: %s", statusCode, body),
		ShouldRetry:    statusCode == 429 || statusCode >= 500,
	}, nil
}

// --- Helpers ---

func newInstruction(t *testing.T) *domain.Instruction {
	t.Helper()
	inst, err := domain.NewInstruction(
		uuid.New(),
		"payment.create",
		"conn-001",
		map[string]any{"amount": "100.00"},
	)
	require.NoError(t, err)
	return inst
}

func newConn(t *testing.T, baseURL string, authConfig domain.AuthConfig) *domain.ProviderConnection {
	t.Helper()
	conn, err := domain.NewProviderConnection(
		"tenant-001",
		"acme-bank",
		"bank",
		domain.ProtocolHTTPS,
		baseURL,
		authConfig,
		domain.RetryPolicy{},
		domain.RateLimitConfig{},
	)
	require.NoError(t, err)
	return conn
}

func simpleRoute(method string) *ports.InstructionRoute {
	return &ports.InstructionRoute{
		InstructionType: "payment.create",
		HTTPMethod:      method,
		PathTemplate:    "/payments",
	}
}

// expectedHMACSHA256 computes the expected HMAC-SHA256 for test assertions.
func expectedHMACSHA256(key, body []byte) string {
	h := hmac.New(sha256.New, key)
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// expectedHMACSHA512 computes the expected HMAC-SHA512 for test assertions.
func expectedHMACSHA512(key, body []byte) string {
	h := hmac.New(sha512.New, key)
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// --- Constructor tests ---

func TestNewHTTPDispatcher_PanicsOnNilSecretStore(t *testing.T) {
	tr := &stubTransformer{}
	assert.Panics(t, func() {
		httpadapter.NewHTTPDispatcher(nil, tr, nil)
	})
}

func TestNewHTTPDispatcher_PanicsOnNilTransformer(t *testing.T) {
	ss := &stubSecretStore{secrets: map[string]string{}}
	assert.Panics(t, func() {
		httpadapter.NewHTTPDispatcher(ss, nil, nil)
	})
}

func TestNewHTTPDispatcher_AcceptsNilLogger(t *testing.T) {
	ss := &stubSecretStore{secrets: map[string]string{}}
	tr := &stubTransformer{}
	assert.NotPanics(t, func() {
		httpadapter.NewHTTPDispatcher(ss, tr, nil)
	})
}

// --- Interface compliance ---

func TestHTTPDispatcher_ImplementsDispatcher(_ *testing.T) {
	ss := &stubSecretStore{secrets: map[string]string{}}
	tr := &stubTransformer{}
	var _ ports.Dispatcher = httpadapter.NewHTTPDispatcher(ss, tr, nil)
}

// --- APIKeyAuth ---

func TestDispatch_APIKeyAuth_SetsHeaderCorrectly(t *testing.T) {
	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"MY_API_KEY": "secret-key-123"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "MY_API_KEY",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.NoError(t, result.Error)
	assert.Equal(t, http.StatusOK, result.StatusCode)
	assert.Equal(t, "secret-key-123", gotKey)
	assert.Equal(t, "ACCEPTED", result.Outcome.ProviderStatus)
	assert.Positive(t, result.Duration)
}

func TestDispatch_APIKeyAuth_SecretNotFound_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{}} // no secrets
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "MISSING_KEY",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, ports.ErrSecretNotFound)
	assert.Zero(t, result.StatusCode)
}

// --- BasicAuth ---

func TestDispatch_BasicAuth_SetsAuthorizationHeader(t *testing.T) {
	var gotUsername, gotPassword string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUsername, gotPassword, _ = r.BasicAuth()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"DB_PASSWORD": "p4ssw0rd"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.BasicAuth{
		Username:    "admin",
		PasswordRef: "DB_PASSWORD",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.NoError(t, result.Error)
	assert.Equal(t, "admin", gotUsername)
	assert.Equal(t, "p4ssw0rd", gotPassword)
	assert.Equal(t, http.StatusCreated, result.StatusCode)
}

func TestDispatch_BasicAuth_PasswordNotFound_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.BasicAuth{
		Username:    "user",
		PasswordRef: "MISSING_PASSWORD",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, ports.ErrSecretNotFound)
}

// --- OAuth2Auth ---

func TestDispatch_OAuth2Auth_FetchesTokenAndSetsBearerHeader(t *testing.T) {
	// Token endpoint returns a valid access_token.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		require.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "client_credentials", r.FormValue("grant_type"))
		assert.Equal(t, "my-client-id", r.FormValue("client_id"))
		assert.Equal(t, "super-secret", r.FormValue("client_secret"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"tok_abc123","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	var gotBearer string
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBearer = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"pmt-001"}`))
	}))
	defer apiServer.Close()

	ss := &stubSecretStore{secrets: map[string]string{"OAUTH_SECRET": "super-secret"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, apiServer.URL, &domain.OAuth2Auth{
		TokenURL:        tokenServer.URL + "/oauth/token",
		ClientID:        "my-client-id",
		ClientSecretRef: "OAUTH_SECRET",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.NoError(t, result.Error)
	assert.Equal(t, "Bearer tok_abc123", gotBearer)
	assert.Equal(t, http.StatusOK, result.StatusCode)
}

func TestDispatch_OAuth2Auth_TokenEndpointFails_ReturnsError(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer tokenServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer apiServer.Close()

	ss := &stubSecretStore{secrets: map[string]string{"OAUTH_SECRET": "wrong-secret"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, apiServer.URL, &domain.OAuth2Auth{
		TokenURL:        tokenServer.URL + "/oauth/token",
		ClientID:        "client-id",
		ClientSecretRef: "OAUTH_SECRET",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, httpadapter.ErrTokenEndpointFailed)
}

func TestDispatch_OAuth2Auth_ClientSecretNotFound_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.OAuth2Auth{
		TokenURL:        server.URL + "/token",
		ClientID:        "client-id",
		ClientSecretRef: "MISSING_SECRET",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, ports.ErrSecretNotFound)
}

// --- HMACAuth ---

func TestDispatch_HMACAuth_SHA256_SetsSignatureHeader(t *testing.T) {
	var gotSig string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Signature")
		var readErr error
		gotBody, readErr = io.ReadAll(r.Body)
		require.NoError(t, readErr)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	requestBody := []byte(`{"stub":true}`)
	ss := &stubSecretStore{secrets: map[string]string{"HMAC_SECRET": "hmac-signing-key"}}
	tr := &stubTransformer{outBody: requestBody}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.HMACAuth{
		SecretRef:       "HMAC_SECRET",
		Algorithm:       "sha256",
		SignatureHeader: "X-Signature",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.NoError(t, result.Error)
	assert.Equal(t, requestBody, gotBody, "request body should be forwarded as-is")

	// Verify the signature matches expected HMAC-SHA256.
	expected := expectedHMACSHA256([]byte("hmac-signing-key"), requestBody)
	assert.Equal(t, expected, gotSig, "HMAC-SHA256 signature should match")
}

func TestDispatch_HMACAuth_SHA512_ComputesCorrectSignature(t *testing.T) {
	var gotSig string
	requestBody := []byte(`{"amount":"500"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-HMAC-Sig")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"HMAC_KEY": "some-key"}}
	tr := &stubTransformer{outBody: requestBody}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.HMACAuth{
		SecretRef:       "HMAC_KEY",
		Algorithm:       "sha512",
		SignatureHeader: "X-HMAC-Sig",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.NoError(t, result.Error)
	expected := expectedHMACSHA512([]byte("some-key"), requestBody)
	assert.Equal(t, expected, gotSig, "HMAC-SHA512 signature should match")
	// sha512 hex is 128 characters.
	assert.Len(t, gotSig, 128, "HMAC-SHA512 should produce 128 hex chars")
}

func TestDispatch_HMACAuth_SecretNotFound_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.HMACAuth{
		SecretRef:       "MISSING_HMAC",
		Algorithm:       "sha256",
		SignatureHeader: "X-Sig",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, ports.ErrSecretNotFound)
}

// --- MTLSAuth ---

func TestDispatch_MTLSAuth_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.MTLSAuth{
		ClientCertRef: "CERT_REF",
		ClientKeyRef:  "KEY_REF",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, httpadapter.ErrMTLSNotSupported)
}

// --- Header handling ---

func TestDispatch_SetsStandardHeaders(t *testing.T) {
	var gotContentType, gotRequestID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotRequestID = r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"KEY": "val"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "KEY",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.NoError(t, result.Error)
	assert.Equal(t, "application/json", gotContentType)
	assert.NotEmpty(t, gotRequestID, "X-Request-ID should be set")
	// Verify it's a valid UUID format.
	_, uuidErr := uuid.Parse(gotRequestID)
	assert.NoError(t, uuidErr, "X-Request-ID should be a valid UUID")
}

func TestDispatch_MergesTransformHeadersAndRouteHeaders(t *testing.T) {
	var gotVersion, gotAccept, gotCustom string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotVersion = r.Header.Get("X-Provider-Version")
		gotAccept = r.Header.Get("Accept")
		gotCustom = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"KEY": "val"}}
	tr := &stubTransformer{
		outHeaders: map[string]string{"X-Provider-Version": "2024-01"},
	}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "KEY",
	})
	route := &ports.InstructionRoute{
		InstructionType: "payment.create",
		HTTPMethod:      "POST",
		PathTemplate:    "/payments",
		Headers: map[string]string{
			"Accept":   "application/json",
			"X-Custom": "from-route",
		},
	}
	result := d.Dispatch(context.Background(), newInstruction(t), conn, route)

	require.NoError(t, result.Error)
	assert.Equal(t, "2024-01", gotVersion)
	assert.Equal(t, "application/json", gotAccept)
	assert.Equal(t, "from-route", gotCustom)
}

// --- Response handling ---

func TestDispatch_Provider500_ReturnsResultWithRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"KEY": "val"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "KEY",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.NoError(t, result.Error)
	assert.Equal(t, http.StatusInternalServerError, result.StatusCode)
	require.NotNil(t, result.Outcome)
	assert.Equal(t, "REJECTED", result.Outcome.ProviderStatus)
	assert.True(t, result.Outcome.ShouldRetry, "5xx should be retryable")
}

func TestDispatch_Provider422_ReturnsResultNoRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"invalid payload"}`))
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"KEY": "val"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "KEY",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.NoError(t, result.Error)
	assert.Equal(t, http.StatusUnprocessableEntity, result.StatusCode)
	require.NotNil(t, result.Outcome)
	assert.False(t, result.Outcome.ShouldRetry, "422 should not be retryable")
}

// --- Large response body handling ---

func TestDispatch_LargeResponseBody_TruncatesAt1MiB(t *testing.T) {
	// Serve a response larger than 1 MiB.
	const overLimit = (1 << 20) + 1024 // 1 MiB + 1 KiB
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, overLimit))
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"KEY": "val"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "KEY",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("GET"))

	// Body is truncated to at most 1 MiB.
	assert.LessOrEqual(t, len(result.ResponseBody), 1<<20)
}

// --- Timeout handling ---

func TestDispatch_ContextDeadlineExceeded_ReturnsError(t *testing.T) {
	// Server delays response beyond the context deadline.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"KEY": "val"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "KEY",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result := d.Dispatch(ctx, newInstruction(t), conn, simpleRoute("POST"))

	require.Error(t, result.Error)
	assert.Positive(t, result.Duration)
}

// --- Outbound transform failure ---

func TestDispatch_OutboundTransformFails_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{}}
	tr := &stubTransformer{outErr: ports.ErrTransformFailed}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "KEY",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, ports.ErrTransformFailed)
	assert.Zero(t, result.StatusCode)
}

// --- Inbound transform failure ---

func TestDispatch_InboundTransformFails_ReturnsPartialResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"pmt-001"}`))
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"KEY": "val"}}
	tr := &stubTransformer{inErr: ports.ErrTransformFailed}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "KEY",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, ports.ErrTransformFailed)
	assert.Equal(t, http.StatusOK, result.StatusCode)
	assert.Equal(t, []byte(`{"id":"pmt-001"}`), result.ResponseBody)
	assert.Nil(t, result.Outcome)
}

// --- Network error ---

func TestDispatch_NetworkError_ReturnsError(t *testing.T) {
	// Use a port that nothing is listening on.
	ss := &stubSecretStore{secrets: map[string]string{"KEY": "val"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, "http://127.0.0.1:19999", &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "KEY",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := d.Dispatch(ctx, newInstruction(t), conn, simpleRoute("POST"))

	require.Error(t, result.Error)
	assert.Zero(t, result.StatusCode)
}

// --- URL building ---

func TestDispatch_PathTemplate_AppendedToBaseURL(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"KEY": "val"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL+"/api/v1", &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "KEY",
	})
	route := &ports.InstructionRoute{
		HTTPMethod:   "POST",
		PathTemplate: "/payments/create",
	}
	result := d.Dispatch(context.Background(), newInstruction(t), conn, route)

	require.NoError(t, result.Error)
	assert.Equal(t, "/api/v1/payments/create", gotPath)
}

// --- Duration tracking ---

func TestDispatch_DurationIsPositive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"KEY": "val"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, server.URL, &domain.APIKeyAuth{
		HeaderName: "X-API-Key",
		SecretRef:  "KEY",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("GET"))

	require.NoError(t, result.Error)
	assert.Positive(t, result.Duration)
}

// --- OAuth2 with scopes ---

func TestDispatch_OAuth2Auth_WithScopes_SendsScopeParam(t *testing.T) {
	var gotScope string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotScope = r.FormValue("scope")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"tok_scoped","token_type":"Bearer"}`))
	}))
	defer tokenServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer apiServer.Close()

	ss := &stubSecretStore{secrets: map[string]string{"SECRET": "s"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, apiServer.URL, &domain.OAuth2Auth{
		TokenURL:        tokenServer.URL + "/token",
		ClientID:        "cid",
		ClientSecretRef: "SECRET",
		Scopes:          []string{"payments:write", "accounts:read"},
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.NoError(t, result.Error)
	assert.Equal(t, "payments:write accounts:read", gotScope)
}

// --- OAuth2 token response edge cases ---

func TestDispatch_OAuth2Auth_MalformedTokenResponse_ReturnsError(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Missing access_token field entirely.
		_, _ = w.Write([]byte(`{"token_type":"Bearer"}`))
	}))
	defer tokenServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer apiServer.Close()

	ss := &stubSecretStore{secrets: map[string]string{"SECRET": "s"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	conn := newConn(t, apiServer.URL, &domain.OAuth2Auth{
		TokenURL:        tokenServer.URL + "/token",
		ClientID:        "cid",
		ClientSecretRef: "SECRET",
	})
	result := d.Dispatch(context.Background(), newInstruction(t), conn, simpleRoute("POST"))

	require.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, httpadapter.ErrTokenNotFound)
}

// --- Concurrent dispatch benchmark ---

func BenchmarkDispatch_Concurrent(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	ss := &stubSecretStore{secrets: map[string]string{"KEY": "bench-key"}}
	tr := &stubTransformer{}
	d := httpadapter.NewHTTPDispatcher(ss, tr, nil)

	inst, err := domain.NewInstruction(
		uuid.New(), "payment.create", "conn-bench",
		map[string]any{"amount": "100.00"},
	)
	if err != nil {
		b.Fatal(err)
	}

	conn, err := domain.NewProviderConnection(
		"tenant-bench", "bench-provider", "bank",
		domain.ProtocolHTTPS, server.URL,
		&domain.APIKeyAuth{HeaderName: "X-API-Key", SecretRef: "KEY"},
		domain.RetryPolicy{}, domain.RateLimitConfig{},
	)
	if err != nil {
		b.Fatal(err)
	}

	route := simpleRoute("POST")
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			result := d.Dispatch(ctx, inst, conn, route)
			if result.Error != nil {
				b.Fatal(result.Error)
			}
		}
	})
}
