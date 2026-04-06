package auth

import "context"

// ActorType identifies the kind of actor performing an operation.
type ActorType string

const (
	// ActorTypeHuman represents an authenticated human user.
	ActorTypeHuman ActorType = "human"
	// ActorTypeScheduler represents a scheduled job (e.g. billing cron, catch-up runner).
	ActorTypeScheduler ActorType = "scheduler"
	// ActorTypeWorker represents a background worker process.
	ActorTypeWorker ActorType = "worker"
	// ActorTypeMigration represents a data migration process.
	ActorTypeMigration ActorType = "migration"
)

// Actor identifies who is performing an operation and how that identity was established.
//
// The Authenticated field is set to true only by the gRPC auth interceptor. Scheduler
// and worker code sets it to false, creating a clear security boundary: callers cannot
// escalate trust by constructing an Actor themselves.
type Actor struct {
	// ID is the actor's identifier, e.g. a user UUID or "system:scheduler:billing".
	ID string
	// Type classifies the actor.
	Type ActorType
	// Authenticated is true only when the Actor was set by the auth interceptor.
	// Scheduler, worker, and migration actors must leave this false.
	Authenticated bool
	// Source describes the mechanism that placed this Actor in context,
	// e.g. "grpc-interceptor", "cron-scheduler", "catch-up".
	Source string
}

// actorContextKey is a separate unexported type so it cannot collide with
// contextKey (used for UserIDContextKey etc.) even though both are string-typed.
type actorContextKey struct{}

// WithActor returns a new context carrying the given Actor.
func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorContextKey{}, actor)
}

// ActorFromContext retrieves the Actor stored in ctx.
// The second return value is false when no Actor has been set.
func ActorFromContext(ctx context.Context) (Actor, bool) {
	actor, ok := ctx.Value(actorContextKey{}).(Actor)
	return actor, ok
}
