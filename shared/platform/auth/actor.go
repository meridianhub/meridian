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
// Security model: Authenticated must only be set to true by the gRPC auth interceptor,
// which is the sole authority that can verify a JWT and confirm the actor identity.
// All other code paths (schedulers, workers, migrations, and any constructor that builds
// an Actor from external/untrusted input) must leave Authenticated as false. Never copy
// an incoming Authenticated value from external data (e.g., proto messages, HTTP headers,
// or request bodies) - always default to false and let the interceptor set it.
type Actor struct {
	// ID is the actor's identifier, e.g. a user UUID or "system:scheduler:billing".
	ID string
	// Type classifies the actor.
	Type ActorType
	// Authenticated is true only when set by the gRPC auth interceptor after JWT
	// verification. All other callers must leave this false.
	Authenticated bool
	// Source describes the mechanism that placed this Actor in context,
	// e.g. "grpc-interceptor", "cron-scheduler", "catch-up".
	Source string
}

// actorContextKey is an unexported struct type. Using a distinct unexported type
// (rather than a string alias) guarantees no collision with other context key types
// such as contextKey (used for UserIDContextKey), even if they were to hold the
// same underlying value.
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
