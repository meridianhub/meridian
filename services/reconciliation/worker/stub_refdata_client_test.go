package worker

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestStubReferenceDataClient_ReturnsEmptyList(t *testing.T) {
	client := NewStubReferenceDataClient(newTestLogger())

	schedules, err := client.ListSettlementSchedules(context.Background())
	require.NoError(t, err)
	assert.Empty(t, schedules)
}

func TestStubReferenceDataClient_ImplementsInterface(t *testing.T) {
	var client ReferenceDataClient = NewStubReferenceDataClient(newTestLogger())
	assert.NotNil(t, client)
}
