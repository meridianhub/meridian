// Package eventstream provides the hexagonal port interfaces and core domain types
// for real-time event streaming in the Meridian gateway service.
//
// # Architecture
//
// The package follows Meridian's hexagonal architecture pattern:
//
//   - Ports (ports.go): Abstract interfaces that define what the system needs from
//     external adapters (EventSource) and what internal components expose (FanOut).
//   - Domain (domain.go): Core value types (DomainEvent, Subscription, ChannelPattern)
//     that flow through the system without infrastructure dependencies.
//
// # Key Types
//
// EventSource is the inbound port — it abstracts event ingestion from Kafka or other
// messaging systems. Call Start to begin consuming events; the provided EventHandler
// is invoked for each received DomainEvent.
//
// FanOut is the distribution port — it coordinates real-time delivery of events across
// gateway instances. Tenants subscribe via Subscribe; incoming events are forwarded
// to matching subscribers via Publish.
//
// DomainEvent carries the canonical event envelope used throughout the streaming
// pipeline. Payload is JSON-encoded so that gateway adapters remain format-agnostic.
//
// # Error Handling
//
// All sentinel errors are defined in this package and can be compared with errors.Is.
package eventstream
