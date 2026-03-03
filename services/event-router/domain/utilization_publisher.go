// Package domain contains the core domain models for utilization metering.
package domain

// UtilizationPublisher defines the interface for publishing utilization measurements
// to the Market Data Service (MDS) for aggregation and pricing.
type UtilizationPublisher interface {
	// Publish adds a utilization measurement to the aggregation buffer.
	// The measurement is buffered and periodically flushed to MDS.
	Publish(measurement *UtilizationMeasurement)

	// Stop gracefully stops the publisher, flushing any pending data.
	Stop()
}
