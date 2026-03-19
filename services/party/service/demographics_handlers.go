package service

import (
	"context"
	"errors"
	"os"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/verification"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ExchangeDemographics verifies party demographics data
func (s *Service) ExchangeDemographics(ctx context.Context, req *pb.ExchangeDemographicsRequest) (*pb.ExchangeDemographicsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Retrieve party for verification
	party, err := s.repo.FindByID(ctx, partyID)
	if err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// If no verification provider is configured, fall back to stub behavior
	if s.verificationProvider == nil {
		return s.exchangeDemographicsStub(req.PartyId)
	}

	// Delegate to the real verification provider
	result, err := s.verificationProvider.VerifyIdentity(ctx, party)
	if err != nil {
		s.logger.Error("verification provider error",
			"party_id", req.PartyId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "verification failed: %v", err)
	}

	verificationStatus := string(result.Status)

	s.logger.Info("identity verification completed",
		"party_id", req.PartyId,
		"verification_id", result.VerificationID,
		"status", verificationStatus,
		"risk_score", result.RiskScore)

	// Sanctions screening - errors warn but do not fail the request
	sanctionsResult, err := s.verificationProvider.CheckSanctions(ctx, party)
	if err != nil {
		s.logger.Warn("sanctions screening failed, proceeding with identity result",
			"party_id", req.PartyId,
			"error", err)
	} else if sanctionsResult.Status == verification.SanctionsStatusMatch {
		verificationStatus = string(verification.StatusManualReview)
		s.logger.Warn("sanctions match found, overriding status to MANUAL_REVIEW",
			"party_id", req.PartyId,
			"screening_id", sanctionsResult.ScreeningID,
			"match_count", len(sanctionsResult.Matches))
	}

	return &pb.ExchangeDemographicsResponse{
		PartyId:               req.PartyId,
		VerificationStatus:    verificationStatus,
		VerificationTimestamp: timestamppb.Now(),
	}, nil
}

// exchangeDemographicsStub returns a stub response when no verification provider is configured.
// In production, this returns Unimplemented to prevent operating without a real provider.
// In development/test environments, it returns a stub VERIFIED response.
func (s *Service) exchangeDemographicsStub(partyID string) (*pb.ExchangeDemographicsResponse, error) {
	if os.Getenv("ENVIRONMENT") == "production" {
		return nil, status.Error(codes.Unimplemented,
			"KYC/AML verification not implemented - no verification provider configured")
	}

	s.logger.Warn("using stub KYC verification - no provider configured",
		"party_id", partyID,
		"environment", os.Getenv("ENVIRONMENT"))

	return &pb.ExchangeDemographicsResponse{
		PartyId:               partyID,
		VerificationStatus:    "VERIFIED",
		VerificationTimestamp: timestamppb.Now(),
	}, nil
}

// UpdateDemographics updates party demographics data
func (s *Service) UpdateDemographics(ctx context.Context, req *pb.UpdateDemographicsRequest) (*pb.UpdateDemographicsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Verify party exists with FOR UPDATE lock to prevent deletion during update.
	// The FK constraint on party_demographic provides additional safety.
	if _, err := s.repo.FindByIDForUpdate(ctx, partyID); err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// Save demographic data
	if err := s.repo.SaveDemographic(ctx, partyID, req.SocioEconomicData, req.EmploymentHistory); err != nil {
		s.logger.Error("failed to save demographic", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to save demographic: %v", err)
	}

	s.logger.Info("party demographics updated", "party_id", req.PartyId)

	return &pb.UpdateDemographicsResponse{
		PartyId:           req.PartyId,
		SocioEconomicData: req.SocioEconomicData,
		EmploymentHistory: req.EmploymentHistory,
		UpdatedAt:         timestamppb.Now(),
	}, nil
}

// RetrieveDemographics retrieves party demographics data
func (s *Service) RetrieveDemographics(ctx context.Context, req *pb.RetrieveDemographicsRequest) (*pb.RetrieveDemographicsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	demo, err := s.repo.FindDemographic(ctx, partyID)
	if err != nil {
		s.logger.Error("failed to retrieve demographic", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve demographic: %v", err)
	}

	resp := &pb.RetrieveDemographicsResponse{
		PartyId: req.PartyId,
	}
	if demo != nil {
		if demo.SocioEconomicData != nil {
			// Extract string from JSONB - handles both JSON strings and raw values
			resp.SocioEconomicData = fromJSONB(*demo.SocioEconomicData)
		}
		if demo.EmploymentHistory != nil {
			// Extract string from JSONB - handles both JSON strings and raw values
			resp.EmploymentHistory = fromJSONB(*demo.EmploymentHistory)
		}
		resp.UpdatedAt = timestamppb.New(demo.UpdatedAt)
	}

	return resp, nil
}

// UpdateBankRelations updates party bank relationship data
func (s *Service) UpdateBankRelations(ctx context.Context, req *pb.UpdateBankRelationsRequest) (*pb.UpdateBankRelationsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Verify party exists with FOR UPDATE lock to prevent deletion during update.
	// The FK constraint on party_bank_relation provides additional safety.
	if _, err := s.repo.FindByIDForUpdate(ctx, partyID); err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// Save bank relation data
	if err := s.repo.SaveBankRelation(ctx, partyID, req.AccountOfficerId, req.RelationshipManagerId, req.AssignedBranch); err != nil {
		s.logger.Error("failed to save bank relation", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to save bank relation: %v", err)
	}

	s.logger.Info("party bank relations updated", "party_id", req.PartyId)

	return &pb.UpdateBankRelationsResponse{
		PartyId:               req.PartyId,
		AccountOfficerId:      req.AccountOfficerId,
		RelationshipManagerId: req.RelationshipManagerId,
		AssignedBranch:        req.AssignedBranch,
		UpdatedAt:             timestamppb.Now(),
	}, nil
}

// RetrieveBankRelations retrieves party bank relationship data
func (s *Service) RetrieveBankRelations(ctx context.Context, req *pb.RetrieveBankRelationsRequest) (*pb.RetrieveBankRelationsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	bankRel, err := s.repo.FindBankRelation(ctx, partyID)
	if err != nil {
		s.logger.Error("failed to retrieve bank relation", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve bank relation: %v", err)
	}

	resp := &pb.RetrieveBankRelationsResponse{
		PartyId: req.PartyId,
	}
	if bankRel != nil {
		if bankRel.AccountOfficerID != nil {
			resp.AccountOfficerId = *bankRel.AccountOfficerID
		}
		if bankRel.RelationshipManagerID != nil {
			resp.RelationshipManagerId = *bankRel.RelationshipManagerID
		}
		if bankRel.AssignedBranch != nil {
			resp.AssignedBranch = *bankRel.AssignedBranch
		}
		resp.UpdatedAt = timestamppb.New(bankRel.UpdatedAt)
	}

	return resp, nil
}
