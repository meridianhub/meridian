package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// RegisterParty creates a new party in the reference data directory
func (s *Service) RegisterParty(ctx context.Context, req *pb.RegisterPartyRequest) (*pb.RegisterPartyResponse, error) {
	// === Input validation (fail-fast) ===

	// Validate party type
	partyType, err := protoToPartyType(req.PartyType)
	if err != nil {
		s.logger.Error("invalid party type",
			"party_type", req.PartyType.String(),
			"error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid party type: %v", err)
	}

	// Validate external reference and type consistency
	if req.ExternalReferenceType != pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED && req.ExternalReference == "" {
		s.logger.Error("external reference type provided without reference",
			"external_reference_type", req.ExternalReferenceType.String())
		return nil, status.Errorf(codes.InvalidArgument, "external reference required when type is specified")
	}

	// Validate external reference type if external reference is provided
	var extRefType domain.ExternalReferenceType
	if req.ExternalReference != "" {
		extRefType, err = protoToExternalRefType(req.ExternalReferenceType)
		if err != nil {
			s.logger.Error("invalid external reference type",
				"external_reference_type", req.ExternalReferenceType.String(),
				"error", err)
			return nil, status.Errorf(codes.InvalidArgument, "invalid external reference type: %v", err)
		}
	}

	// === Domain object creation ===

	party, err := domain.NewParty(partyType, req.LegalName)
	if err != nil {
		s.logger.Error("failed to create party",
			"party_type", partyType,
			"error", err)
		return nil, status.Errorf(codes.InvalidArgument, "failed to create party: %v", err)
	}

	// Set optional display name
	if req.DisplayName != "" {
		if err := party.SetDisplayName(req.DisplayName); err != nil {
			s.logger.Error("invalid display name",
				"error", err)
			return nil, status.Errorf(codes.InvalidArgument, "invalid display name: %v", err)
		}
	}

	// Set optional attributes if provided at registration time.
	if len(req.Attributes) > 0 {
		party.SetAttributes(protoAttributesToDomain(req.Attributes))
	}

	// === Attribute validation ===

	// Validate attributes against the tenant's PartyTypeDefinition schema and CEL rules.
	// Validation is skipped when no validator is configured or no tenant context is present.
	if s.attributeValidator != nil {
		if tid, ok := tenant.FromContext(ctx); ok {
			if err := s.attributeValidator.ValidateAttributes(ctx, tid.String(), string(partyType), party); err != nil {
				s.logger.Warn("attribute validation failed during registration",
					"party_type", partyType,
					"tenant_id", tid.String(),
					"error", err)
				return nil, status.Errorf(codes.InvalidArgument, "attribute validation failed: %v", err)
			}
		}
	}

	// === External reference handling ===

	if req.ExternalReference != "" {
		// Check for duplicate external reference
		existing, err := s.repo.FindByExternalReference(ctx, req.ExternalReference, string(extRefType))
		if err != nil && !errors.Is(err, persistence.ErrPartyNotFound) {
			s.logger.Error("failed to check external reference uniqueness",
				"external_reference_type", extRefType,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to check external reference: %v", err)
		}
		if existing != nil {
			s.logger.Warn("duplicate external reference",
				"external_reference_type", extRefType,
				"existing_party_id", existing.ID().String())
			return nil, status.Errorf(codes.AlreadyExists,
				"party with external reference of type %s already exists",
				extRefType)
		}

		if err := party.SetExternalReference(req.ExternalReference, extRefType); err != nil {
			s.logger.Error("invalid external reference",
				"external_reference_type", extRefType,
				"error", err)
			return nil, status.Errorf(codes.InvalidArgument, "invalid external reference: %v", err)
		}
	}

	// Save party and publish event atomically
	if err := s.savePartyWithEvent(ctx, party, func(tx *gorm.DB) error {
		event := &eventsv1.PartyCreatedEvent{
			EventId:   uuid.New().String(),
			PartyId:   party.ID().String(),
			PartyType: string(party.PartyType()),
			LegalName: party.LegalName(),
			Status:    string(party.Status()),
			Timestamp: timestamppb.New(time.Now().UTC()),
		}
		return s.outboxPublisher.Publish(ctx, tx, event, events.PublishConfig{
			EventType:     "party.created.v1",
			AggregateID:   party.ID().String(),
			AggregateType: "Party",
			Topic:         topics.PartyCreatedV1,
		})
	}); err != nil {
		if errors.Is(err, persistence.ErrPartyExists) {
			s.logger.Warn("party already exists (race condition)",
				"party_id", party.ID().String())
			return nil, status.Errorf(codes.AlreadyExists, "party already exists")
		}
		s.logger.Error("failed to save party",
			"party_id", party.ID().String(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to save party: %v", err)
	}

	s.logger.Info("party registered",
		"party_id", party.ID().String(),
		"party_type", partyType,
		"external_reference_type", party.ExternalReferenceType())

	return &pb.RegisterPartyResponse{
		Party: domainToProto(party),
	}, nil
}

// RetrieveParty gets party details by ID
func (s *Service) RetrieveParty(ctx context.Context, req *pb.RetrievePartyRequest) (*pb.RetrievePartyResponse, error) {
	// Parse party ID as UUID
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		s.logger.Error("invalid party ID format",
			"party_id", req.PartyId,
			"error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Retrieve party
	party, err := s.repo.FindByID(ctx, partyID)
	if err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			s.logger.Warn("party not found",
				"party_id", req.PartyId)
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		s.logger.Error("failed to retrieve party",
			"party_id", req.PartyId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	return &pb.RetrievePartyResponse{
		Party: domainToProto(party),
	}, nil
}

// ListParties returns a paginated list of parties with optional filtering.
func (s *Service) ListParties(ctx context.Context, req *pb.ListPartiesRequest) (*pb.ListPartiesResponse, error) {
	// Apply defaults and validate page size
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 25
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// Decode cursor
	cursor, err := persistence.DecodePartyCursor(req.PageToken)
	if err != nil {
		s.logger.Warn("invalid page_token", "page_token", req.PageToken, "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
	}

	// Build filter params
	params := persistence.ListPartiesParams{
		Limit:       pageSize,
		Cursor:      cursor,
		SearchQuery: req.SearchQuery,
	}

	if req.PartyType != pb.PartyType_PARTY_TYPE_UNSPECIFIED {
		partyType, err := protoToPartyType(req.PartyType)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid party_type: %v", err)
		}
		params.PartyType = string(partyType)
	}

	if req.Status != pb.PartyStatus_PARTY_STATUS_UNSPECIFIED {
		params.Status = protoToPartyStatus(req.Status)
	}

	result, err := s.repo.ListParties(ctx, params)
	if err != nil {
		s.logger.Error("failed to list parties", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list parties: %v", err)
	}

	pbParties := make([]*pb.Party, len(result.Parties))
	for i, p := range result.Parties {
		pbParties[i] = domainToProto(p)
	}

	return &pb.ListPartiesResponse{
		Parties:       pbParties,
		NextPageToken: result.NextCursor,
		TotalCount:    result.TotalCount,
	}, nil
}

// UpdateParty updates party details with field mask support for partial updates
func (s *Service) UpdateParty(ctx context.Context, req *pb.UpdatePartyRequest) (*pb.UpdatePartyResponse, error) {
	// Parse party ID
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		s.logger.Error("invalid party ID format", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Load existing party with pessimistic lock for consistent read-modify-write
	party, err := s.repo.FindByIDForUpdate(ctx, partyID)
	if err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			s.logger.Warn("party not found", "party_id", req.PartyId)
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		s.logger.Error("failed to retrieve party", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// Verify version for optimistic locking (defense in depth)
	// #nosec G115 - Version is bounded by database constraints
	if req.Version > 0 && party.Version() != int64(req.Version) {
		s.logger.Warn("version conflict", "party_id", req.PartyId, "expected", req.Version, "actual", party.Version())
		return nil, status.Errorf(codes.Aborted, "version conflict: party was modified by another transaction")
	}

	// Apply field mask updates
	attributesUpdated := false
	if req.UpdateMask != nil && len(req.UpdateMask.Paths) > 0 {
		for _, path := range req.UpdateMask.Paths {
			if path == "attributes" {
				attributesUpdated = true
			}
			if err := s.applyFieldUpdate(party, path, req); err != nil {
				s.logger.Error("failed to apply field update", "field", path, "error", err)
				return nil, status.Errorf(codes.InvalidArgument, "failed to update field %s: %v", path, err)
			}
		}
	} else {
		// No field mask - update all non-empty fields
		if req.DisplayName != "" {
			if err := party.SetDisplayName(req.DisplayName); err != nil {
				s.logger.Error("invalid display name", "error", err)
				return nil, status.Errorf(codes.InvalidArgument, "invalid display name: %v", err)
			}
		}
	}

	// Validate attributes if they were updated and a validator is configured.
	// Validation is skipped when no validator is configured or no tenant context is present.
	if attributesUpdated && s.attributeValidator != nil {
		if tid, ok := tenant.FromContext(ctx); ok {
			partyType := string(party.PartyType())
			if err := s.attributeValidator.ValidateAttributes(ctx, tid.String(), partyType, party); err != nil {
				s.logger.Warn("attribute validation failed during update",
					"party_id", req.PartyId,
					"party_type", partyType,
					"tenant_id", tid.String(),
					"error", err)
				return nil, status.Errorf(codes.InvalidArgument, "attribute validation failed: %v", err)
			}
		}
	}

	// Persist updated party and publish event atomically
	if err := s.savePartyWithEvent(ctx, party, func(tx *gorm.DB) error {
		event := &eventsv1.PartyUpdatedEvent{
			EventId:   uuid.New().String(),
			PartyId:   party.ID().String(),
			PartyType: string(party.PartyType()),
			Status:    string(party.Status()),
			Timestamp: timestamppb.New(time.Now().UTC()),
		}
		return s.outboxPublisher.Publish(ctx, tx, event, events.PublishConfig{
			EventType:     "party.updated.v1",
			AggregateID:   party.ID().String(),
			AggregateType: "Party",
			Topic:         topics.PartyUpdatedV1,
		})
	}); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			s.logger.Warn("version conflict on save", "party_id", req.PartyId)
			return nil, status.Errorf(codes.Aborted, "version conflict: party was modified by another transaction")
		}
		s.logger.Error("failed to save party", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to save party: %v", err)
	}

	s.logger.Info("party updated", "party_id", req.PartyId, "version", party.Version())

	return &pb.UpdatePartyResponse{
		Party: domainToProto(party),
	}, nil
}

// applyFieldUpdate applies a single field update based on field mask path
func (s *Service) applyFieldUpdate(party *domain.Party, path string, req *pb.UpdatePartyRequest) error {
	switch path {
	case "display_name":
		if req.DisplayName != "" {
			return party.SetDisplayName(req.DisplayName)
		}
	case "attributes":
		party.SetAttributes(protoAttributesToDomain(req.Attributes))
	default:
		return ErrUnsupportedFieldUpdate
	}
	return nil
}

// ControlParty manages party lifecycle with state machine enforcement
func (s *Service) ControlParty(ctx context.Context, req *pb.ControlPartyRequest) (*pb.ControlPartyResponse, error) {
	// Parse party ID
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		s.logger.Error("invalid party ID format", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Convert control action to domain type
	action, err := protoToControlAction(req.ControlAction)
	if err != nil {
		s.logger.Error("invalid control action", "action", req.ControlAction.String(), "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid control action: %v", err)
	}

	// Load existing party
	party, err := s.repo.FindByIDForUpdate(ctx, partyID)
	if err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			s.logger.Warn("party not found", "party_id", req.PartyId)
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		s.logger.Error("failed to retrieve party", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// Apply control action (state machine enforced in domain)
	if err := party.ControlParty(action, req.Reason); err != nil {
		s.logger.Error("failed to apply control action",
			"party_id", req.PartyId,
			"action", action,
			"error", err)
		return nil, status.Errorf(codes.FailedPrecondition, "invalid status transition: %v", err)
	}

	// Set actor_id in context for audit trail if provided
	saveCtx := ctx
	if req.ActorId != "" {
		saveCtx = context.WithValue(ctx, auth.UserIDContextKey, req.ActorId)
	}

	// Persist updated party and publish control event atomically
	actionTime := time.Now().UTC()
	if err := s.savePartyWithEvent(saveCtx, party, func(tx *gorm.DB) error {
		event := &eventsv1.PartyControlledEvent{
			EventId:       uuid.New().String(),
			PartyId:       party.ID().String(),
			ControlAction: string(action),
			NewStatus:     string(party.Status()),
			Reason:        req.Reason,
			ActorId:       req.ActorId,
			Timestamp:     timestamppb.New(actionTime),
		}
		return s.outboxPublisher.Publish(saveCtx, tx, event, events.PublishConfig{
			EventType:     "party.controlled.v1",
			AggregateID:   party.ID().String(),
			AggregateType: "Party",
			Topic:         topics.PartyControlledV1,
		})
	}); err != nil {
		s.logger.Error("failed to save party after control action", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to save party: %v", err)
	}

	s.logger.Info("party control action executed",
		"party_id", req.PartyId,
		"action", action,
		"actor_id", req.ActorId,
		"reason", req.Reason,
		"new_status", party.Status())

	return &pb.ControlPartyResponse{
		Party:           domainToProto(party),
		ActionTimestamp: timestamppb.New(actionTime),
	}, nil
}
