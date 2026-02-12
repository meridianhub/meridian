package worker

import (
	"context"
	"log/slog"
)

// StubReferenceDataClient is a temporary implementation of ReferenceDataClient
// that returns an empty schedule list. It will be replaced once the Reference Data
// service proto definitions are available and a gRPC adapter can be created.
type StubReferenceDataClient struct {
	logger *slog.Logger
}

// NewStubReferenceDataClient creates a new stub reference data client.
func NewStubReferenceDataClient(logger *slog.Logger) *StubReferenceDataClient {
	return &StubReferenceDataClient{logger: logger}
}

// ListSettlementSchedules returns an empty list. The scheduler will pick up
// real schedules once the Reference Data gRPC client is wired in.
func (c *StubReferenceDataClient) ListSettlementSchedules(_ context.Context) ([]SettlementSchedule, error) {
	c.logger.Debug("stub reference data client: returning empty schedule list")
	return nil, nil
}
