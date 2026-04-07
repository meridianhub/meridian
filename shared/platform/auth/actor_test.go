package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActorFromContext_RoundTrip(t *testing.T) {
	actor := Actor{
		ID:            "user-uuid-123",
		Type:          ActorTypeHuman,
		Authenticated: true,
		Source:        "grpc-interceptor",
	}

	ctx := WithActor(context.Background(), actor)

	got, ok := ActorFromContext(ctx)

	require.True(t, ok)
	assert.Equal(t, actor, got)
}

func TestActorFromContext_Missing(t *testing.T) {
	_, ok := ActorFromContext(context.Background())
	assert.False(t, ok)
}

func TestActorFromContext_ActorTypes(t *testing.T) {
	cases := []struct {
		actorType ActorType
		id        string
		source    string
		auth      bool
	}{
		{ActorTypeHuman, "user-uuid-abc", "grpc-interceptor", true},
		{ActorTypeScheduler, "system:scheduler:billing", "cron-scheduler", false},
		{ActorTypeWorker, "system:worker:settlement", "background-worker", false},
		{ActorTypeMigration, "system:migration:v42", "catch-up", false},
	}

	for _, tc := range cases {
		t.Run(string(tc.actorType), func(t *testing.T) {
			actor := Actor{
				ID:            tc.id,
				Type:          tc.actorType,
				Authenticated: tc.auth,
				Source:        tc.source,
			}

			ctx := WithActor(context.Background(), actor)
			got, ok := ActorFromContext(ctx)

			require.True(t, ok)
			assert.Equal(t, actor, got)
		})
	}
}

func TestActorContextKey_DoesNotCollideWithUserIDContextKey(t *testing.T) {
	// Storing an Actor must not interfere with UserIDContextKey lookups and vice-versa.
	actor := Actor{
		ID:     "user-uuid-456",
		Type:   ActorTypeHuman,
		Source: "grpc-interceptor",
	}

	ctx := WithActor(context.Background(), actor)
	ctx = context.WithValue(ctx, UserIDContextKey, "different-user-id")

	gotActor, ok := ActorFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, actor, gotActor)

	gotUserID, ok := ctx.Value(UserIDContextKey).(string)
	require.True(t, ok)
	assert.Equal(t, "different-user-id", gotUserID)
}
