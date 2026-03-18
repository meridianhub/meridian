// Package saga provides saga orchestration runtime and persistence for durable execution.
//
// A saga is a sequence of local transactions that together form a distributed workflow.
// Each step declares a forward action and a compensating action; if any step fails,
// completed steps are compensated in reverse order (LIFO), restoring consistency
// without distributed locking.
//
// # Key Types
//
//   - [SagaInstance]: persistent record of a running or completed saga
//   - [SagaStepResult]: record of a single step within a saga
//   - [CausationTreeNode]: hierarchical view of nested sagas spawned by a parent
//
// All sagas are persisted to the database before execution begins, enabling
// resumption after service restarts and full audit-trail reconstruction.
package saga
