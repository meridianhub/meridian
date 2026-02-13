package verification

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/party/config"
	"github.com/meridianhub/meridian/services/party/domain"
)

func newTestParty(t *testing.T, name string) *domain.Party {
	t.Helper()
	party, err := domain.NewParty(domain.PartyTypePerson, name)
	require.NoError(t, err)
	return party
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func newTestConfig(baseURL string) *config.VerificationConfig {
	return &config.VerificationConfig{
		Provider: "onfido",
		ProviderConfig: map[string]string{
			"api_key":  "test_token_abc123",
			"base_url": baseURL,
		},
		WebhookSecret: "webhook-secret",
		WebhookURL:    "https://example.com/webhook",
	}
}

func TestNewOnfidoProvider_Success(t *testing.T) {
	cfg := newTestConfig("https://api.onfido.com/v3.6")

	provider, err := NewOnfidoProvider(cfg, newTestLogger())

	require.NoError(t, err)
	require.NotNil(t, provider)
	assert.Equal(t, "test_token_abc123", provider.apiToken)
	assert.Equal(t, "https://api.onfido.com/v3.6", provider.baseURL)
}

func TestNewOnfidoProvider_DefaultBaseURL(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider: "onfido",
		ProviderConfig: map[string]string{
			"api_key": "test_token",
		},
	}

	provider, err := NewOnfidoProvider(cfg, newTestLogger())

	require.NoError(t, err)
	assert.Equal(t, onfidoDefaultBaseURL, provider.baseURL)
}

func TestNewOnfidoProvider_MissingAPIKey(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider:       "onfido",
		ProviderConfig: map[string]string{},
	}

	provider, err := NewOnfidoProvider(cfg, newTestLogger())

	assert.Nil(t, provider)
	assert.ErrorIs(t, err, ErrOnfidoMissingAPIKey)
}

func TestNewOnfidoProvider_NilProviderConfig(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider:       "onfido",
		ProviderConfig: nil,
	}

	provider, err := NewOnfidoProvider(cfg, newTestLogger())

	assert.Nil(t, provider)
	assert.ErrorIs(t, err, ErrOnfidoMissingAPIKey)
}

func TestOnfidoProvider_VerifyIdentity_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Token token=test_token_abc123", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/applicants":
			var req onfidoApplicantRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			require.NoError(t, err)
			assert.Equal(t, "Jane", req.FirstName)
			assert.Equal(t, "Smith", req.LastName)

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoApplicantResponse{
				ID:        "applicant-001",
				FirstName: req.FirstName,
				LastName:  req.LastName,
			})

		case r.Method == http.MethodPost && r.URL.Path == "/checks":
			var req onfidoCheckRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			require.NoError(t, err)
			assert.Equal(t, "applicant-001", req.ApplicantID)
			assert.Equal(t, []string{"identity_enhanced"}, req.ReportNames)

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoCheckResponse{
				ID:          "check-001",
				Status:      "complete",
				Result:      "clear",
				ApplicantID: req.ApplicantID,
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "Jane Smith")
	result, err := provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, "check-001", result.VerificationID)
	assert.Equal(t, StatusApproved, result.Status)
	assert.Equal(t, "Identity verification passed", result.Reason)
	assert.InDelta(t, 0.1, result.RiskScore, 0.001)
	assert.NotNil(t, result.CompletedAt)
	assert.Equal(t, "onfido", result.Metadata["provider"])
	assert.Equal(t, "applicant-001", result.Metadata["applicant_id"])
}

func TestOnfidoProvider_VerifyIdentity_Rejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/applicants":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoApplicantResponse{ID: "applicant-002"})

		case "/checks":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoCheckResponse{
				ID:     "check-002",
				Status: "complete",
				Result: "unidentified",
			})
		}
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	result, err := provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, StatusRejected, result.Status)
	assert.Equal(t, "Identity verification failed", result.Reason)
	assert.InDelta(t, 0.9, result.RiskScore, 0.001)
}

func TestOnfidoProvider_VerifyIdentity_ManualReview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/applicants":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoApplicantResponse{ID: "applicant-003"})

		case "/checks":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoCheckResponse{
				ID:     "check-003",
				Status: "complete",
				Result: "consider",
			})
		}
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "Alice Jones")
	result, err := provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, StatusManualReview, result.Status)
	assert.Equal(t, "Verification requires manual review", result.Reason)
	assert.InDelta(t, 0.5, result.RiskScore, 0.001)
}

func TestOnfidoProvider_VerifyIdentity_Pending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/applicants":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoApplicantResponse{ID: "applicant-004"})

		case "/checks":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoCheckResponse{
				ID:     "check-004",
				Status: "in_progress",
				Result: "",
			})
		}
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "Bob Builder")
	result, err := provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, StatusPending, result.Status)
	assert.Nil(t, result.CompletedAt)
}

func TestOnfidoProvider_VerifyIdentity_SingleName(t *testing.T) {
	var capturedFirstName, capturedLastName string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/applicants":
			var req onfidoApplicantRequest
			json.NewDecoder(r.Body).Decode(&req)
			capturedFirstName = req.FirstName
			capturedLastName = req.LastName

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoApplicantResponse{ID: "applicant-005"})

		case "/checks":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoCheckResponse{
				ID:     "check-005",
				Status: "complete",
				Result: "clear",
			})
		}
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "Madonna")
	_, err = provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, "Madonna", capturedFirstName)
	assert.Equal(t, "-", capturedLastName)
}

func TestOnfidoProvider_CheckSanctions_Clear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/applicants":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoApplicantResponse{ID: "applicant-010"})

		case "/checks":
			var req onfidoCheckRequest
			json.NewDecoder(r.Body).Decode(&req)
			assert.Equal(t, []string{"watchlist_enhanced"}, req.ReportNames)

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoCheckResponse{
				ID:     "check-010",
				Status: "complete",
				Result: "clear",
			})
		}
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "Safe Person")
	result, err := provider.CheckSanctions(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, SanctionsStatusClear, result.Status)
	assert.Empty(t, result.Matches)
	assert.NotEmpty(t, result.ScreeningID)
	assert.Equal(t, "onfido", result.Metadata["provider"])
}

func TestOnfidoProvider_CheckSanctions_Match(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/applicants":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoApplicantResponse{ID: "applicant-011"})

		case "/checks":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoCheckResponse{
				ID:     "check-011",
				Status: "complete",
				Result: "consider",
			})
		}
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "Suspicious Person")
	result, err := provider.CheckSanctions(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, SanctionsStatusMatch, result.Status)
	assert.Len(t, result.Matches, 1)
	assert.Equal(t, "ONFIDO_WATCHLIST", result.Matches[0].ListName)
}

func TestOnfidoProvider_CheckSanctions_Pending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/applicants":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoApplicantResponse{ID: "applicant-012"})

		case "/checks":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoCheckResponse{
				ID:     "check-012",
				Status: "in_progress",
			})
		}
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "Pending Person")
	result, err := provider.CheckSanctions(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, SanctionsStatusPending, result.Status)
	assert.Empty(t, result.Matches)
}

func TestOnfidoProvider_GetVerificationStatus_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/checks/check-020" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(onfidoCheckResponse{
				ID:     "check-020",
				Status: "complete",
				Result: "clear",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	result, err := provider.GetVerificationStatus(context.Background(), "check-020")

	require.NoError(t, err)
	assert.Equal(t, "check-020", result.VerificationID)
	assert.Equal(t, StatusApproved, result.Status)
	assert.NotNil(t, result.CompletedAt)
}

func TestOnfidoProvider_GetVerificationStatus_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	_, err = provider.GetVerificationStatus(context.Background(), "nonexistent")

	assert.ErrorIs(t, err, ErrVerificationNotFound)
}

func TestOnfidoProvider_AuthenticationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(onfidoErrorResponse{
			Error: struct {
				Type    string            `json:"type"`
				Message string            `json:"message"`
				Fields  map[string]string `json:"fields"`
			}{
				Type:    "authorization_error",
				Message: "Invalid token",
			},
		})
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(context.Background(), party)

	assert.ErrorIs(t, err, ErrOnfidoUnauthorized)
}

func TestOnfidoProvider_RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(context.Background(), party)

	assert.ErrorIs(t, err, ErrOnfidoRateLimited)
}

func TestOnfidoProvider_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(context.Background(), party)

	assert.ErrorIs(t, err, ErrOnfidoServerError)
}

func TestOnfidoProvider_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Server never responds - context should cancel the request
		<-r.Context().Done()
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(ctx, party)

	assert.Error(t, err)
}

func TestOnfidoProvider_ServerErrorWithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(onfidoErrorResponse{
			Error: struct {
				Type    string            `json:"type"`
				Message string            `json:"message"`
				Fields  map[string]string `json:"fields"`
			}{
				Type:    "validation_error",
				Message: "first_name is required",
			},
		})
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(context.Background(), party)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation_error")
	assert.Contains(t, err.Error(), "first_name is required")
}

func TestOnfidoProvider_AuthorizationHeader(t *testing.T) {
	var capturedAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuthHeader = r.Header.Get("Authorization")

		switch r.URL.Path {
		case "/applicants":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoApplicantResponse{ID: "applicant-auth"})
		case "/checks":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(onfidoCheckResponse{
				ID:     "check-auth",
				Status: "complete",
				Result: "clear",
			})
		}
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.ProviderConfig["api_key"] = "my_secret_token"
	provider, err := NewOnfidoProvider(cfg, newTestLogger())
	require.NoError(t, err)

	party := newTestParty(t, "John Doe")
	_, err = provider.VerifyIdentity(context.Background(), party)

	require.NoError(t, err)
	assert.Equal(t, "Token token=my_secret_token", capturedAuthHeader)
}

func TestSplitName(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedFirst string
		expectedLast  string
	}{
		{"two parts", "John Smith", "John", "Smith"},
		{"three parts", "Mary Jane Watson", "Mary", "Jane Watson"},
		{"single name", "Madonna", "Madonna", "-"},
		{"with spaces", "Jean Claude Van Damme", "Jean", "Claude Van Damme"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			first, last := splitName(tc.input)
			assert.Equal(t, tc.expectedFirst, first)
			assert.Equal(t, tc.expectedLast, last)
		})
	}
}

func TestMapOnfidoCheckStatus(t *testing.T) {
	tests := []struct {
		name           string
		onfidoStatus   string
		onfidoResult   string
		expectedStatus Status
		expectedScore  float64
	}{
		{"in_progress", "in_progress", "", StatusPending, 0.0},
		{"awaiting_applicant", "awaiting_applicant", "", StatusPending, 0.0},
		{"complete_clear", "complete", "clear", StatusApproved, 0.1},
		{"complete_consider", "complete", "consider", StatusManualReview, 0.5},
		{"complete_unidentified", "complete", "unidentified", StatusRejected, 0.9},
		{"withdrawn", "withdrawn", "", StatusRejected, 0.0},
		{"cancelled", "cancelled", "", StatusRejected, 0.0},
		{"paused", "paused", "", StatusManualReview, 0.5},
		{"unknown", "unknown_status", "", StatusPending, 0.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, _, riskScore := mapOnfidoCheckStatus(tc.onfidoStatus, tc.onfidoResult)
			assert.Equal(t, tc.expectedStatus, status)
			assert.InDelta(t, tc.expectedScore, riskScore, 0.001)
		})
	}
}

func TestMapOnfidoSanctionsStatus(t *testing.T) {
	tests := []struct {
		name           string
		onfidoStatus   string
		onfidoResult   string
		expectedStatus SanctionsStatus
		expectMatches  bool
	}{
		{"pending", "in_progress", "", SanctionsStatusPending, false},
		{"clear", "complete", "clear", SanctionsStatusClear, false},
		{"match", "complete", "consider", SanctionsStatusMatch, true},
		{"error_unknown", "error", "", SanctionsStatusError, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, matches := mapOnfidoSanctionsStatus(tc.onfidoStatus, tc.onfidoResult)
			assert.Equal(t, tc.expectedStatus, status)
			if tc.expectMatches {
				assert.NotEmpty(t, matches)
			} else {
				assert.Empty(t, matches)
			}
		})
	}
}
