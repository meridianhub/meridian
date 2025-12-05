package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTracer_ResourceCreation_NoSchemaConflict validates that tracer initialization
// does not fail due to conflicting OpenTelemetry schema versions between dependencies.
//
// This test catches schema version conflicts between:
//   - go.opentelemetry.io/otel (base SDK)
//   - go.opentelemetry.io/contrib/detectors/gcp (GCP resource detector)
//   - GoogleCloudPlatform/opentelemetry-operations-go/detectors/gcp (transitive dependency)
//
// Schema conflicts manifest as errors like:
//
//	"failed to merge resources: conflicting Schema URL: https://opentelemetry.io/schemas/1.37.0 and https://opentelemetry.io/schemas/1.27.0"
//
// This test ensures dependency versions remain compatible during upgrades.
func TestTracer_ResourceCreation_NoSchemaConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	cfg := TracerConfig{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		Enabled:        true,
		OTLPEndpoint:   "localhost:4317",
		SamplingRate:   1.0,
		UseTLS:         false,
	}

	ctx := context.Background()

	// This initialization would fail with schema conflict if dependencies are incompatible
	tracer, err := NewTracer(ctx, cfg)

	// Assert no schema conflict errors occurred during resource creation
	require.NoError(t, err, "tracer initialization should not fail with schema conflicts")
	require.NotNil(t, tracer, "tracer should be initialized")

	// Verify tracer is functional
	assert.NotNil(t, tracer.tracer, "tracer.tracer should be initialized")
	assert.NotNil(t, tracer.provider, "tracer.provider should be initialized")
	assert.Equal(t, cfg.ServiceName, tracer.config.ServiceName)

	// Clean shutdown
	shutdownCtx := context.Background()
	err = tracer.Shutdown(shutdownCtx)
	assert.NoError(t, err, "tracer shutdown should not fail")
}

// TestTracer_NoSchemaConflict_WhenDisabled verifies that disabled tracer
// does not attempt resource creation and thus avoids schema conflicts
func TestTracer_NoSchemaConflict_WhenDisabled(t *testing.T) {
	cfg := TracerConfig{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		Enabled:        false, // Tracing disabled
		OTLPEndpoint:   "localhost:4317",
		SamplingRate:   1.0,
		UseTLS:         false,
	}

	ctx := context.Background()
	tracer, err := NewTracer(ctx, cfg)

	require.NoError(t, err, "disabled tracer initialization should not fail")
	require.NotNil(t, tracer, "tracer should be initialized")
	assert.Nil(t, tracer.provider, "disabled tracer should have nil provider")
}
