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
	switch {
	case errors.Is(err, domain.ErrObservationNotFound):
		s.logger.Warn("observation not found",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.NotFound, "observation not found: %s", identifier)

	case errors.Is(err, domain.ErrDataSetNotFound):
		s.logger.Warn("dataset not found",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.NotFound, "dataset not found: %s", identifier)

	case errors.Is(err, domain.ErrDataSourceNotFound):
		s.logger.Warn("data source not found",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.NotFound, "data source not found: %s", identifier)

	case errors.Is(err, domain.ErrDataSetDeprecated):
		s.logger.Warn("dataset is deprecated",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.FailedPrecondition, "dataset is deprecated: %s", identifier)

	case errors.Is(err, domain.ErrInvalidTemporalBounds):
		s.logger.Warn("invalid temporal bounds",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.InvalidArgument, "invalid temporal bounds: valid_from must be before valid_to")

	case errors.Is(err, domain.ErrInvalidQualityLevel):
		s.logger.Warn("invalid quality level",
			"operation", operation,
			"identifier", identifier)
		return status.Errorf(codes.InvalidArgument, "invalid quality level")

	case errors.Is(err, domain.ErrDataSetCodeRequired):
		s.logger.Warn("dataset code required",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "dataset code is required")

	case errors.Is(err, domain.ErrSourceIDRequired):
		s.logger.Warn("source ID required",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "source ID is required")

	case errors.Is(err, domain.ErrResolutionKeyRequired):
		s.logger.Warn("resolution key required",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "resolution key is required")

	case errors.Is(err, domain.ErrUnitRequired):
		s.logger.Warn("unit required",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "unit is required")

	case errors.Is(err, domain.ErrCausationIDRequired):
		s.logger.Warn("causation ID required",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "causation ID is required")

	case errors.Is(err, domain.ErrInvalidTrustLevel):
		s.logger.Warn("invalid trust level",
			"operation", operation)
		return status.Errorf(codes.InvalidArgument, "trust level must be between 0 and 100")

	default:
		s.logger.Error("internal error",
			"operation", operation,
			"identifier", identifier,
			"error", err)
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// shouldPublishObservationEvent determines if an observation event should be published.
// Only publish for ACTUAL and VERIFIED quality levels (not ESTIMATE).
func shouldPublishObservationEvent(quality domain.QualityLevel) bool {
	return quality == domain.QualityLevelActual || quality == domain.QualityLevelVerified
}

// protoQualityLevelToDomain converts proto QualityLevel to domain QualityLevel.
func protoQualityLevelToDomain(protoQuality pb.QualityLevel) domain.QualityLevel {
	switch protoQuality {
	case pb.QualityLevel_QUALITY_LEVEL_UNSPECIFIED:
		return domain.QualityLevelEstimate // Default to lowest quality
	case pb.QualityLevel_QUALITY_LEVEL_ESTIMATE:
		return domain.QualityLevelEstimate
	case pb.QualityLevel_QUALITY_LEVEL_PROVISIONAL:
		// Map PROVISIONAL to ESTIMATE (domain doesn't have PROVISIONAL)
		return domain.QualityLevelEstimate
	case pb.QualityLevel_QUALITY_LEVEL_ACTUAL:
		return domain.QualityLevelActual
	case pb.QualityLevel_QUALITY_LEVEL_REVISED:
		// Map REVISED to VERIFIED (corrected values are verified)
		return domain.QualityLevelVerified
	default:
		return domain.QualityLevelEstimate // Default to lowest quality
	}
}

// domainQualityLevelToProto converts domain QualityLevel to proto QualityLevel.
func domainQualityLevelToProto(domainQuality domain.QualityLevel) pb.QualityLevel {
	switch domainQuality {
	case domain.QualityLevelEstimate:
		return pb.QualityLevel_QUALITY_LEVEL_ESTIMATE
	case domain.QualityLevelActual:
		return pb.QualityLevel_QUALITY_LEVEL_ACTUAL
	case domain.QualityLevelVerified:
		// Domain VERIFIED maps to proto ACTUAL (highest reliable quality)
		return pb.QualityLevel_QUALITY_LEVEL_ACTUAL
	default:
		return pb.QualityLevel_QUALITY_LEVEL_UNSPECIFIED
	}
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
