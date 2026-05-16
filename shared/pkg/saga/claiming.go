// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/env"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DefaultMaxReplays is the default maximum number of replay attempts before a saga
// is considered a zombie and transitions to FAILED_MANUAL_INTERVENTION.
// This prevents infinite retry loops (FR-19).
const DefaultMaxReplays = 5

// ClaimConfig holds configuration for the saga claim service.
type ClaimConfig struct {
	// LeaseDuration is how long a pod holds a saga lease before it can be claimed by others.
	// Default: 5 minutes (SAGA_LEASE_DURATION)
	LeaseDuration time.Duration

	// BatchSize is the maximum number of sagas to claim in a single operation.
	// Default: 10 (SAGA_CLAIM_BATCH_SIZE)
	BatchSize int

	// MaxJitterMS is the maximum random delay before claiming to prevent thundering herd.
	// Default: 500ms (SAGA_CLAIM_JITTER_MS)
	MaxJitterMS int

	// PodID identifies this pod instance for claiming purposes.
	// Default: HOSTNAME env var or generated UUID
	PodID string

	// MaxReplays is the maximum number of replay attempts before a saga is considered
	// a zombie and transitions to FAILED_MANUAL_INTERVENTION.
	// Default: 5 (SAGA_MAX_REPLAYS)
	MaxReplays int

	// RetryBaseDelay is the base delay for exponential backoff on transient failures.
	// The actual delay for replay N is base*2^N + uniform[0, base) jitter, capped at MaxDelay.
	// Default: 1s (SAGA_RETRY_BASE_DELAY)
	RetryBaseDelay time.Duration

	// RetryMaxDelay caps the computed backoff delay so a high replay_count cannot
	// stretch a retry interval to hours or days.
	// Default: 5m (SAGA_RETRY_MAX_DELAY)
	RetryMaxDelay time.Duration
}

// NewClaimConfig creates a ClaimConfig populated from environment variables.
// Environment variables:
//   - SAGA_LEASE_DURATION: Duration string (e.g., "5m", "10m"). Default: "5m"
//   - SAGA_CLAIM_BATCH_SIZE: Integer. Default: 10
//   - SAGA_CLAIM_JITTER_MS: Integer milliseconds. Default: 500
//   - SAGA_MAX_REPLAYS: Maximum replay attempts before zombie detection. Default: 5
//   - SAGA_RETRY_BASE_DELAY: Base delay for exponential backoff. Default: 1s
//   - SAGA_RETRY_MAX_DELAY: Max delay cap for exponential backoff. Default: 5m
//   - HOSTNAME: Pod identifier (Kubernetes sets this). Fallback: generated UUID
//
// SAGA_RETRY_BASE_DELAY and SAGA_RETRY_MAX_DELAY are sanitized: negative
// values or an inverted pair (base > max) fall back to the package defaults
// rather than being silently accepted. Operator misconfiguration would
// otherwise collapse or bypass backoff at runtime.
func NewClaimConfig() *ClaimConfig {
	baseDelay, maxDelay := loadRetryBounds()
	return &ClaimConfig{
		LeaseDuration:  env.GetEnvAsDuration("SAGA_LEASE_DURATION", 5*time.Minute),
		BatchSize:      env.GetEnvAsInt("SAGA_CLAIM_BATCH_SIZE", 10),
		MaxJitterMS:    env.GetEnvAsInt("SAGA_CLAIM_JITTER_MS", 500),
		MaxReplays:     env.GetEnvAsInt("SAGA_MAX_REPLAYS", DefaultMaxReplays),
		RetryBaseDelay: baseDelay,
		RetryMaxDelay:  maxDelay,
		PodID:          GetPodID(),
	}
}

// loadRetryBounds reads SAGA_RETRY_BASE_DELAY / SAGA_RETRY_MAX_DELAY and
// returns sanitized values. A non-positive value or an inverted pair
// (base > max) is rejected: both fields revert to the package defaults so
// an operator typo cannot disable backoff in production.
func loadRetryBounds() (time.Duration, time.Duration) {
	baseDelay := env.GetEnvAsDuration("SAGA_RETRY_BASE_DELAY", DefaultRetryBaseDelay)
	maxDelay := env.GetEnvAsDuration("SAGA_RETRY_MAX_DELAY", DefaultRetryMaxDelay)

	if baseDelay <= 0 || maxDelay <= 0 || baseDelay > maxDelay {
		slog.Default().Warn("invalid SAGA_RETRY_BASE_DELAY/SAGA_RETRY_MAX_DELAY - falling back to defaults",
			"base_delay", baseDelay,
			"max_delay", maxDelay,
			"default_base", DefaultRetryBaseDelay,
			"default_max", DefaultRetryMaxDelay,
		)
		return DefaultRetryBaseDelay, DefaultRetryMaxDelay
	}
	return baseDelay, maxDelay
}

// GetPodID returns the pod identifier from HOSTNAME env var or generates a UUID.
// In Kubernetes, HOSTNAME is automatically set to the pod name.
// For local development, a UUID is generated as fallback.
func GetPodID() string {
	hostname := strings.TrimSpace(os.Getenv("HOSTNAME"))
	if hostname != "" {
		return hostname
	}
	return uuid.New().String()
}

// ClaimService handles claiming orphaned sagas using FOR UPDATE row locking.
// This service enables safe concurrent recovery across multiple pods without
// race conditions.
type ClaimService struct {
	db     *gorm.DB
	config *ClaimConfig
	logger *slog.Logger
}

// NewClaimService creates a new ClaimService with the given configuration.
func NewClaimService(db *gorm.DB, config *ClaimConfig) *ClaimService {
	return &ClaimService{
		db:     db,
		config: config,
		logger: slog.Default(),
	}
}

// WithLogger sets the logger for the claim service.
func (s *ClaimService) WithLogger(logger *slog.Logger) *ClaimService {
	s.logger = logger
	return s
}

// ClaimOrphanedSagas finds and claims orphaned saga instances.
// A saga is considered orphaned if:
//   - Status is PENDING, RUNNING, or COMPENSATING (active states)
//   - AND (lease_expires_at < NOW() OR claimed_by_pod IS NULL)
//
// Before claiming, this method detects and transitions zombie sagas.
// A zombie saga is one that has exceeded MaxReplays attempts (FR-19).
// Zombie sagas are transitioned to FAILED_MANUAL_INTERVENTION and not claimed.
//
// Uses FOR UPDATE row locking to prevent race conditions when multiple pods
// attempt to claim simultaneously. CockroachDB's serializable isolation
// ensures transaction-level safety without requiring SKIP LOCKED.
//
// Random jitter (0-MaxJitterMS) is applied before claiming to prevent
// thundering herd when multiple pods start recovery simultaneously.
//
// Returns the IDs of successfully claimed sagas.
func (s *ClaimService) ClaimOrphanedSagas(ctx context.Context) ([]uuid.UUID, error) {
	// Apply jitter to prevent thundering herd
	if s.config.MaxJitterMS > 0 {
		jitter := time.Duration(rand.IntN(s.config.MaxJitterMS)) * time.Millisecond
		time.Sleep(jitter)
	}

	// First, detect and transition zombie sagas (replay_count >= MaxReplays)
	if err := s.transitionZombieSagas(ctx); err != nil {
		s.logger.Error("failed to transition zombie sagas",
			"error", err,
		)
		// Continue with claiming - zombie detection is best-effort
	}

	now := time.Now()
	leaseExpiry := now.Add(s.config.LeaseDuration)

	// The claiming operation uses FOR UPDATE to atomically select and update
	// orphaned sagas. Split into two steps (SELECT then UPDATE) because
	// CockroachDB requires FOR UPDATE at the top level of a SELECT.

	var claimedIDs []uuid.UUID

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Step 1: SELECT orphaned saga IDs with FOR UPDATE.
		// CockroachDB's serializable isolation prevents concurrent claiming
		// races at the transaction level, making SKIP LOCKED unnecessary.
		// (CockroachDB does not support SKIP LOCKED under serializable isolation.)
		//
		// The next_retry_at predicate makes the watcher backoff-aware: sagas
		// that hit a transient failure have next_retry_at set to a future
		// timestamp and are skipped until that time elapses. NULL means no
		// backoff in effect (fresh saga or successfully advanced to next step),
		// so the watcher is free to reclaim it immediately.
		var candidates []SagaInstance
		result := tx.Model(&SagaInstance{}).
			Where("status IN ?", []SagaStatus{SagaStatusPending, SagaStatusRunning, SagaStatusCompensating}).
			Where("(lease_expires_at < ? OR claimed_by_pod IS NULL)", now).
			Where("(next_retry_at IS NULL OR next_retry_at <= ?)", now).
			Where("replay_count < ?", s.config.MaxReplays).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").
			Limit(s.config.BatchSize).
			Find(&candidates)

		if result.Error != nil {
			return result.Error
		}
		if len(candidates) == 0 {
			return nil
		}

		// Extract IDs for the UPDATE
		ids := make([]uuid.UUID, len(candidates))
		for i, c := range candidates {
			ids[i] = c.ID
		}

		// Step 2: UPDATE the locked rows.
		result = tx.Model(&SagaInstance{}).
			Where("id IN ?", ids).
			Updates(map[string]interface{}{
				"claimed_by_pod":   s.config.PodID,
				"claimed_at":       now,
				"lease_expires_at": leaseExpiry,
				"replay_count":     gorm.Expr("replay_count + 1"),
			})

		if result.Error != nil {
			return result.Error
		}

		claimedIDs = ids
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Record metrics for replay count increments
	for range claimedIDs {
		RecordReplayIncrement()
	}

	return claimedIDs, nil
}

// transitionZombieSagas finds sagas that have exceeded MaxReplays and
// transitions them to FAILED_MANUAL_INTERVENTION status.
// This prevents infinite retry loops (FR-19).
func (s *ClaimService) transitionZombieSagas(ctx context.Context) error {
	now := time.Now()

	// Find and transition zombie sagas atomically.
	// We capture saga data before updating for metrics logging.
	type zombieResult struct {
		ID               uuid.UUID
		SagaDefinitionID uuid.UUID
		ReplayCount      int
	}
	var zombies []zombieResult

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Step 1: SELECT zombie sagas with FOR UPDATE.
		// CockroachDB's serializable isolation prevents concurrent races,
		// making SKIP LOCKED unnecessary.
		var candidates []SagaInstance
		result := tx.Model(&SagaInstance{}).
			Where("status IN ?", []SagaStatus{SagaStatusPending, SagaStatusRunning, SagaStatusCompensating}).
			Where("(lease_expires_at < ? OR claimed_by_pod IS NULL)", now).
			Where("replay_count >= ?", s.config.MaxReplays).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id, saga_definition_id, replay_count").
			Find(&candidates)

		if result.Error != nil {
			return result.Error
		}
		if len(candidates) == 0 {
			return nil
		}

		// Capture data for metrics before updating
		ids := make([]uuid.UUID, len(candidates))
		for i, c := range candidates {
			ids[i] = c.ID
			zombies = append(zombies, zombieResult{
				ID:               c.ID,
				SagaDefinitionID: c.SagaDefinitionID,
				ReplayCount:      c.ReplayCount,
			})
		}

		// Step 2: UPDATE the locked rows.
		result = tx.Model(&SagaInstance{}).
			Where("id IN ?", ids).
			Updates(map[string]interface{}{
				"status":           SagaStatusFailedManualIntervention,
				"claimed_by_pod":   nil,
				"claimed_at":       nil,
				"lease_expires_at": nil,
				"updated_at":       now,
			})

		return result.Error
	})
	if err != nil {
		return err
	}

	// Log and record metrics for each detected zombie
	for _, zombie := range zombies {
		s.logger.Error("zombie saga detected - transitioned to FAILED_MANUAL_INTERVENTION",
			"saga_id", zombie.ID,
			"saga_definition_id", zombie.SagaDefinitionID,
			"replay_count", zombie.ReplayCount,
			"max_replays", s.config.MaxReplays,
		)
		RecordZombieSagaDetected(zombie.SagaDefinitionID.String())
		RecordReplayCount(zombie.ReplayCount)
	}

	return nil
}

// RenewLease extends the lease on a saga the current pod owns.
// This should be called periodically during long-running saga execution
// to prevent the saga from being claimed by another pod.
func (s *ClaimService) RenewLease(ctx context.Context, sagaID uuid.UUID) error {
	now := time.Now()
	leaseExpiry := now.Add(s.config.LeaseDuration)

	result := s.db.WithContext(ctx).Model(&SagaInstance{}).
		Where("id = ? AND claimed_by_pod = ?", sagaID, s.config.PodID).
		Updates(map[string]interface{}{
			"claimed_at":       now,
			"lease_expires_at": leaseExpiry,
		})

	return result.Error
}

// ReleaseLease releases the lease on a saga, allowing other pods to claim it.
// This should be called when a saga completes or when gracefully shutting down.
//
// Returns nil immediately if the service has no DB handle. This makes the call
// safe inside the executor's handleTransientFailure path when constructing
// test fixtures that supply a ClaimConfig (for retry-bound resolution) but no
// database, and matches the existing nil-claimService guard at the call site.
func (s *ClaimService) ReleaseLease(ctx context.Context, sagaID uuid.UUID) error {
	if s.db == nil {
		return nil
	}
	result := s.db.WithContext(ctx).Model(&SagaInstance{}).
		Where("id = ? AND claimed_by_pod = ?", sagaID, s.config.PodID).
		Updates(map[string]interface{}{
			"claimed_by_pod":   nil,
			"claimed_at":       nil,
			"lease_expires_at": nil,
		})

	return result.Error
}
