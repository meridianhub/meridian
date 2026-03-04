package correlation_test

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/services/event-router/internal/correlation"
	"github.com/meridianhub/meridian/services/gateway/eventstream"
	"github.com/stretchr/testify/assert"
)

func TestExtractor_PrimarySource_DomainEventCorrelationID(t *testing.T) {
	e := correlation.NewExtractor()
	event := &eventstream.DomainEvent{
		CorrelationID: "corr-from-event",
		EventID:       "event-id-1",
	}
	headers := map[string]string{
		"correlation_id": "corr-from-header",
	}

	id, src := e.Extract(context.Background(), event, headers)

	assert.Equal(t, "corr-from-event", id)
	assert.Equal(t, correlation.SourceEvent, src)
}

func TestExtractor_FallbackToHeader_WhenEventCorrelationIDEmpty(t *testing.T) {
	e := correlation.NewExtractor()
	event := &eventstream.DomainEvent{
		CorrelationID: "",
		EventID:       "event-id-2",
	}

	tests := []struct {
		name       string
		headerKey  string
		headerVal  string
		wantSource correlation.Source
	}{
		{"correlation_id header", "correlation_id", "corr-1", correlation.SourceHeader},
		{"x-correlation-id header", "x-correlation-id", "corr-2", correlation.SourceHeader},
		{"X-Correlation-ID header", "X-Correlation-ID", "corr-3", correlation.SourceHeader},
		{"correlationId header", "correlationId", "corr-4", correlation.SourceHeader},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{tc.headerKey: tc.headerVal}
			id, src := e.Extract(context.Background(), event, headers)
			assert.Equal(t, tc.headerVal, id)
			assert.Equal(t, tc.wantSource, src)
		})
	}
}

func TestExtractor_HeaderPriority_FirstMatchWins(t *testing.T) {
	e := correlation.NewExtractor()
	event := &eventstream.DomainEvent{CorrelationID: ""}
	// Both correlation_id and x-correlation-id present — correlation_id should win
	headers := map[string]string{
		"correlation_id":   "first-wins",
		"x-correlation-id": "second",
	}

	id, src := e.Extract(context.Background(), event, headers)

	assert.Equal(t, "first-wins", id)
	assert.Equal(t, correlation.SourceHeader, src)
}

func TestExtractor_FallbackToEventID(t *testing.T) {
	e := correlation.NewExtractor()
	event := &eventstream.DomainEvent{
		CorrelationID: "",
		EventID:       "fallback-event-id",
	}
	headers := map[string]string{} // No correlation headers

	id, src := e.Extract(context.Background(), event, headers)

	assert.Equal(t, "fallback-event-id", id)
	assert.Equal(t, correlation.SourceEventID, src)
}

func TestExtractor_FallbackToGenerated_WhenNoEventOrHeaders(t *testing.T) {
	e := correlation.NewExtractor()

	id, src := e.Extract(context.Background(), nil, nil)

	assert.NotEmpty(t, id)
	assert.Equal(t, correlation.SourceGenerated, src)
}

func TestExtractor_FallbackToGenerated_WhenEventEmpty(t *testing.T) {
	e := correlation.NewExtractor()
	event := &eventstream.DomainEvent{
		CorrelationID: "",
		EventID:       "",
	}

	id, src := e.Extract(context.Background(), event, nil)

	assert.NotEmpty(t, id)
	assert.Equal(t, correlation.SourceGenerated, src)
}

func TestExtractFromMetadata_HeaderFound(t *testing.T) {
	headers := map[string]string{
		"x-correlation-id": "meta-corr-id",
	}

	id, src := correlation.ExtractFromMetadata(headers)

	assert.Equal(t, "meta-corr-id", id)
	assert.Equal(t, correlation.SourceHeader, src)
}

func TestExtractFromMetadata_Generated_WhenNoHeaders(t *testing.T) {
	id, src := correlation.ExtractFromMetadata(nil)

	assert.NotEmpty(t, id)
	assert.Equal(t, correlation.SourceGenerated, src)
}

func TestExtractFromMetadata_Generated_WhenHeadersEmpty(t *testing.T) {
	id, src := correlation.ExtractFromMetadata(map[string]string{})

	assert.NotEmpty(t, id)
	assert.Equal(t, correlation.SourceGenerated, src)
}

func TestExtractFromMetadata_SkipsEmptyHeaderValues(t *testing.T) {
	headers := map[string]string{
		"correlation_id":   "",
		"x-correlation-id": "actual-id",
	}

	id, src := correlation.ExtractFromMetadata(headers)

	assert.Equal(t, "actual-id", id)
	assert.Equal(t, correlation.SourceHeader, src)
}
