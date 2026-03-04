// Package correlation provides utilities for extracting and tracking correlation IDs
// from Kafka message headers and domain events.
package correlation

import (
	"context"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Source indicates how the correlation ID was obtained.
type Source string

const (
	// SourceEvent means the correlation ID came from DomainEvent.CorrelationID.
	SourceEvent Source = "event"

	// SourceHeader means the correlation ID came from a Kafka message header.
	SourceHeader Source = "header"

	// SourceEventID means the correlation ID fell back to DomainEvent.EventID.
	SourceEventID Source = "event_id"

	// SourceGenerated means a UUID was generated as a last resort.
	SourceGenerated Source = "generated"
)

// Known header keys for correlation ID, checked in priority order.
var correlationIDHeaders = []string{
	"correlation_id",
	"x-correlation-id",
	"X-Correlation-ID",
	"correlationId",
}

// extractionCounter tracks how correlation IDs are being sourced.
var extractionCounter = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "event_router",
		Subsystem: "correlation",
		Name:      "id_extraction_total",
		Help:      "Total number of correlation ID extractions by source.",
	},
	[]string{"source"},
)

// Extractor extracts correlation IDs from domain events and Kafka headers.
type Extractor struct{}

// NewExtractor creates a new correlation ID extractor.
func NewExtractor() *Extractor {
	return &Extractor{}
}

// Extract returns the best correlation ID available, following the fallback chain:
//  1. event.CorrelationID (primary)
//  2. Kafka headers: correlation_id → x-correlation-id → X-Correlation-ID → correlationId
//  3. event.EventID
//  4. Generated UUID (last resort)
//
// The second return value indicates how the correlation ID was obtained.
func (e *Extractor) Extract(_ context.Context, event *eventstream.DomainEvent, headers map[string]string) (string, Source) {
	// Primary: DomainEvent.CorrelationID
	if event != nil && event.CorrelationID != "" {
		extractionCounter.WithLabelValues(string(SourceEvent)).Inc()
		return event.CorrelationID, SourceEvent
	}

	// Fallback 1: Kafka message headers
	for _, key := range correlationIDHeaders {
		if val, ok := headers[key]; ok && val != "" {
			extractionCounter.WithLabelValues(string(SourceHeader)).Inc()
			return val, SourceHeader
		}
	}

	// Fallback 2: DomainEvent.EventID
	if event != nil && event.EventID != "" {
		extractionCounter.WithLabelValues(string(SourceEventID)).Inc()
		return event.EventID, SourceEventID
	}

	// Last resort: generate UUID
	extractionCounter.WithLabelValues(string(SourceGenerated)).Inc()
	return uuid.New().String(), SourceGenerated
}

// ExtractFromMetadata extracts a correlation ID from metadata headers only
// (no DomainEvent). Used when only the metadata map is available (e.g., in proto-based handlers).
//
// Fallback chain:
//  1. Kafka headers: correlation_id → x-correlation-id → X-Correlation-ID → correlationId
//  2. Generated UUID
func ExtractFromMetadata(headers map[string]string) (string, Source) {
	for _, key := range correlationIDHeaders {
		if val, ok := headers[key]; ok && val != "" {
			extractionCounter.WithLabelValues(string(SourceHeader)).Inc()
			return val, SourceHeader
		}
	}

	extractionCounter.WithLabelValues(string(SourceGenerated)).Inc()
	return uuid.New().String(), SourceGenerated
}
