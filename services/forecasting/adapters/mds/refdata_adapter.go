package mds

import (
	"context"
	"errors"
	"fmt"

	"github.com/meridianhub/meridian/services/forecasting/starlark"
)

// ErrRefDataNotConfigured indicates that no reference data service is available.
var ErrRefDataNotConfigured = errors.New("reference data service not configured")

// NoOpRefDataClient is a RefDataClient that always returns an error, used when
// reference data lookups are not configured for a strategy.
type NoOpRefDataClient struct{}

// GetNodeByResolutionKey returns an error since no ref data service is configured.
func (n *NoOpRefDataClient) GetNodeByResolutionKey(_ context.Context, _, resolutionKey string) (*starlark.ReferenceData, error) {
	return nil, fmt.Errorf("%w: cannot resolve key %q", ErrRefDataNotConfigured, resolutionKey)
}
