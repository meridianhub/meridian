package verification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/party/config"
	"github.com/meridianhub/meridian/services/party/domain"
)

const (
	onfidoDefaultBaseURL = "https://api.onfido.com/v3.6"
	onfidoHTTPTimeout    = 30 * time.Second
)

// Onfido-specific errors
var (
	ErrOnfidoMissingAPIKey  = errors.New("onfido: api_key is required in provider config")
	ErrOnfidoUnauthorized   = errors.New("onfido: unauthorized - check API token")
	ErrOnfidoRateLimited    = errors.New("onfido: rate limited - retry later")
	ErrOnfidoServerError    = errors.New("onfido: server error")
	ErrOnfidoValidationFail = errors.New("onfido: validation error")
)

// OnfidoProvider implements the Provider interface using the Onfido API.
type OnfidoProvider struct {
	apiToken string
	baseURL  string
	client   *http.Client
	logger   *slog.Logger
}

// Ensure OnfidoProvider implements Provider at compile time.
var _ Provider = (*OnfidoProvider)(nil)

// NewOnfidoProvider creates a new OnfidoProvider from the given configuration.
func NewOnfidoProvider(cfg *config.VerificationConfig, logger *slog.Logger) (*OnfidoProvider, error) {
	apiKey := cfg.ProviderConfig["api_key"]
	if apiKey == "" {
		return nil, ErrOnfidoMissingAPIKey
	}

	baseURL := cfg.ProviderConfig["base_url"]
	if baseURL == "" {
		baseURL = onfidoDefaultBaseURL
	}

	return &OnfidoProvider{
		apiToken: apiKey,
		baseURL:  baseURL,
		client: &http.Client{
			Timeout: onfidoHTTPTimeout,
		},
		logger: logger,
	}, nil
}

// onfidoApplicantRequest is the request body for creating an Onfido applicant.
type onfidoApplicantRequest struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// onfidoApplicantResponse is the response from creating an Onfido applicant.
type onfidoApplicantResponse struct {
	ID        string `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// onfidoCheckRequest is the request body for creating an Onfido check.
type onfidoCheckRequest struct {
	ApplicantID string   `json:"applicant_id"`
	ReportNames []string `json:"report_names"`
}

// onfidoCheckResponse is the response from creating or retrieving an Onfido check.
type onfidoCheckResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Result      string `json:"result"`
	ApplicantID string `json:"applicant_id"`
}

// onfidoErrorResponse is the error response from the Onfido API.
type onfidoErrorResponse struct {
	Error struct {
		Type    string            `json:"type"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	} `json:"error"`
}

// VerifyIdentity creates an Onfido applicant and initiates an identity check.
func (p *OnfidoProvider) VerifyIdentity(ctx context.Context, party *domain.Party) (Result, error) {
	p.logger.Info("initiating identity verification",
		slog.String("party_id", party.ID().String()),
		slog.String("party_type", string(party.PartyType())),
	)

	// Create applicant
	firstName, lastName := splitName(party.LegalName())
	applicant, err := p.createApplicant(ctx, firstName, lastName)
	if err != nil {
		return Result{}, fmt.Errorf("onfido: create applicant: %w", err)
	}

	// Create identity check
	check, err := p.createCheck(ctx, applicant.ID, []string{"identity_enhanced"})
	if err != nil {
		return Result{}, fmt.Errorf("onfido: create check: %w", err)
	}

	status, reason, riskScore := mapOnfidoCheckStatus(check.Status, check.Result)

	var completedAt *time.Time
	if status != StatusPending {
		now := time.Now()
		completedAt = &now
	}

	result := Result{
		VerificationID: check.ID,
		Status:         status,
		Reason:         reason,
		RiskScore:      riskScore,
		CompletedAt:    completedAt,
		Metadata: map[string]string{
			"provider":     "onfido",
			"applicant_id": applicant.ID,
			"party_id":     party.ID().String(),
			"party_type":   string(party.PartyType()),
			"check_status": check.Status,
			"check_result": check.Result,
		},
	}

	p.logger.Info("identity verification initiated",
		slog.String("verification_id", check.ID),
		slog.String("status", string(status)),
	)

	return result, nil
}

// CheckSanctions creates an Onfido applicant and initiates a watchlist check.
func (p *OnfidoProvider) CheckSanctions(ctx context.Context, party *domain.Party) (SanctionsResult, error) {
	p.logger.Info("initiating sanctions screening",
		slog.String("party_id", party.ID().String()),
		slog.String("party_name", party.LegalName()),
	)

	// Create applicant
	firstName, lastName := splitName(party.LegalName())
	applicant, err := p.createApplicant(ctx, firstName, lastName)
	if err != nil {
		return SanctionsResult{}, fmt.Errorf("onfido: create applicant: %w", err)
	}

	// Create watchlist check
	check, err := p.createCheck(ctx, applicant.ID, []string{"watchlist_enhanced"})
	if err != nil {
		return SanctionsResult{}, fmt.Errorf("onfido: create check: %w", err)
	}

	sanctionsStatus, matches := mapOnfidoSanctionsStatus(check.Status, check.Result)

	result := SanctionsResult{
		ScreeningID: check.ID,
		Status:      sanctionsStatus,
		Matches:     matches,
		ScreenedAt:  time.Now(),
		Metadata: map[string]string{
			"provider":     "onfido",
			"applicant_id": applicant.ID,
			"party_id":     party.ID().String(),
			"check_status": check.Status,
			"check_result": check.Result,
		},
	}

	p.logger.Info("sanctions screening initiated",
		slog.String("screening_id", check.ID),
		slog.String("status", string(sanctionsStatus)),
	)

	return result, nil
}

// GetVerificationStatus retrieves the current status of an Onfido check.
func (p *OnfidoProvider) GetVerificationStatus(ctx context.Context, verificationID string) (Result, error) {
	p.logger.Info("retrieving verification status",
		slog.String("verification_id", verificationID),
	)

	check, err := p.getCheck(ctx, verificationID)
	if err != nil {
		return Result{}, fmt.Errorf("onfido: get check: %w", err)
	}

	status, reason, riskScore := mapOnfidoCheckStatus(check.Status, check.Result)

	var completedAt *time.Time
	if status != StatusPending {
		now := time.Now()
		completedAt = &now
	}

	result := Result{
		VerificationID: check.ID,
		Status:         status,
		Reason:         reason,
		RiskScore:      riskScore,
		CompletedAt:    completedAt,
		Metadata: map[string]string{
			"provider":     "onfido",
			"check_status": check.Status,
			"check_result": check.Result,
		},
	}

	return result, nil
}

// createApplicant creates a new applicant in Onfido.
func (p *OnfidoProvider) createApplicant(ctx context.Context, firstName, lastName string) (*onfidoApplicantResponse, error) {
	reqBody := onfidoApplicantRequest{
		FirstName: firstName,
		LastName:  lastName,
	}

	var resp onfidoApplicantResponse
	if err := p.postJSON(ctx, "/applicants", reqBody, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// createCheck creates a new check in Onfido.
func (p *OnfidoProvider) createCheck(ctx context.Context, applicantID string, reportNames []string) (*onfidoCheckResponse, error) {
	reqBody := onfidoCheckRequest{
		ApplicantID: applicantID,
		ReportNames: reportNames,
	}

	var resp onfidoCheckResponse
	if err := p.postJSON(ctx, "/checks", reqBody, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// getCheck retrieves a check by ID from Onfido.
func (p *OnfidoProvider) getCheck(ctx context.Context, checkID string) (*onfidoCheckResponse, error) {
	var resp onfidoCheckResponse
	if err := p.getJSON(ctx, "/checks/"+checkID, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// postJSON sends a POST request with JSON body and decodes the response.
func (p *OnfidoProvider) postJSON(ctx context.Context, path string, body interface{}, result interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Token token="+p.apiToken)
	req.Header.Set("Content-Type", "application/json")

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
func (p *OnfidoProvider) getJSON(ctx context.Context, path string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Token token="+p.apiToken)
	req.Header.Set("Content-Type", "application/json")

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
func (p *OnfidoProvider) handleResponse(resp *http.Response, result interface{}) error {
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
		return ErrOnfidoUnauthorized

	case resp.StatusCode == http.StatusTooManyRequests:
		p.logger.Warn("rate limited by Onfido API",
			slog.Int("status_code", resp.StatusCode),
		)
		return ErrOnfidoRateLimited

	case resp.StatusCode == http.StatusNotFound:
		return ErrVerificationNotFound

	default:
		var onfidoErr onfidoErrorResponse
		if jsonErr := json.Unmarshal(respBody, &onfidoErr); jsonErr == nil && onfidoErr.Error.Message != "" {
			p.logger.Error("onfido API error",
				slog.Int("status_code", resp.StatusCode),
				slog.String("error_type", onfidoErr.Error.Type),
				slog.String("error_message", onfidoErr.Error.Message),
			)
			return fmt.Errorf("%w: %s: %s", ErrOnfidoValidationFail, onfidoErr.Error.Type, onfidoErr.Error.Message)
		}

		p.logger.Error("unexpected API response",
			slog.Int("status_code", resp.StatusCode),
		)
		return fmt.Errorf("%w: status %d", ErrOnfidoServerError, resp.StatusCode)
	}
}

// splitName splits a full name into first and last name components.
// If the name contains no space, the entire name is used as the first name
// and the last name defaults to "-" (Onfido requires a last name).
func splitName(fullName string) (string, string) {
	parts := strings.SplitN(fullName, " ", 2)
	if len(parts) == 1 {
		return parts[0], "-"
	}
	return parts[0], parts[1]
}

// mapOnfidoCheckStatus maps Onfido check status and result to our domain types.
func mapOnfidoCheckStatus(onfidoStatus, onfidoResult string) (Status, string, float64) {
	switch onfidoStatus {
	case "in_progress", "awaiting_applicant":
		return StatusPending, "Verification in progress", 0.0

	case "complete":
		switch onfidoResult {
		case "clear":
			return StatusApproved, "Identity verification passed", 0.1
		case "consider":
			return StatusManualReview, "Verification requires manual review", 0.5
		default:
			return StatusRejected, "Identity verification failed", 0.9
		}

	case "withdrawn", "cancelled":
		return StatusRejected, "Verification was cancelled", 0.0

	case "paused":
		return StatusManualReview, "Verification paused - manual review required", 0.5

	default:
		return StatusPending, "Unknown status: " + onfidoStatus, 0.0
	}
}

// mapOnfidoSanctionsStatus maps Onfido watchlist check results to sanctions domain types.
func mapOnfidoSanctionsStatus(onfidoStatus, onfidoResult string) (SanctionsStatus, []SanctionsMatch) {
	switch onfidoStatus {
	case "in_progress", "awaiting_applicant":
		return SanctionsStatusPending, nil

	case "complete":
		switch onfidoResult {
		case "clear":
			return SanctionsStatusClear, nil
		default:
			return SanctionsStatusMatch, []SanctionsMatch{
				{
					ListName:        "ONFIDO_WATCHLIST",
					MatchedName:     "potential match detected",
					MatchConfidence: 0.8,
					ListEntryID:     "onfido-watchlist-match",
				},
			}
		}

	default:
		return SanctionsStatusError, nil
	}
}
