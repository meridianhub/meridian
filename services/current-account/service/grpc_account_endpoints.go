package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InitiateCurrentAccount creates a new current account facility
func (s *Service) InitiateCurrentAccount(ctx context.Context, req *pb.InitiateCurrentAccountRequest) (*pb.InitiateCurrentAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("initiate_account", operationStatus, time.Since(start))
	}()

	// Generate account ID
	accountID := fmt.Sprintf("ACC-%s", uuid.New().String()[:8])

	// Map currency enum to string
	currency := mapCurrency(req.BaseCurrency)
	if currency == "" {
		operationStatus = operationStatusInvalidCurrency
		return nil, status.Errorf(codes.InvalidArgument, "unsupported currency: %v", req.BaseCurrency)
	}

	// Validate party exists and is active (if party client is configured)
	if s.partyClient != nil {
		partyValidationStart := time.Now()
		s.logger.Info("validating party for account creation",
			"party_id", req.PartyId,
			"account_id", accountID)

		if err := s.partyClient.ValidateParty(ctx, req.PartyId); err != nil {
			caobservability.RecordPartyValidationDuration(time.Since(partyValidationStart), false)

			if errors.Is(err, ErrPartyNotFound) {
				operationStatus = "party_not_found"
				s.logger.Warn("party not found during account creation",
					"party_id", req.PartyId,
					"account_id", accountID)
				return nil, status.Errorf(codes.InvalidArgument, "party not found: %s", req.PartyId)
			}
			if errors.Is(err, ErrPartyNotActive) {
				operationStatus = "party_not_active"
				s.logger.Warn("party not active during account creation",
					"party_id", req.PartyId,
					"account_id", accountID)
				return nil, status.Errorf(codes.FailedPrecondition, "party not active: %s", req.PartyId)
			}
			operationStatus = "party_validation_failed"
			s.logger.Error("party validation failed during account creation",
				"party_id", req.PartyId,
				"account_id", accountID,
				"error", err)
			caobservability.RecordExternalServiceError("party", "validate_party")
			return nil, status.Errorf(codes.Internal, "party validation failed: %v", err)
		}

		caobservability.RecordPartyValidationDuration(time.Since(partyValidationStart), true)
		s.logger.Info("party validated successfully",
			"party_id", req.PartyId,
			"account_id", accountID)
	}

	// Create domain model (now returns value, not pointer)
	account, err := domain.NewCurrentAccount(
		accountID,
		req.AccountIdentification,
		req.PartyId,
		currency,
	)
	if err != nil {
		operationStatus = "domain_error"
		return nil, status.Errorf(codes.InvalidArgument, "failed to create account: %v", err)
	}

	// Save to database (context carries audit user info for created_by/updated_by fields)
	if err := s.repo.Save(ctx, account); err != nil {
		operationStatus = opStatusSaveFailed
		return nil, status.Errorf(codes.Internal, "failed to create account: %v", err)
	}

	// Record initial balance
	caobservability.RecordBalance(safeMinorUnits(account.Balance()), currency)

	// Convert to proto response
	return &pb.InitiateCurrentAccountResponse{
		AccountId: accountID,
		Facility:  toProtoFacility(account),
	}, nil
}

// RetrieveCurrentAccount gets current account details including balance from Position Keeping.
func (s *Service) RetrieveCurrentAccount(ctx context.Context, req *pb.RetrieveCurrentAccountRequest) (*pb.RetrieveCurrentAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("retrieve_account", operationStatus, time.Since(start))
	}()

	// Context carries organization for multi-tenant routing
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Hydrate account with balance from Position Keeping service.
	// Balance is no longer persisted locally - Position Keeping is the source of truth.
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to retrieve balance from Position Keeping",
			"account_id", req.AccountId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	return &pb.RetrieveCurrentAccountResponse{
		Facility: toProtoFacility(account),
	}, nil
}

// UpdateCurrentAccount modifies account configuration settings.
// BIAN: Update Control Record (UpCR) - Updates overdraft settings.
// Uses optimistic locking to prevent lost updates from concurrent modifications.
func (s *Service) UpdateCurrentAccount(ctx context.Context, req *pb.UpdateCurrentAccountRequest) (*pb.UpdateCurrentAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("update_account", operationStatus, time.Since(start))
	}()

	// Validate required fields
	if req.AccountId == "" {
		operationStatus = opStatusMissingAccountID
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}

	// Retrieve account (context carries organization for multi-tenant routing)
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Check if account is closed - cannot update closed accounts
	if account.Status() == domain.AccountStatusClosed {
		operationStatus = opStatusAccountClosed
		return nil, status.Errorf(codes.FailedPrecondition, "cannot update closed account: %s", req.AccountId)
	}

	// Track if any updates were made
	updated := false

	// Apply overdraft settings updates if any overdraft fields are provided
	if req.OverdraftLimit != nil || req.OverdraftEnabled != nil || req.OverdraftRate != nil {
		// Determine new overdraft values, using current values as defaults
		newLimit := account.OverdraftLimit()
		newRate := account.OverdraftRate()
		newEnabled := account.OverdraftEnabled()

		// Apply overdraft limit if provided
		if req.OverdraftLimit != nil {
			limitCurrency := req.OverdraftLimit.Amount.CurrencyCode
			if limitCurrency != account.Balance().CurrencyCode() {
				operationStatus = opStatusCurrencyMismatch
				return nil, status.Errorf(codes.InvalidArgument,
					"overdraft limit currency mismatch: expected %s, got %s",
					account.Balance().CurrencyCode(), limitCurrency)
			}

			// Convert to minor units
			limitCents := req.OverdraftLimit.Amount.Units*100 + int64(req.OverdraftLimit.Amount.Nanos/10000000)
			var err error
			newLimit, err = domain.NewMoney(limitCurrency, limitCents)
			if err != nil {
				operationStatus = operationStatusInvalidCurrency
				return nil, status.Errorf(codes.InvalidArgument, "invalid overdraft limit: %v", err)
			}
		}

		// Apply overdraft rate if provided
		if req.OverdraftRate != nil {
			newRate = *req.OverdraftRate
		}

		// Apply overdraft enabled if provided
		if req.OverdraftEnabled != nil {
			newEnabled = *req.OverdraftEnabled
		}

		// Use domain method to update overdraft settings with validation
		account, err = account.UpdateOverdraftSettings(newLimit, newRate, newEnabled)
		if err != nil {
			if errors.Is(err, domain.ErrNegativeOverdraftRate) {
				operationStatus = opStatusInvalidAmount
				return nil, status.Errorf(codes.InvalidArgument, "invalid overdraft rate: %v", err)
			}
			operationStatus = "update_overdraft_failed"
			return nil, status.Errorf(codes.InvalidArgument, "failed to update overdraft settings: %v", err)
		}
		updated = true

		s.logger.Info("overdraft settings updated",
			"account_id", req.AccountId,
			"overdraft_enabled", newEnabled,
			"overdraft_rate", newRate)
	}

	// If no updates were made, return current state
	if !updated {
		s.logger.Debug("no changes to apply for UpdateCurrentAccount",
			"account_id", req.AccountId)
		return &pb.UpdateCurrentAccountResponse{
			Facility: toProtoFacility(account),
			Version:  account.Version(),
		}, nil
	}

	// Persist with optimistic locking
	if err := s.repo.Save(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			operationStatus = "version_conflict"
			s.logger.Warn("version conflict during account update",
				"account_id", req.AccountId)
			return nil, status.Errorf(codes.Aborted, "version conflict: account was modified by another transaction, please retry")
		}
		operationStatus = opStatusSaveFailed
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	s.logger.Info("account updated successfully",
		"account_id", req.AccountId,
		"new_version", account.Version())

	return &pb.UpdateCurrentAccountResponse{
		Facility: toProtoFacility(account),
		Version:  account.Version(),
	}, nil
}
