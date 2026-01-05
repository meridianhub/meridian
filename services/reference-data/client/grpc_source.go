// Package client provides a gRPC client for the Reference Data service with
// integrated tiered caching for high-performance instrument lookups.
package client

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/services/reference-data/registry"
)

// GRPCSource implements cache.Source by fetching instrument definitions
// from the Reference Data Service via gRPC. This serves as the L3 (source of truth)
// layer in the tiered cache hierarchy.
type GRPCSource struct {
	client referencedatav1.ReferenceDataServiceClient
}

// Verify GRPCSource implements cache.Source.
var _ cache.Source = (*GRPCSource)(nil)

// NewGRPCSource creates a new gRPC-backed source for instrument definitions.
func NewGRPCSource(client referencedatav1.ReferenceDataServiceClient) *GRPCSource {
	return &GRPCSource{
		client: client,
	}
}

// GetDefinition retrieves an instrument definition from the Reference Data Service.
// Returns registry.ErrNotFound if the instrument doesn't exist.
func (s *GRPCSource) GetDefinition(ctx context.Context, code string, version int) (*registry.InstrumentDefinition, error) {
	resp, err := s.client.RetrieveInstrument(ctx, &referencedatav1.RetrieveInstrumentRequest{
		Code:    code,
		Version: int32(version),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil, registry.ErrNotFound
		}
		return nil, fmt.Errorf("grpc retrieve instrument: %w", err)
	}

	return protoToDefinition(resp.Instrument), nil
}

// protoToDefinition converts a protobuf InstrumentDefinition to the domain model.
func protoToDefinition(pb *referencedatav1.InstrumentDefinition) *registry.InstrumentDefinition {
	if pb == nil {
		return nil
	}

	def := &registry.InstrumentDefinition{
		Code:                     pb.Code,
		Version:                  int(pb.Version),
		Dimension:                dimensionFromProto(pb.Dimension),
		Precision:                int(pb.Precision),
		Status:                   statusFromProto(pb.Status),
		IsSystem:                 pb.IsSystem,
		ValidationExpression:     pb.ValidationExpression,
		FungibilityKeyExpression: pb.FungibilityKeyExpression,
		ErrorMessageExpression:   pb.ErrorMessageExpression,
		AttributeSchema:          []byte(pb.AttributeSchema),
		DisplayName:              pb.DisplayName,
		Description:              pb.Description,
	}

	// Parse UUID
	if pb.Id != "" {
		if id, err := uuid.Parse(pb.Id); err == nil {
			def.ID = id
		}
	}

	// Parse timestamps
	if pb.CreatedAt != nil {
		def.CreatedAt = pb.CreatedAt.AsTime()
	}

	if pb.ActivatedAt != nil {
		t := pb.ActivatedAt.AsTime()
		def.ActivatedAt = &t
	}

	if pb.DeprecatedAt != nil {
		t := pb.DeprecatedAt.AsTime()
		def.DeprecatedAt = &t
	}

	// Parse successor ID if present
	if pb.SuccessorId != "" {
		if successorID, err := uuid.Parse(pb.SuccessorId); err == nil {
			def.SuccessorID = &successorID
		}
	}

	return def
}

// dimensionFromProto converts protobuf Dimension to domain Dimension.
func dimensionFromProto(d referencedatav1.Dimension) registry.Dimension {
	switch d {
	case referencedatav1.Dimension_DIMENSION_CURRENCY:
		return registry.DimensionMonetary
	case referencedatav1.Dimension_DIMENSION_ENERGY:
		return registry.DimensionEnergy
	case referencedatav1.Dimension_DIMENSION_MASS:
		return registry.DimensionMass
	case referencedatav1.Dimension_DIMENSION_VOLUME:
		return registry.DimensionVolume
	case referencedatav1.Dimension_DIMENSION_TIME:
		return registry.DimensionTime
	case referencedatav1.Dimension_DIMENSION_COMPUTE:
		return registry.DimensionCompute
	case referencedatav1.Dimension_DIMENSION_COUNT:
		return registry.DimensionQuantity
	case referencedatav1.Dimension_DIMENSION_UNSPECIFIED,
		referencedatav1.Dimension_DIMENSION_CARBON,
		referencedatav1.Dimension_DIMENSION_DATA:
		return ""
	default:
		return ""
	}
}

// statusFromProto converts protobuf InstrumentStatus to domain Status.
func statusFromProto(s referencedatav1.InstrumentStatus) registry.Status {
	switch s {
	case referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT:
		return registry.StatusDraft
	case referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE:
		return registry.StatusActive
	case referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED:
		return registry.StatusDeprecated
	case referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}
