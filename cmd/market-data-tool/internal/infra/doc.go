// Package infra provides infrastructure components for the market-data-tool CLI.
//
// Components:
//
//   - GRPCClient: Connection management with context propagation for Market Information Service
//   - BatchInserter: Buffers observations and calls RecordObservationBatch when batch_size reached
//   - CheckpointManagerAdapter: Wraps position-tool's checkpoint manager for import persistence
//   - CELPreview: Non-authoritative CEL validation preview (service is authoritative)
//
// The gRPC client handles tenant context propagation via gRPC metadata, ensuring
// all calls include the appropriate tenant_id header.
//
// CEL validation is intentionally preview-only. The Market Information Service
// performs authoritative validation and may have different CEL evaluation behavior.
// The preview is provided for user feedback during dry-run operations.
package infra
