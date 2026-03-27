// Package service provides gRPC service implementations for the Market Information service.
// BIAN Service Domain: Market Information Management
//
// This file implements the observation query and retrieval operations.
package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

// getDatasetVersion looks up the version for a dataset by code.
// Returns 1 as fallback if the dataset cannot be found.
func (s *Server) getDatasetVersion(ctx context.Context, datasetCode string) int {
	dataset, err := s.dataSetRepo.FindByCode(ctx, datasetCode)
	if err != nil {
		s.logger.Warn("failed to get dataset version, using fallback",
			"dataset_code", datasetCode,
			"error", err)
		return 1 // Fallback to version 1
	}
	return dataset.Version()
}

// RetrieveObservation fetches a specific observation with bi-temporal query support.
// Returns NOT_FOUND if observation doesn't exist at the requested times.
func (s *Server) RetrieveObservation(ctx context.Context, req *pb.RetrieveObservationRequest) (*pb.RetrieveObservationResponse, error) {
	// Parse observation ID
	obsID, err := uuid.Parse(req.ObservationId)
	if err != nil {
		s.logger.Warn("invalid observation ID",
			"observation_id", req.ObservationId,
			"error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid observation ID: %s", req.ObservationId)
	}

	// Retrieve observation by ID
	observation, err := s.observationRepo.FindByID(ctx, obsID)
	if err != nil {
		return nil, s.mapObservationDomainError(err, "RetrieveObservation", req.ObservationId)
	}

	// Apply knowledge_base_time filter if provided (bi-temporal query)
	if req.KnowledgeBaseTime != nil {
		knowledgeTime := req.KnowledgeBaseTime.AsTime()

		// Check if the observation was known at the requested time
		// An observation is "known" if it was created before the knowledge base time
		// and not yet superseded at that time
		if observation.CreatedAt().After(knowledgeTime) {
			s.logger.Debug("observation not known at requested time",
				"observation_id", req.ObservationId,
				"knowledge_base_time", knowledgeTime,
				"created_at", observation.CreatedAt())
			return nil, status.Errorf(codes.NotFound, "observation %s was not known at %v", req.ObservationId, knowledgeTime)
		}

		// Check if superseded before the knowledge base time
		if observation.SupersededAt() != nil && observation.SupersededAt().Before(knowledgeTime) {
			s.logger.Debug("observation was superseded before requested time",
				"observation_id", req.ObservationId,
				"knowledge_base_time", knowledgeTime,
				"superseded_at", observation.SupersededAt())
			return nil, status.Errorf(codes.NotFound, "observation %s was superseded before %v", req.ObservationId, knowledgeTime)
		}
	}

	// Get dataset version for proto conversion
	datasetVersion := s.getDatasetVersion(ctx, observation.DataSetCode())

	return &pb.RetrieveObservationResponse{
		Observation: domainObservationToProto(observation, nil, datasetVersion),
	}, nil
}

// ListObservations returns observations matching the filter criteria with cursor-based pagination.
// Supports filtering by data set, time ranges, quality level, and pagination.
func (s *Server) ListObservations(ctx context.Context, req *pb.ListObservationsRequest) (*pb.ListObservationsResponse, error) {
	// Apply pagination defaults
	pageSize := int(req.PageSize)
	if pageSize < 0 {
		return nil, status.Errorf(codes.InvalidArgument, "page_size cannot be negative")
	}
	if pageSize == 0 {
		pageSize = 100 // Default
	}
	if pageSize > 1000 {
		pageSize = 1000 // Max from proto validation
	}

	// Build query from request filters
	query := domain.ObservationQuery{
		DataSetCode:       req.DatasetCode,
		IncludeSuperseded: req.IncludeSuperseded,
		Limit:             pageSize,
		PageToken:         req.PageToken,
	}

	// Apply resolution key filter
	if req.ResolutionKeyValue != "" {
		query.ResolutionKey = &req.ResolutionKeyValue
	}

	// Apply time range filters
	if req.ObservedFrom != nil {
		t := req.ObservedFrom.AsTime()
		query.ObservedAfter = &t
	}
	if req.ObservedTo != nil {
		t := req.ObservedTo.AsTime()
		query.ObservedBefore = &t
	}

	// Apply quality filter
	if req.QualityFilter != pb.QualityLevel_QUALITY_LEVEL_UNSPECIFIED {
		qualityLevel := protoQualityLevelToDomain(req.QualityFilter)
		query.QualityLevel = &qualityLevel
	}

	// Execute query
	observations, nextPageToken, err := s.observationRepo.Query(ctx, query)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidPageToken) {
			s.logger.Warn("invalid page token",
				"page_token", req.PageToken)
			return nil, status.Errorf(codes.InvalidArgument, "invalid page_token: malformed cursor")
		}
		s.logger.Error("failed to query observations",
			"dataset_code", req.DatasetCode,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to query observations: %v", err)
	}

	// Get total count for pagination info
	totalCount, err := s.observationRepo.CountByDataset(ctx, req.DatasetCode, req.IncludeSuperseded)
	if err != nil {
		// Log warning but don't fail the request - 0 means unknown per proto spec
		s.logger.Warn("failed to get observation count",
			"dataset_code", req.DatasetCode,
			"error", err)
		totalCount = 0
	}

	// Get dataset version for proto conversion
	datasetVersion := s.getDatasetVersion(ctx, req.DatasetCode)

	// Convert to proto messages
	pbObservations := make([]*pb.MarketPriceObservation, len(observations))
	for i, obs := range observations {
		pbObservations[i] = domainObservationToProto(obs, nil, datasetVersion)
	}

	s.logger.Debug("listed observations",
		"dataset_code", req.DatasetCode,
		"count", len(pbObservations),
		"total_count", totalCount,
		"has_more", nextPageToken != "")

	return &pb.ListObservationsResponse{
		Observations:  pbObservations,
		NextPageToken: nextPageToken,
		TotalCount:    int32(totalCount),
	}, nil
}
