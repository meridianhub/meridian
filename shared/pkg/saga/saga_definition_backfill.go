package saga

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// BackfillResult summarizes the outcome of BackfillSagaDefinitionIDs.
type BackfillResult struct {
	// Linked is the number of in-flight saga instances that were successfully
	// linked to a saga_definitions row matching their (saga_name, saga_version).
	Linked int

	// FlaggedManualIntervention is the number of in-flight instances that had no
	// matching saga_definitions row and were transitioned to
	// FAILED_MANUAL_INTERVENTION. These need operator review.
	FlaggedManualIntervention int
}

// BackfillSagaDefinitionIDs reconciles existing in-flight saga instances with
// the new saga_definitions pinning table.
//
// CockroachDB constraint: this MUST run as a separate operation from the
// migration that adds the saga_definitions table itself. CockroachDB rejects
// DML that references a column added in the same transaction. Callers should
// invoke this once on service startup AFTER RunSagaMigrations has completed.
//
// Behavior:
//
//	For each instance whose status is PENDING / RUNNING / COMPENSATING and whose
//	SagaDefinitionID points at a row that does NOT exist in saga_definitions:
//	  1. Look up saga_definitions by (saga_name, saga_version).
//	  2. If found: update SagaDefinitionID to the local pinning row.
//	  3. If not found: transition the instance to FAILED_MANUAL_INTERVENTION
//	     with an error message explaining the orphaned state.
//
// Instances that already point at a valid saga_definitions row are skipped.
// The function is idempotent: repeated calls on the same data are no-ops.
func BackfillSagaDefinitionIDs(ctx context.Context, db *gorm.DB) (BackfillResult, error) {
	var result BackfillResult
	now := time.Now().UTC()

	// Single transaction so the backfill is atomic per call. CockroachDB's
	// serializable isolation prevents another pod from claiming a saga we're
	// rewriting underneath it.
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Find candidates: active instances whose SagaDefinitionID does NOT yet
		// resolve to a saga_definitions row.
		var candidates []SagaInstance
		findErr := tx.Model(&SagaInstance{}).
			Where("status IN ?", nonTerminalSagaStatuses()).
			Where("NOT EXISTS (SELECT 1 FROM saga_definitions sd WHERE sd.id = saga_instances.saga_definition_id)").
			Find(&candidates).Error
		if findErr != nil {
			return fmt.Errorf("scan candidate instances: %w", findErr)
		}

		for i := range candidates {
			inst := &candidates[i]
			match, matchErr := findLocalDefinitionByNameVersion(tx, inst.SagaName, inst.SagaVersion)
			if matchErr != nil {
				return matchErr
			}
			if match != nil {
				if err := tx.Model(&SagaInstance{}).
					Where("id = ?", inst.ID).
					Updates(map[string]interface{}{
						"saga_definition_id": match.ID,
						"updated_at":         now,
					}).Error; err != nil {
					return fmt.Errorf("link instance %s: %w", inst.ID, err)
				}
				result.Linked++
				continue
			}

			// No matching local definition - flag for manual intervention.
			errMsg := fmt.Sprintf(
				"Backfill failed: no matching saga_definition found for (name=%q, version=%d)",
				inst.SagaName, inst.SagaVersion,
			)
			if err := tx.Model(&SagaInstance{}).
				Where("id = ?", inst.ID).
				Updates(map[string]interface{}{
					"status":        SagaStatusFailedManualIntervention,
					"error_message": errMsg,
					"updated_at":    now,
				}).Error; err != nil {
				return fmt.Errorf("flag instance %s: %w", inst.ID, err)
			}
			result.FlaggedManualIntervention++
		}

		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

// nonTerminalSagaStatuses returns the set of statuses for in-flight sagas that
// may eventually need to resume and therefore require a valid SagaDefinitionID.
// Excludes COMPLETED, COMPENSATED, FAILED, and FAILED_MANUAL_INTERVENTION which
// are terminal and never re-execute.
func nonTerminalSagaStatuses() []SagaStatus {
	return []SagaStatus{
		SagaStatusPending,
		SagaStatusRunning,
		SagaStatusCompensating,
		SagaStatusSuspended,
		SagaStatusWaitingForEvent,
	}
}

// findLocalDefinitionByNameVersion locates a saga_definitions row matching the
// instance's (name, integer version). Returns nil when no row matches.
//
// The local saga_definitions.version column is a varchar (to accept both
// integer reference-data versions and semver platform versions), but the
// SagaInstance.SagaVersion field is an int. This backfill is therefore
// scoped to services whose saga_definitions versions are integer-encoded; the
// semver-versioned platform sagas live in control-plane's separate database
// and never produce saga_instances rows that need backfilling.
//
// We match by string-equality after converting the int to its canonical
// decimal representation.
func findLocalDefinitionByNameVersion(tx *gorm.DB, name string, version int) (*SagaDefinition, error) {
	if name == "" {
		// Instances missing a saga_name cannot be looked up; treat as no match.
		return nil, nil //nolint:nilnil // sentinel: caller flags as manual intervention
	}

	var def SagaDefinition
	versionStr := strconv.Itoa(version)
	err := tx.Where("name = ? AND version = ?", name, versionStr).First(&def).Error
	if err != nil {
		if isGormNotFound(err) {
			return nil, nil //nolint:nilnil // sentinel: no match found
		}
		return nil, fmt.Errorf("lookup local saga definition: %w", err)
	}
	return &def, nil
}

// isGormNotFound returns true for the GORM "no rows" sentinel.
func isGormNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
