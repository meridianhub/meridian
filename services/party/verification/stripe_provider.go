package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/party/config"
	"github.com/meridianhub/meridian/services/party/domain"
)

const (
	stripeDefaultBaseURL = "https://api.stripe.com"
	stripeHTTPTimeout    = 30 * time.Second
)

// Stripe-specific errors
var (
	ErrStripeMissingAPIKey = errors.New("stripe: api_key is required in provider config")
	ErrStripeUnauthorized  = errors.New("stripe: unauthorized - check API key")
	ErrStripeRateLimited   = errors.New("stripe: rate limited - retry later")
	ErrStripeServerError   = errors.New("stripe: server error")
)

// StripeIdentityProvider implements the Provider interface using the Stripe Identity API.
type StripeIdentityProvider struct {
	apiKey        string
	baseURL       string
	client        *http.Client
	logger        *slog.Logger
	stripeAccount string // Optional: for Stripe Connect (connected account header)
}

// Ensure StripeIdentityProvider implements Provider at compile time.
var _ Provider = (*StripeIdentityProvider)(nil)

// NewStripeIdentityProvider creates a new StripeIdentityProvider from the given configuration.
func NewStripeIdentityProvider(cfg *config.VerificationConfig, logger *slog.Logger) (*StripeIdentityProvider, error) {
	apiKey := cfg.ProviderConfig["api_key"]
	if apiKey == "" {
		return nil, ErrStripeMissingAPIKey
	}

	baseURL := cfg.ProviderConfig["base_url"]
	if baseURL == "" {
		baseURL = stripeDefaultBaseURL
	}

	stripeAccount := cfg.ProviderConfig["stripe_account"]

	return &StripeIdentityProvider{
		apiKey:  apiKey,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: stripeHTTPTimeout,
		},
		logger:        logger,
		stripeAccount: stripeAccount,
	}, nil
}

// stripeVerificationSessionResponse is the response from creating or retrieving a Stripe verification session.
type stripeVerificationSessionResponse struct {
	ID                     string `json:"id"`
	Status                 string `json:"status"`
	ClientSecret           string `json:"client_secret"`
	LastVerificationReport string `json:"last_verification_report"`
}

// stripeErrorResponse is the error response from the Stripe API.
type stripeErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// VerifyIdentity creates a Stripe Identity verification session for the given party.
func (p *StripeIdentityProvider) VerifyIdentity(ctx context.Context, party *domain.Party) (Result, error) {
	p.logger.Info("initiating identity verification",
		slog.String("party_id", party.ID().String()),
		slog.String("party_type", string(party.PartyType())),
	)

	formData := url.Values{}
	formData.Set("type", "document")
	formData.Set("metadata[party_id]", party.ID().String())
	formData.Set("metadata[party_type]", string(party.PartyType()))

	var session stripeVerificationSessionResponse
	if err := p.postForm(ctx, "/v1/identity/verification_sessions", formData, &session); err != nil {
		return Result{}, fmt.Errorf("stripe: create verification session: %w", err)
	}

	status, reason, riskScore := mapStripeSessionStatus(session.Status)

	var completedAt *time.Time
	if status != StatusPending {
		now := time.Now()
		completedAt = &now
	}

	result := Result{
		VerificationID: session.ID,
		Status:         status,
		Reason:         reason,
		RiskScore:      riskScore,
		CompletedAt:    completedAt,
		Metadata: map[string]string{
			"provider":       "stripe",
			"party_id":       party.ID().String(),
			"party_type":     string(party.PartyType()),
			"session_status": session.Status,
		},
	}

	p.logger.Info("identity verification initiated",
		slog.String("verification_id", session.ID),
		slog.String("status", string(status)),
	)

	return result, nil
}

// CheckSanctions returns a clear result with a note that Stripe Identity does not support sanctions screening.
func (p *StripeIdentityProvider) CheckSanctions(_ context.Context, party *domain.Party) (SanctionsResult, error) {
	p.logger.Info("stripe identity does not support sanctions screening",
		slog.String("party_id", party.ID().String()))
	return SanctionsResult{
		Status:     SanctionsStatusClear,
		ScreenedAt: time.Now(),
		Metadata: map[string]string{
			"provider": "stripe",
			"note":     "sanctions screening not supported by Stripe Identity",
		},
	}, nil
}

// GetVerificationStatus retrieves the current status of a Stripe verification session.
func (p *StripeIdentityProvider) GetVerificationStatus(ctx context.Context, verificationID string) (Result, error) {
	p.logger.Info("retrieving verification status",
		slog.String("verification_id", verificationID),
	)

	var session stripeVerificationSessionResponse
	if err := p.getJSON(ctx, "/v1/identity/verification_sessions/"+verificationID, &session); err != nil {
		return Result{}, fmt.Errorf("stripe: get verification session: %w", err)
	}

	status, reason, riskScore := mapStripeSessionStatus(session.Status)

	var completedAt *time.Time
	if status != StatusPending {
		now := time.Now()
		completedAt = &now
	}

	result := Result{
		VerificationID: session.ID,
		Status:         status,
		Reason:         reason,
		RiskScore:      riskScore,
		CompletedAt:    completedAt,
		Metadata: map[string]string{
			"provider":       "stripe",
			"session_status": session.Status,
		},
	}

	return result, nil
}

// postForm sends a POST request with form-encoded body and decodes the JSON response.
func (p *StripeIdentityProvider) postForm(ctx context.Context, path string, data url.Values, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if p.stripeAccount != "" {
		req.Header.Set("Stripe-Account", p.stripeAccount)
	}

	p.logger.Debug("sending POST request",
		slog.String("path", path),
	)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return p.handleResponse(resp, result)
}

// getJSON sends a GET request and decodes the JSON response.
func (p *StripeIdentityProvider) getJSON(ctx context.Context, path string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	if p.stripeAccount != "" {
		req.Header.Set("Stripe-Account", p.stripeAccount)
	}

	p.logger.Debug("sending GET request",
		slog.String("path", path),
	)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return p.handleResponse(resp, result)
}

// handleResponse processes the HTTP response and decodes the JSON body.
func (p *StripeIdentityProvider) handleResponse(resp *http.Response, result interface{}) error {
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		return nil

	case resp.StatusCode == http.StatusUnauthorized:
		p.logger.Error("authentication failed",
			slog.Int("status_code", resp.StatusCode),
		)
		return ErrStripeUnauthorized

	case resp.StatusCode == http.StatusTooManyRequests:
		p.logger.Warn("rate limited by Stripe API",
			slog.Int("status_code", resp.StatusCode),
		)
		return ErrStripeRateLimited

	case resp.StatusCode == http.StatusNotFound:
		return ErrVerificationNotFound

	default:
		var stripeErr stripeErrorResponse
		if jsonErr := json.Unmarshal(respBody, &stripeErr); jsonErr == nil && stripeErr.Error.Message != "" {
			p.logger.Error("stripe API error",
				slog.Int("status_code", resp.StatusCode),
				slog.String("error_type", stripeErr.Error.Type),
				slog.String("error_message", stripeErr.Error.Message),
			)
			return fmt.Errorf("%w: %s: %s", ErrStripeServerError, stripeErr.Error.Type, stripeErr.Error.Message)
		}

		p.logger.Error("unexpected API response",
			slog.Int("status_code", resp.StatusCode),
		)
		return fmt.Errorf("%w: status %d", ErrStripeServerError, resp.StatusCode)
	}
}

// mapStripeSessionStatus maps Stripe verification session status to our domain types.
func mapStripeSessionStatus(stripeStatus string) (Status, string, float64) {
	switch stripeStatus {
	case "requires_input":
		return StatusPending, "Waiting for user to complete verification", 0.0

	case "processing":
		return StatusPending, "Stripe is processing the verification", 0.0

	case "verified":
		return StatusApproved, "Identity verification passed", 0.1

	case "canceled":
		return StatusRejected, "Verification was canceled", 0.0

	case "requires_action":
		return StatusManualReview, "Verification requires manual review", 0.5

	default:
		return StatusPending, "Unknown status: " + stripeStatus, 0.0
	}
}
