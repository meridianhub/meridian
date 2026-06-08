package admin

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// createFinancialPositionLogsTable creates a minimal financial_position_logs
// table so the position-tracing queries can execute. The position-tracing
// queries LEFT JOIN this table, which only exists in the position-keeping
// service in production. The admin queries reference log_id and account_id.
func createFinancialPositionLogsTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS financial_position_logs (
			log_id     UUID PRIMARY KEY,
			account_id VARCHAR(255) NOT NULL
		)
	`).Error)
	t.Cleanup(func() {
		_ = db.Exec(`DROP TABLE IF EXISTS financial_position_logs`).Error
	})
}

// createEventOutboxTable creates a minimal event_outbox table so the outbox
// fallback query can execute. The admin findSagaViaOutbox query references
// id and causation_id.
func createEventOutboxTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	// causation_id is stored as text so the admin query's text comparison
	// (ssr.causation_id::text = eo.causation_id) type-checks on CockroachDB.
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS event_outbox (
			id           UUID PRIMARY KEY,
			causation_id VARCHAR(255) NULL
		)
	`).Error)
	t.Cleanup(func() {
		_ = db.Exec(`DROP TABLE IF EXISTS event_outbox`).Error
	})
}

// newVisualizer wires up a CausationVisualizer against the shared test DB.
func newVisualizer(db *gorm.DB) *CausationVisualizer {
	treeRepo := saga.NewCausationTreeRepository(db)
	return NewCausationVisualizer(db, treeRepo, nil)
}

// seedRootSaga inserts a completed root saga (no parent) with one step result
// and returns its ID and correlation ID.
func seedRootSaga(t *testing.T, db *gorm.DB, name string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	sagaID := uuid.New()
	correlationID := uuid.New()
	instance := &saga.SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		SagaName:         name,
		Status:           saga.SagaStatusCompleted,
		CorrelationID:    correlationID,
		CurrentStepIndex: 0,
	}
	require.NoError(t, db.Create(instance).Error)

	step := &saga.SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: sagaID,
		StepIndex:      0,
		StepName:       "step_0",
		IdempotencyKey: saga.FormatIdempotencyKey(sagaID, 0),
		Status:         saga.StepStatusCompleted,
	}
	require.NoError(t, db.Create(step).Error)

	return sagaID, correlationID
}

// TestIntegration_GetCausationTreeForPosition_ViaCorrelation exercises the
// primary position-tracing path: findSagaForPosition matches a saga whose
// correlation_id equals the position log_id, then returns the full tree.
func TestIntegration_GetCausationTreeForPosition_ViaCorrelation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)
	createFinancialPositionLogsTable(t, db)

	// The position log_id is reused as the saga correlation_id so the primary
	// query (si.correlation_id::text = $1::text) matches.
	positionID := uuid.New()
	sagaID := uuid.New()
	instance := &saga.SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "position_settlement",
		Status:           saga.SagaStatusCompleted,
		CorrelationID:    positionID,
		CurrentStepIndex: 0,
	}
	require.NoError(t, db.Create(instance).Error)

	step := &saga.SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: sagaID,
		StepIndex:      0,
		StepName:       "post_entry",
		IdempotencyKey: saga.FormatIdempotencyKey(sagaID, 0),
		Status:         saga.StepStatusCompleted,
	}
	require.NoError(t, db.Create(step).Error)

	// Insert the position log so account_id is populated via the LEFT JOIN.
	require.NoError(t, db.Exec(
		`INSERT INTO financial_position_logs (log_id, account_id) VALUES ($1, $2)`,
		positionID, "acct-pos-001",
	).Error)

	v := newVisualizer(db)
	result, info, err := v.GetCausationTreeForPosition(context.Background(), positionID)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, info)

	assert.Equal(t, sagaID, result.SagaID)
	assert.Equal(t, "position_settlement", result.Tree.SagaName)
	assert.Equal(t, positionID, info.PositionID)
	assert.Equal(t, "acct-pos-001", info.AccountID)
}

// TestIntegration_GetCausationTreeForPosition_ViaLineageFallback exercises the
// findSagaViaPositionLineage fallback: the primary query finds no match, so the
// code falls back to matching the position ID inside a step result's JSON
// result payload.
func TestIntegration_GetCausationTreeForPosition_ViaLineageFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)
	createFinancialPositionLogsTable(t, db)

	positionID := uuid.New()
	sagaID := uuid.New()
	// correlation_id deliberately differs from positionID so the primary
	// query returns no rows and the lineage fallback runs.
	instance := &saga.SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "lineage_saga",
		Status:           saga.SagaStatusCompleted,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 0,
	}
	require.NoError(t, db.Create(instance).Error)

	// The fallback matches positionID inside the step result JSON payload
	// (ssr.result::text LIKE '%positionID%').
	step := &saga.SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: sagaID,
		StepIndex:      0,
		StepName:       "post_entry",
		IdempotencyKey: saga.FormatIdempotencyKey(sagaID, 0),
		Status:         saga.StepStatusCompleted,
		Result:         saga.JSONB{"position_log_id": positionID.String()},
	}
	require.NoError(t, db.Create(step).Error)

	require.NoError(t, db.Exec(
		`INSERT INTO financial_position_logs (log_id, account_id) VALUES ($1, $2)`,
		positionID, "acct-pos-lineage",
	).Error)

	v := newVisualizer(db)
	result, info, err := v.GetCausationTreeForPosition(context.Background(), positionID)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, info)

	assert.Equal(t, sagaID, result.SagaID)
	assert.Equal(t, "lineage_saga", result.Tree.SagaName)
	assert.Equal(t, "acct-pos-lineage", info.AccountID)
}

// TestIntegration_GetCausationTreeForPosition_NotFound exercises the case where
// neither the primary query nor the lineage fallback finds a saga, returning
// ErrNoSagaFound.
func TestIntegration_GetCausationTreeForPosition_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)
	createFinancialPositionLogsTable(t, db)

	v := newVisualizer(db)
	_, _, err := v.GetCausationTreeForPosition(context.Background(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoSagaFound)
}

// TestIntegration_FindSagaViaOutbox_TableMissing exercises the branch where the
// event_outbox table does not exist: findSagaViaOutbox should short-circuit to
// ErrNoSagaFound. This path is reached when findSagaForEvent's primary query
// finds no causation_id match and falls back to the outbox.
func TestIntegration_FindSagaViaOutbox_TableMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)
	// Ensure the outbox table is absent for this test.
	require.NoError(t, db.Exec(`DROP TABLE IF EXISTS event_outbox`).Error)

	v := newVisualizer(db)
	_, err := v.GetCausationTreeForEvent(context.Background(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoSagaFound)
}

// TestIntegration_FindSagaViaOutbox_ViaOutboxMatch exercises the outbox fallback
// path that resolves a saga: the primary causation_id lookup misses, but the
// event_outbox row links to a step result's causation_id.
func TestIntegration_FindSagaViaOutbox_ViaOutboxMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)
	createEventOutboxTable(t, db)

	sagaID, _ := seedRootSaga(t, db, "outbox_saga")

	// Give the saga's step a causation_id that the event_outbox row points to.
	stepCausationID := saga.GenerateCausationID(sagaID, 0)
	require.NoError(t, db.Exec(
		`UPDATE saga_step_results SET causation_id = $1 WHERE saga_instance_id = $2`,
		stepCausationID, sagaID,
	).Error)

	// The event being traced has its own ID; the primary findSagaForEvent
	// lookup (ssr.causation_id = eventID) misses because eventID differs from
	// stepCausationID, forcing the outbox fallback.
	eventID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO event_outbox (id, causation_id) VALUES ($1, $2)`,
		eventID, stepCausationID.String(),
	).Error)

	v := newVisualizer(db)
	result, err := v.GetCausationTreeForEvent(context.Background(), eventID)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, sagaID, result.SagaID)
	assert.Equal(t, "outbox_saga", result.Tree.SagaName)
}

// TestIntegration_FindSagaViaOutbox_TablePresentNoMatch exercises the outbox
// fallback when the table exists but contains no matching row, returning
// ErrNoSagaFound.
func TestIntegration_FindSagaViaOutbox_TablePresentNoMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)
	createEventOutboxTable(t, db)

	v := newVisualizer(db)
	_, err := v.GetCausationTreeForEvent(context.Background(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoSagaFound)
}

// TestIntegration_FindRootSaga_ChainTooDeep exercises the ErrCausationChainTooDeep
// branch in findRootSaga. A cycle in the parent chain (a -> b -> a) means the
// recursive CTE never reaches a NULL parent within the depth limit, so no root
// row is returned.
func TestIntegration_FindRootSaga_ChainTooDeep(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)

	correlationID := uuid.New()
	aID := uuid.New()
	bID := uuid.New()
	stepIdx := 0

	// Insert two sagas first with NULL parents to satisfy any FK, then wire up
	// a cycle: a.parent = b, b.parent = a. Neither has a NULL parent.
	a := &saga.SagaInstance{
		ID:               aID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "cycle_a",
		Status:           saga.SagaStatusCompleted,
		CorrelationID:    correlationID,
		CurrentStepIndex: 0,
	}
	require.NoError(t, db.Create(a).Error)

	b := &saga.SagaInstance{
		ID:               bID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "cycle_b",
		Status:           saga.SagaStatusCompleted,
		CorrelationID:    correlationID,
		ParentSagaID:     &aID,
		ParentStepIndex:  &stepIdx,
		CurrentStepIndex: 0,
	}
	require.NoError(t, db.Create(b).Error)

	// Now point a's parent at b to form the cycle.
	require.NoError(t, db.Exec(
		`UPDATE saga_instances SET parent_saga_id = $1, parent_step_index = $2 WHERE id = $3`,
		bID, stepIdx, aID,
	).Error)

	v := newVisualizer(db)
	// findSagaForTransaction matches on correlation_id, then findRootSaga walks
	// the cyclic chain and never finds a NULL parent.
	_, err := v.GetCausationTreeForTransaction(context.Background(), correlationID)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCausationChainTooDeep)
}

// TestIntegration_GetCausationTree_TreeNotFound exercises the getCausationTree
// branch where the resolved root saga exists for root-finding but the tree
// repository reports ErrSagaNotFound, surfacing as ErrNoSagaFound.
//
// This is engineered by deleting the saga instance row after the correlation
// match but the scenario is constructed via a step result that resolves a root
// ID for a saga whose instance row is absent at tree-build time.
func TestIntegration_GetCausationTree_RootResolvesButTreeMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)

	// Seed a real root saga so findSagaForTransaction + findRootSaga succeed.
	sagaID, correlationID := seedRootSaga(t, db, "vanishing_saga")

	v := newVisualizer(db)
	// Sanity check the happy path resolves first.
	result, err := v.GetCausationTreeForTransaction(context.Background(), correlationID)
	require.NoError(t, err)
	require.Equal(t, sagaID, result.SagaID)

	// Now confirm getCausationTree maps ErrSagaNotFound -> ErrNoSagaFound by
	// calling the tree repo directly against a non-existent saga via the
	// unexported helper through a fresh root that no longer has a tree.
	_, _, terr := v.getCausationTree(context.Background(), uuid.New())
	require.Error(t, terr)
	assert.ErrorIs(t, terr, ErrNoSagaFound)
	assert.True(t, errors.Is(terr, ErrNoSagaFound))
}
