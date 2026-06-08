// Package service provides gRPC service implementations for the Market Information service.
// BIAN Service Domain: Market Information Management
//
// This file contains shared helpers for observation operations: error mapping,
// quality level conversion, and proto conversion utilities.
package service

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

// mapObservationDomainError converts domain errors to appropriate gRPC status codes.
func (s *Server) mapObservationDomainError(err error, operation, identifier string) error {
	// Check lookup errors (NotFound, FailedPrecondition)
	if grpcErr := mapObservationLookupError(err, identifier); grpcErr != nil {
		s.logger.Warn("observation domain error",
			"operation", operation,
			"identifier", identifier,
			"error", err)
		return grpcErr
	}

	// Check validation errors (InvalidArgument)
	if grpcErr := mapObservationValidationError(err); grpcErr != nil {
		s.logger.Warn("observation validation error",
			"operation", operation,
			"identifier", identifier,
			"error", err)
		return grpcErr
	}

	s.logger.Error("internal error",
		"operation", operation,
		"identifier", identifier,
		"error", err)
	return status.Errorf(codes.Internal, "internal error: %v", err)
}

// mapObservationLookupError maps observation lookup/state errors to gRPC status codes.
// Returns nil if the error does not match any known lookup error.
func mapObservationLookupError(err error, identifier string) error {
	switch {
	case errors.Is(err, domain.ErrObservationNotFound):
		return status.Errorf(codes.NotFound, "observation not found: %s", identifier)
	case errors.Is(err, domain.ErrDataSetNotFound):
		return status.Errorf(codes.NotFound, "dataset not found: %s", identifier)
	case errors.Is(err, domain.ErrDataSourceNotFound):
		return status.Errorf(codes.NotFound, "data source not found: %s", identifier)
	case errors.Is(err, domain.ErrDataSetDeprecated):
		return status.Errorf(codes.FailedPrecondition, "dataset is deprecated: %s", identifier)
	case errors.Is(err, domain.ErrInvalidTemporalBounds):
		return status.Errorf(codes.InvalidArgument, "invalid temporal bounds: valid_from must be before valid_to")
	case errors.Is(err, domain.ErrInvalidQualityLevel):
		return status.Errorf(codes.InvalidArgument, "invalid quality level")
	default:
		return nil
	}
}

// mapObservationValidationError maps observation field validation errors to gRPC status codes.
// Returns nil if the error does not match any known validation error.
func mapObservationValidationError(err error) error {
	switch {
	case errors.Is(err, domain.ErrDataSetCodeRequired):
		return status.Errorf(codes.InvalidArgument, "dataset code is required")
	case errors.Is(err, domain.ErrSourceIDRequired):
		return status.Errorf(codes.InvalidArgument, "source ID is required")
	case errors.Is(err, domain.ErrResolutionKeyRequired):
		return status.Errorf(codes.InvalidArgument, "resolution key is required")
	case errors.Is(err, domain.ErrUnitRequired):
		return status.Errorf(codes.InvalidArgument, "unit is required")
	case errors.Is(err, domain.ErrCausationIDRequired):
		return status.Errorf(codes.InvalidArgument, "causation ID is required")
	case errors.Is(err, domain.ErrInvalidTrustLevel):
		return status.Errorf(codes.InvalidArgument, "trust level must be between 0 and 100")
	default:
		return nil
	}
}

// shouldPublishObservationEvent determines if an observation event should be published.
// Only publish for ACTUAL and VERIFIED quality levels (not ESTIMATE).
func shouldPublishObservationEvent(quality domain.QualityLevel) bool {
	return quality == domain.QualityLevelActual || quality == domain.QualityLevelVerified
}

// Proto<->domain quality mapping (Axis A, confidence) per ADR-0017.
//
// The mapping is LOSSLESS: every domain level round-trips through proto back to
// itself. The four confidence grades line up one-to-one with the proto enum:
//
//	ESTIMATE     <-> QUALITY_LEVEL_ESTIMATE    (1)
//	PROVISIONAL  <-> QUALITY_LEVEL_PROVISIONAL (2)
//	ACTUAL       <-> QUALITY_LEVEL_ACTUAL      (3)
//	VERIFIED     <-> QUALITY_LEVEL_REVISED     (4)
//
// Proto slot 4 is still spelled QUALITY_LEVEL_REVISED, but its doc comment
// redefines it to VERIFIED confidence semantics; no QUALITY_LEVEL_VERIFIED symbol
// exists yet. Slot 4 is therefore the only level-4 symbol available, so VERIFIED
// maps onto it (and back) until the symbol rename/reserve lands (task 14).
//
// Mapping is by confidence grade only. Lifecycle/correction state (Axis B) is the
// server-assigned revision field, independent of this enum, so the mappers never
// read or set revision.
var (
	protoToDomainQuality = map[pb.QualityLevel]domain.QualityLevel{
		pb.QualityLevel_QUALITY_LEVEL_UNSPECIFIED: domain.QualityLevelEstimate,
		pb.QualityLevel_QUALITY_LEVEL_ESTIMATE:    domain.QualityLevelEstimate,
		pb.QualityLevel_QUALITY_LEVEL_PROVISIONAL: domain.QualityLevelProvisional,
		pb.QualityLevel_QUALITY_LEVEL_ACTUAL:      domain.QualityLevelActual,
		pb.QualityLevel_QUALITY_LEVEL_REVISED:     domain.QualityLevelVerified, // slot 4 = VERIFIED-semantically (rename pending task 14)
	}

	domainToProtoQuality = map[domain.QualityLevel]pb.QualityLevel{
		domain.QualityLevelEstimate:    pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
		domain.QualityLevelProvisional: pb.QualityLevel_QUALITY_LEVEL_PROVISIONAL,
		domain.QualityLevelActual:      pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
		domain.QualityLevelVerified:    pb.QualityLevel_QUALITY_LEVEL_REVISED, // slot 4 is the only level-4 symbol until task 14 renames it
	}
)

// protoQualityLevelToDomain converts proto QualityLevel to domain QualityLevel.
// Unrecognized or UNSPECIFIED values default to the lowest confidence (ESTIMATE).
func protoQualityLevelToDomain(protoQuality pb.QualityLevel) domain.QualityLevel {
	if level, ok := protoToDomainQuality[protoQuality]; ok {
		return level
	}
	return domain.QualityLevelEstimate // Default to lowest confidence
}

// domainQualityLevelToProto converts domain QualityLevel to proto QualityLevel.
// Unrecognized values map to UNSPECIFIED.
func domainQualityLevelToProto(domainQuality domain.QualityLevel) pb.QualityLevel {
	if level, ok := domainToProtoQuality[domainQuality]; ok {
		return level
	}
	return pb.QualityLevel_QUALITY_LEVEL_UNSPECIFIED
}

// domainObservationToProto converts a domain MarketPriceObservation to proto.
// The datasetVersion parameter should be obtained from the dataset definition.
// If the version cannot be determined, pass 1 as a fallback.
func domainObservationToProto(obs domain.MarketPriceObservation, attributes []*quantityv1.AttributeEntry, datasetVersion int) *pb.MarketPriceObservation {
	pbObs := &pb.MarketPriceObservation{
		Id:                 obs.ID().String(),
		DatasetCode:        obs.DataSetCode(),
		DatasetVersion:     int32(datasetVersion),
		ResolutionKeyValue: obs.ResolutionKey(),
		ObservedAt:         timestamppb.New(obs.ObservedAt()),
		ValidFrom:          timestamppb.New(obs.ValidFrom()),
		ValidTo:            timestamppb.New(obs.ValidTo()),
		Value:              obs.Value().String(),
		Quality:            domainQualityLevelToProto(obs.QualityLevel()),
		SourceId:           obs.SourceID().String(),
		CreatedAt:          timestamppb.New(obs.CreatedAt()),
		Attributes:         attributes,
	}

	// Set optional superseded fields
	if obs.SupersededAt() != nil {
		pbObs.SupersededAt = timestamppb.New(*obs.SupersededAt())
	}
	if obs.SupersededBy() != nil {
		pbObs.SupersededById = obs.SupersededBy().String()
	}

	return pbObs
}
