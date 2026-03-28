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
	partyType, extRefType, err := validateRegisterPartyInput(req)
	if err != nil {
		return nil, err
	}

	party, err := s.buildNewParty(ctx, req, partyType)
	if err != nil {
		return nil, err
	}

	if err := s.applyExternalReference(ctx, party, req.ExternalReference, extRefType); err != nil {
		return nil, err
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

// validateRegisterPartyInput validates and parses the party type and external reference type from the request.
func validateRegisterPartyInput(req *pb.RegisterPartyRequest) (domain.PartyType, domain.ExternalReferenceType, error) {
	partyType, err := protoToPartyType(req.PartyType)
	if err != nil {
		return "", "", status.Errorf(codes.InvalidArgument, "invalid party type: %v", err)
	}

	if req.ExternalReferenceType != pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED && req.ExternalReference == "" {
		return "", "", status.Errorf(codes.InvalidArgument, "external reference required when type is specified")
	}

	var extRefType domain.ExternalReferenceType
	if req.ExternalReference != "" {
		extRefType, err = protoToExternalRefType(req.ExternalReferenceType)
		if err != nil {
			return "", "", status.Errorf(codes.InvalidArgument, "invalid external reference type: %v", err)
		}
	}

	return partyType, extRefType, nil
}

// buildNewParty creates a domain Party from the request, applying optional fields and attribute validation.
func (s *Service) buildNewParty(ctx context.Context, req *pb.RegisterPartyRequest, partyType domain.PartyType) (*domain.Party, error) {
	party, err := domain.NewParty(partyType, req.LegalName)
	if err != nil {
		s.logger.Error("failed to create party", "party_type", partyType, "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "failed to create party: %v", err)
	}

	if req.DisplayName != "" {
		if err := party.SetDisplayName(req.DisplayName); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid display name: %v", err)
		}
	}

	if len(req.Attributes) > 0 {
		party.SetAttributes(protoAttributesToDomain(req.Attributes))
	}

	if err := s.validatePartyAttributes(ctx, party, partyType); err != nil {
		return nil, err
	}

	return party, nil
}

// validatePartyAttributes validates attributes against the tenant's PartyTypeDefinition schema and CEL rules.
// Validation is skipped when no validator is configured or no tenant context is present.
func (s *Service) validatePartyAttributes(ctx context.Context, party *domain.Party, partyType domain.PartyType) error {
	if s.attributeValidator == nil {
		return nil
	}
	tid, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}
	if err := s.attributeValidator.ValidateAttributes(ctx, tid.String(), string(partyType), party); err != nil {
		s.logger.Warn("attribute validation failed",
			"party_type", partyType,
			"tenant_id", tid.String(),
			"error", err)
		return status.Errorf(codes.InvalidArgument, "attribute validation failed: %v", err)
	}
	return nil
}

// applyExternalReference checks for duplicates and sets the external reference on the party.
// No-op if externalRef is empty.
func (s *Service) applyExternalReference(ctx context.Context, party *domain.Party, externalRef string, extRefType domain.ExternalReferenceType) error {
	if externalRef == "" {
		return nil
	}

	existing, err := s.repo.FindByExternalReference(ctx, externalRef, string(extRefType))
	if err != nil && !errors.Is(err, persistence.ErrPartyNotFound) {
		s.logger.Error("failed to check external reference uniqueness",
			"external_reference_type", extRefType, "error", err)
		return status.Errorf(codes.Internal, "failed to check external reference: %v", err)
	}
	if existing != nil {
		s.logger.Warn("duplicate external reference",
			"external_reference_type", extRefType,
			"existing_party_id", existing.ID().String())
		return status.Errorf(codes.AlreadyExists,
			"party with external reference of type %s already exists", extRefType)
	}

	if err := party.SetExternalReference(externalRef, extRefType); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid external reference: %v", err)
	}
	return nil
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
		statusStr, err := protoToPartyStatus(req.Status)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid status: %v", err)
		}
		params.Status = statusStr
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
	party, err := s.loadPartyForUpdate(ctx, req.PartyId, int64(req.Version))
	if err != nil {
		return nil, err
	}

	attributesUpdated, err := s.applyUpdateFields(party, req)
	if err != nil {
		return nil, err
	}

	if attributesUpdated {
		if err := s.validatePartyAttributes(ctx, party, party.PartyType()); err != nil {
			return nil, err
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

// loadPartyForUpdate loads a party with a pessimistic lock and verifies the optimistic locking version.
// Pass version <= 0 to skip the version check.
func (s *Service) loadPartyForUpdate(ctx context.Context, partyID string, version int64) (*domain.Party, error) {
	id, err := uuid.Parse(partyID)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	party, err := s.repo.FindByIDForUpdate(ctx, id)
	if err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return nil, status.Errorf(codes.NotFound, "party not found: %s", partyID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// #nosec G115 - Version is bounded by database constraints
	if version > 0 && party.Version() != version {
		s.logger.Warn("version conflict", "party_id", partyID, "expected", version, "actual", party.Version())
		return nil, status.Errorf(codes.Aborted, "version conflict: party was modified by another transaction")
	}

	return party, nil
}

// applyUpdateFields applies field mask or full-field updates to the party.
// Returns true if attributes were updated.
func (s *Service) applyUpdateFields(party *domain.Party, req *pb.UpdatePartyRequest) (bool, error) {
	attributesUpdated := false

	if req.UpdateMask != nil && len(req.UpdateMask.Paths) > 0 {
		for _, path := range req.UpdateMask.Paths {
			if path == "attributes" {
				attributesUpdated = true
			}
			if err := s.applyFieldUpdate(party, path, req); err != nil {
				s.logger.Error("failed to apply field update", "field", path, "error", err)
				return false, status.Errorf(codes.InvalidArgument, "failed to update field %s: %v", path, err)
			}
		}
	} else {
		if req.DisplayName != "" {
			if err := party.SetDisplayName(req.DisplayName); err != nil {
				return false, status.Errorf(codes.InvalidArgument, "invalid display name: %v", err)
			}
		}
		if len(req.Attributes) > 0 {
			party.SetAttributes(protoAttributesToDomain(req.Attributes))
			attributesUpdated = true
		}
	}

	return attributesUpdated, nil
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
	action, err := protoToControlAction(req.ControlAction)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid control action: %v", err)
	}

	party, err := s.loadPartyForUpdate(ctx, req.PartyId, 0)
	if err != nil {
		return nil, err
	}

	if err := party.ControlParty(action, req.Reason); err != nil {
		s.logger.Error("failed to apply control action",
			"party_id", req.PartyId, "action", action, "error", err)
		return nil, status.Errorf(codes.FailedPrecondition, "invalid status transition: %v", err)
	}

	saveCtx := ctx
	if req.ActorId != "" {
		saveCtx = context.WithValue(ctx, auth.UserIDContextKey, req.ActorId)
	}

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
		if errors.Is(err, persistence.ErrVersionConflict) {
			s.logger.Warn("version conflict on save", "party_id", req.PartyId)
			return nil, status.Errorf(codes.Aborted, "version conflict: party was modified by another transaction")
		}
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
