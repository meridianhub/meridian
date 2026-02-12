package worker

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStubReferenceDataClient_ReturnsEmptyList(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewStubReferenceDataClient(logger)

	schedules, err := client.ListSettlementSchedules(context.Background())
	require.NoError(t, err)
	assert.Empty(t, schedules)
}

func TestStubReferenceDataClient_ImplementsInterface(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	var client ReferenceDataClient = NewStubReferenceDataClient(logger)
	assert.NotNil(t, client)
}
