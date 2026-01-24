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
}

// NewClaimConfig creates a ClaimConfig populated from environment variables.
// Environment variables:
//   - SAGA_LEASE_DURATION: Duration string (e.g., "5m", "10m"). Default: "5m"
//   - SAGA_CLAIM_BATCH_SIZE: Integer. Default: 10
//   - SAGA_CLAIM_JITTER_MS: Integer milliseconds. Default: 500
//   - SAGA_MAX_REPLAYS: Maximum replay attempts before zombie detection. Default: 5
//   - HOSTNAME: Pod identifier (Kubernetes sets this). Fallback: generated UUID
func NewClaimConfig() *ClaimConfig {
	return &ClaimConfig{
		LeaseDuration: env.GetEnvAsDuration("SAGA_LEASE_DURATION", 5*time.Minute),
		BatchSize:     env.GetEnvAsInt("SAGA_CLAIM_BATCH_SIZE", 10),
		MaxJitterMS:   env.GetEnvAsInt("SAGA_CLAIM_JITTER_MS", 500),
		MaxReplays:    env.GetEnvAsInt("SAGA_MAX_REPLAYS", DefaultMaxReplays),
		PodID:         GetPodID(),
	}
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

// ClaimService handles claiming orphaned sagas using FOR UPDATE SKIP LOCKED.
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
// Uses PostgreSQL FOR UPDATE SKIP LOCKED to prevent race conditions when
// multiple pods attempt to claim simultaneously. The SKIP LOCKED clause
// ensures that rows being claimed by another transaction are skipped,
// rather than waiting for the lock.
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

	// The claiming query uses a subquery with FOR UPDATE SKIP LOCKED
	// to atomically select and update orphaned sagas.
	//
	// Query explanation:
	// 1. Inner SELECT finds orphaned sagas (expired lease or no owner)
	// 2. Filter out zombies (replay_count >= MaxReplays)
	// 3. FOR UPDATE SKIP LOCKED acquires row locks, skipping locked rows
	// 4. LIMIT controls batch size
	// 5. Outer UPDATE claims the selected rows and increments replay_count
	// 6. RETURNING gives us the claimed saga IDs
	//
	// Note: PostgreSQL requires the subquery to be a CTE for UPDATE...FROM syntax
	// when using FOR UPDATE SKIP LOCKED in the subquery.

	var claimedIDs []uuid.UUID

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Execute the claim query with replay_count filter and increment
		result := tx.Raw(`
			UPDATE saga_instances
			SET
				claimed_by_pod = ?,
				claimed_at = ?,
				lease_expires_at = ?,
				replay_count = replay_count + 1
			WHERE id IN (
				SELECT id FROM saga_instances
				WHERE status IN (?, ?, ?)
				  AND (lease_expires_at < ? OR claimed_by_pod IS NULL)
				  AND replay_count < ?
				FOR UPDATE SKIP LOCKED
				LIMIT ?
			)
			RETURNING id
		`,
			s.config.PodID,
			now,
			leaseExpiry,
			SagaStatusPending,
			SagaStatusRunning,
			SagaStatusCompensating,
			now,
			s.config.MaxReplays,
			s.config.BatchSize,
		).Scan(&claimedIDs)

		if result.Error != nil {
			return result.Error
		}

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

	// Find and transition zombie sagas in a single atomic operation
	// We use a struct to scan the results including saga_definition_id for metrics
	type zombieResult struct {
		ID               uuid.UUID `gorm:"column:id"`
		SagaDefinitionID uuid.UUID `gorm:"column:saga_definition_id"`
		ReplayCount      int       `gorm:"column:replay_count"`
	}
	var zombies []zombieResult

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Select zombie sagas and transition them atomically
		result := tx.Raw(`
			UPDATE saga_instances
			SET
				status = ?,
				claimed_by_pod = NULL,
				claimed_at = NULL,
				lease_expires_at = NULL,
				updated_at = ?
			WHERE id IN (
				SELECT id FROM saga_instances
				WHERE status IN (?, ?, ?)
				  AND (lease_expires_at < ? OR claimed_by_pod IS NULL)
				  AND replay_count >= ?
				FOR UPDATE SKIP LOCKED
			)
			RETURNING id, saga_definition_id, replay_count
		`,
			SagaStatusFailedManualIntervention,
			now,
			SagaStatusPending,
			SagaStatusRunning,
			SagaStatusCompensating,
			now,
			s.config.MaxReplays,
		).Scan(&zombies)

		if result.Error != nil {
			return result.Error
		}

		return nil
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
func (s *ClaimService) ReleaseLease(ctx context.Context, sagaID uuid.UUID) error {
	result := s.db.WithContext(ctx).Model(&SagaInstance{}).
		Where("id = ? AND claimed_by_pod = ?", sagaID, s.config.PodID).
		Updates(map[string]interface{}{
			"claimed_by_pod":   nil,
			"claimed_at":       nil,
			"lease_expires_at": nil,
		})

	return result.Error
}
