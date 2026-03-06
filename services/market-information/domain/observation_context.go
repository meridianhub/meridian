// Package domain contains the domain models for the Market Information service.
package domain

// ObservationContext carries typed metadata about how a market data observation
// was collected, processed, and qualified. It is stored as JSONB alongside each
// observation record.
//
// The Attributes map holds the key-value pairs that drive CEL resolution-key
// computation and validation (e.g. "base_code"="USD", "quote_code"="EUR").
// Additional fields capture source metadata, collection parameters, and
// processing metadata that are useful for audit and reconciliation.
type ObservationContext struct {
	// Attributes are the user-supplied key-value pairs from the proto
	// AttributeEntry list. They are used by CEL expressions for resolution
	// key computation, validation, and error message generation.
	Attributes map[string]string `json:"attributes,omitempty"`

	// SourceSystem identifies the upstream system that produced the observation
	// (e.g. "bloomberg", "internal-forecast-engine").
	SourceSystem string `json:"source_system,omitempty"`

	// CollectionMethod describes how the data was obtained
	// (e.g. "api-poll", "manual-entry", "streaming-feed").
	CollectionMethod string `json:"collection_method,omitempty"`

	// Unit is the unit of measurement for the observed value
	// (e.g. "USD/oz", "p/kWh", "EUR/MWh").
	Unit string `json:"unit,omitempty"`

	// Notes is free-text annotation attached at ingestion time.
	Notes string `json:"notes,omitempty"`
}

// NewObservationContext creates an ObservationContext from a map of attributes.
// This is the primary constructor used during observation ingestion where
// attributes come from the proto AttributeEntry list.
func NewObservationContext(attributes map[string]string) ObservationContext {
	if attributes == nil {
		attributes = make(map[string]string)
	}
	return ObservationContext{
		Attributes: attributes,
	}
}

// IsEmpty returns true when the context carries no meaningful data.
func (c ObservationContext) IsEmpty() bool {
	return len(c.Attributes) == 0 &&
		c.SourceSystem == "" &&
		c.CollectionMethod == "" &&
		c.Unit == "" &&
		c.Notes == ""
}
