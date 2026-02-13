package verification

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
)

// TimeoutVerificationRepository defines the repository operations needed by the timeout handler.
type TimeoutVerificationRepository interface {
	ListPendingVerifications(ctx context.Context) ([]persistence.PartyVerificationEntity, error)
	UpdateVerificationStatus(ctx context.Context, verificationID uuid.UUID, status string, riskScore *float64, reason *string, completedAt *time.Time, currentVersion int64) error
}

// StatusChecker is the subset of Provider needed by the timeout handler.
type StatusChecker interface {
	GetVerificationStatus(ctx context.Context, verificationID string) (Result, error)
}

// TimeoutHandlerConfig holds configuration for the TimeoutHandler.
type TimeoutHandlerConfig struct {
	VerificationRepo TimeoutVerificationRepository
	Provider         StatusChecker
	Timeout          time.Duration // How long before a PENDING verification is considered stuck. Default: 24h.
	PollInterval     time.Duration // How often to check for stuck verifications. Default: 1h.
	Logger           *slog.Logger
}

// TimeoutHandler detects PENDING verifications that have exceeded the configured
// timeout and resolves them by checking the provider for a final status or
// marking them for manual review.
type TimeoutHandler struct {
	repo         TimeoutVerificationRepository
	provider     StatusChecker
	timeout      time.Duration
	pollInterval time.Duration
	logger       *slog.Logger
}

// NewTimeoutHandler creates a new TimeoutHandler with the given configuration.
func NewTimeoutHandler(cfg TimeoutHandlerConfig) *TimeoutHandler {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 24 * time.Hour
	}
	pollInterval := cfg.PollInterval
	if pollInterval == 0 {
		pollInterval = 1 * time.Hour
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	return &TimeoutHandler{
		repo:         cfg.VerificationRepo,
		provider:     cfg.Provider,
		timeout:      timeout,
		pollInterval: pollInterval,
		logger:       logger,
	}
}

// Run starts the timeout handler loop. It blocks until ctx is cancelled.
func (h *TimeoutHandler) Run(ctx context.Context) {
	h.logger.Info("timeout handler started",
		"timeout", h.timeout,
		"poll_interval", h.pollInterval)

	// Run an immediate check before entering the ticker loop.
	h.processTimedOut(ctx)

	ticker := time.NewTicker(h.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("timeout handler stopped")
			return
		case <-ticker.C:
			h.processTimedOut(ctx)
		}
	}
}

// processTimedOut finds PENDING verifications older than the timeout and resolves them.
func (h *TimeoutHandler) processTimedOut(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	pending, err := h.repo.ListPendingVerifications(ctx)
	if err != nil {
		h.logger.Error("failed to list pending verifications", "error", err)
		return
	}

	cutoff := time.Now().Add(-h.timeout)

	for _, v := range pending {
		if ctx.Err() != nil {
			return
		}

		if v.CreatedAt.After(cutoff) {
			continue
		}

		h.logger.Info("processing timed-out verification",
			"verification_id", v.ID,
			"party_id", v.PartyID,
			"provider_verification_id", v.VerificationID,
			"created_at", v.CreatedAt)

		h.resolveVerification(ctx, v)
	}
}

// resolveVerification checks the provider for the current status. If the provider
// returns a terminal status, updates the verification accordingly. If the provider
// still returns PENDING, marks the verification as MANUAL_REVIEW.
func (h *TimeoutHandler) resolveVerification(ctx context.Context, v persistence.PartyVerificationEntity) {
	result, err := h.provider.GetVerificationStatus(ctx, v.VerificationID)
	if err != nil {
		h.logger.Error("failed to check provider status for timed-out verification",
			"verification_id", v.ID,
			"provider_verification_id", v.VerificationID,
			"error", err)
		return
	}

	var (
		status      string
		riskScore   *float64
		reason      *string
		completedAt *time.Time
	)

	if result.Status != StatusPending {
		// Provider has a terminal status - use it
		status = string(result.Status)
		if result.RiskScore != 0 {
			rs := result.RiskScore
			riskScore = &rs
		}
		if result.Reason != "" {
			r := result.Reason
			reason = &r
		}
		completedAt = result.CompletedAt
		if completedAt == nil {
			now := time.Now()
			completedAt = &now
		}
	} else {
		// Provider still returns PENDING after timeout - escalate to manual review
		status = string(StatusManualReview)
		r := "Verification timed out after " + h.timeout.String()
		reason = &r
		now := time.Now()
		completedAt = &now
	}

	if err := h.repo.UpdateVerificationStatus(ctx, v.ID, status, riskScore, reason, completedAt, v.Version); err != nil {
		h.logger.Error("failed to update timed-out verification",
			"verification_id", v.ID,
			"status", status,
			"error", err)
		return
	}

	h.logger.Info("timed-out verification resolved",
		"verification_id", v.ID,
		"party_id", v.PartyID,
		"status", status)
}
