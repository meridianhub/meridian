package auth_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/auth"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleAuthorize_StateStoreFull verifies that when the in-flight OIDC
// state store is at capacity, HandleAuthorize returns 503 Service Unavailable
// with a Retry-After: 30 header instead of a generic 500.
//
// Backpressure from a full store is a transient condition — clients should
// retry, not treat it as a permanent failure.
func TestHandleAuthorize_StateStoreFull(t *testing.T) {
	signer, err := platformauth.NewJWTSigner(platformauth.JWTSignerConfig{})
	require.NoError(t, err)

	stateStore := auth.NewOIDCStateStore()
	t.Cleanup(stateStore.Close)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		ConsentStore:      newFakeConsentStore(),
		DefaultTenantSlug: "acme",
		BaseURL:           "https://demo.meridianhub.cloud",
		BaseDomain:        "demo.meridianhub.cloud",
		StateStore:        stateStore,
		CodeStore:         newTestStore(t),
		Signer:            signer,
		Logger:            slog.Default(),
	})
	require.NoError(t, err)

	// Fill the state store to capacity via the public API.
	for i := 0; i < 10_000; i++ {
		_, err := stateStore.Store(auth.OIDCFlowState{IssuedAt: time.Now()})
		require.NoError(t, err, "expected store to accept entry %d", i)
	}
	// One more should fail — but we rely on the handler returning 503.

	_, challenge := generatePKCEPair(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"https://claude.ai/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "30", w.Header().Get("Retry-After"))
}
