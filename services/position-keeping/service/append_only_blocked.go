package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
)

// UpdatePosition is UNIMPLEMENTED - positions use append-only semantics.
//
// Position updates are forbidden to ensure:
//   - O(1) write performance without locks
//   - Complete audit trail integrity
//   - Simplified concurrent access patterns
//
// To modify a position, clients should:
//  1. Create a new position record with the adjustment amount (positive or negative)
//  2. Read-time aggregation will compute the net position
//
// This method always returns UNIMPLEMENTED status code.
func (s *PositionKeepingService) UpdatePosition(
	_ context.Context,
	_ *positionkeepingv1.UpdatePositionRequest,
) (*positionkeepingv1.UpdatePositionResponse, error) {
	return nil, status.Error(
		codes.Unimplemented,
		"UpdatePosition is not implemented: positions use append-only write semantics. "+
			"To modify a position, create a new position record with the adjustment amount.",
	)
}

// MergePositions is UNIMPLEMENTED - positions use append-only semantics.
//
// Position merging is forbidden at write time to ensure:
//   - O(1) write performance without read-modify-write cycles
//   - Lock-free concurrent inserts
//   - Complete audit trail preservation
//
// Position consolidation is deferred to:
//   - Read-time aggregation (via GetAggregatedPosition)
//   - Background compaction (Phase 2 - future implementation)
//
// This method always returns UNIMPLEMENTED status code.
func (s *PositionKeepingService) MergePositions(
	_ context.Context,
	_ *positionkeepingv1.MergePositionsRequest,
) (*positionkeepingv1.MergePositionsResponse, error) {
	return nil, status.Error(
		codes.Unimplemented,
		"MergePositions is not implemented: positions use append-only write semantics. "+
			"Position consolidation is deferred to read-time aggregation or background compaction.",
	)
}
