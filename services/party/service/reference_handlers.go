package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// UpdateReference adds party reference data.
// NOTE: This method creates new reference records rather than updating existing ones.
// Multiple references of the same type (e.g., multiple passports) are allowed.
// To implement true update-or-insert behavior, a unique constraint on
// (party_id, reference_type) would be needed.
func (s *Service) UpdateReference(ctx context.Context, req *pb.UpdateReferenceRequest) (*pb.UpdateReferenceResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		s.logger.Error("invalid party ID format", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Verify party exists with FOR UPDATE lock to prevent deletion during update.
	// The FK constraint on party_reference provides additional safety.
	if _, err := s.repo.FindByIDForUpdate(ctx, partyID); err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// Collect references to save in a single transaction
	var refs []persistence.ReferenceInput
	if req.GovernmentId != "" {
		refs = append(refs, persistence.ReferenceInput{
			RefType:          "GOVERNMENT_ID",
			RefValue:         req.GovernmentId,
			IssuingAuthority: req.IssuingAuthority,
			ExpiryDate:       req.ExpiryDate,
		})
	}
	if req.TaxReference != "" {
		refs = append(refs, persistence.ReferenceInput{
			RefType:  "TAX_REFERENCE",
			RefValue: req.TaxReference,
		})
	}

	// Save all references in a single transaction
	if len(refs) > 0 {
		if err := s.repo.SaveReferences(ctx, partyID, refs); err != nil {
			s.logger.Error("failed to save references", "party_id", req.PartyId, "error", err)
			return nil, status.Errorf(codes.Internal, "failed to save references: %v", err)
		}
	}

	s.logger.Info("party reference updated", "party_id", req.PartyId)

	return &pb.UpdateReferenceResponse{
		PartyId:          req.PartyId,
		GovernmentId:     req.GovernmentId,
		TaxReference:     req.TaxReference,
		IssuingAuthority: req.IssuingAuthority,
		ExpiryDate:       req.ExpiryDate,
		UpdatedAt:        timestamppb.Now(),
	}, nil
}

// RetrieveReference retrieves party reference data
func (s *Service) RetrieveReference(ctx context.Context, req *pb.RetrieveReferenceRequest) (*pb.RetrieveReferenceResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	refs, err := s.repo.FindReferences(ctx, partyID)
	if err != nil {
		s.logger.Error("failed to retrieve references", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve references: %v", err)
	}

	// Build response from references by type.
	// Note: the response proto uses singular fields per reference type, so when multiple
	// references of the same type exist (allowed by the append-only write path), only
	// the last iterated row is returned. This is the current proto contract — supporting
	// multiple references per type requires a proto schema change.
	resp := &pb.RetrieveReferenceResponse{
		PartyId: req.PartyId,
	}
	for _, ref := range refs {
		switch ref.ReferenceType {
		case "GOVERNMENT_ID":
			resp.GovernmentId = ref.ReferenceValue
			if ref.IssuingAuthority != nil {
				resp.IssuingAuthority = *ref.IssuingAuthority
			}
			if ref.ExpiryDate != nil {
				resp.ExpiryDate = ref.ExpiryDate.Format("2006-01-02")
			}
			resp.UpdatedAt = timestamppb.New(ref.CreatedAt)
		case "TAX_REFERENCE":
			resp.TaxReference = ref.ReferenceValue
			resp.UpdatedAt = timestamppb.New(ref.CreatedAt)
		}
	}

	return resp, nil
}
