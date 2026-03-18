package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// toLienProto converts a domain Lien to proto Lien
func toLienProto(lien *domain.Lien) *pb.Lien {
	pbLien := &pb.Lien{
		LienId:                lien.ID.String(),
		AccountId:             lien.AccountID.String(),
		Amount:                toMoneyAmount(lien.Amount),
		Status:                mapLienStatusToProto(lien.Status),
		PaymentOrderReference: lien.PaymentOrderReference,
		CreatedAt:             timestamppb.New(lien.CreatedAt),
		UpdatedAt:             timestamppb.New(lien.UpdatedAt),
		BucketId:              lien.BucketID,
	}

	// Add valuation fields if present
	if lien.ReservedQuantity != nil && !lien.ReservedQuantity.IsZero() {
		pbLien.ReservedQuantity = &quantityv1.InstrumentAmount{
			Amount:         lien.ReservedQuantity.Amount.String(),
			InstrumentCode: lien.ReservedQuantity.InstrumentCode,
			Version:        1,
		}
	}
	if lien.ValuedAmount != nil && !lien.ValuedAmount.IsZero() {
		pbLien.ValuedAmount = &quantityv1.InstrumentAmount{
			Amount:         lien.ValuedAmount.Amount.String(),
			InstrumentCode: lien.ValuedAmount.InstrumentCode,
			Version:        1,
		}
	}

	return pbLien
}

// mapLienStatusToProto maps domain LienStatus to proto LienStatus
func mapLienStatusToProto(status domain.LienStatus) pb.LienStatus {
	switch status {
	case domain.LienStatusActive:
		return pb.LienStatus_LIEN_STATUS_ACTIVE
	case domain.LienStatusExecuted:
		return pb.LienStatus_LIEN_STATUS_EXECUTED
	case domain.LienStatusTerminated:
		return pb.LienStatus_LIEN_STATUS_TERMINATED
	default:
		return pb.LienStatus_LIEN_STATUS_UNSPECIFIED
	}
}

// buildExecuteLienIdempotentResponse builds an idempotent response for an already-executed lien.
func (s *Service) buildExecuteLienIdempotentResponse(ctx context.Context, lien *domain.Lien) (*pb.ExecuteLienResponse, error) {
	account, err := s.repo.FindByUUID(ctx, lien.AccountID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Hydrate account with balance from Position Keeping.
	// Balance is no longer persisted - it comes from Position Keeping service.
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get account balance: %v", err)
	}

	// Calculate available balance scoped to the lien's bucket (if any)
	availableMoney := s.calculateAvailableBalanceByBucket(ctx, lien.AccountID, lien.BucketID, account.Balance())
	return &pb.ExecuteLienResponse{
		Lien:             toLienProto(lien),
		NewBalance:       toMoneyAmount(account.Balance()),
		AvailableBalance: toMoneyAmount(availableMoney),
		TransactionId:    fmt.Sprintf("TXN-LIEN-%s", lien.ID.String()[:8]),
	}, nil
}

// checkLienIdempotency checks if a lien with the given PaymentOrderReference already exists.
// Returns (response, true) if idempotent response should be returned, (nil, false) otherwise.
// Returns error status if a non-recoverable error occurs.
func (s *Service) checkLienIdempotency(ctx context.Context, paymentOrderRef string) (*pb.InitiateLienResponse, bool, error) {
	if paymentOrderRef == "" {
		return nil, false, nil
	}

	existingLien, err := s.lienRepo.FindByPaymentOrderReference(ctx, paymentOrderRef)
	if err != nil {
		if errors.Is(err, persistence.ErrLienNotFound) {
			return nil, false, nil // Not found - continue with creation
		}
		return nil, false, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
	}

	// Lien already exists - return idempotent response
	s.logger.Info("lien already exists (idempotent)",
		"lien_id", existingLien.ID.String(),
		"payment_order_ref", paymentOrderRef)

	// Retrieve account for available balance calculation
	account, acctErr := s.repo.FindByUUID(ctx, existingLien.AccountID)
	if acctErr != nil {
		s.logger.Error("failed to retrieve account for idempotent response", "error", acctErr)
		return &pb.InitiateLienResponse{Lien: toLienProto(existingLien)}, true, nil
	}
	// Hydrate account with balance from Position Keeping.
	account, acctErr = s.hydrateAccountWithBalance(ctx, account)
	if acctErr != nil {
		s.logger.Error("failed to get account balance for idempotent response", "error", acctErr)
		return &pb.InitiateLienResponse{Lien: toLienProto(existingLien)}, true, nil
	}
	// Calculate available balance scoped to the lien's bucket (if any)
	availableMoney := s.calculateAvailableBalanceByBucket(ctx, existingLien.AccountID, existingLien.BucketID, account.Balance())
	resp := &pb.InitiateLienResponse{
		Lien:             toLienProto(existingLien),
		AvailableBalance: toMoneyAmount(availableMoney),
	}

	// Reconstruct valuation fields for price lock preservation on idempotent retry
	if existingLien.HasValuation() {
		if existingLien.ValuedAmount != nil {
			resp.ValuedAmount = &quantityv1.InstrumentAmount{
				Amount:         existingLien.ValuedAmount.Amount.String(),
				InstrumentCode: existingLien.ValuedAmount.InstrumentCode,
				Version:        1,
			}
		}
		if existingLien.ValuationAnalysis != nil {
			var analysis pb.ValuationAnalysis
			if err := protojson.Unmarshal(existingLien.ValuationAnalysis, &analysis); err == nil {
				resp.Basis = &analysis
			} else {
				s.logger.Warn("failed to unmarshal valuation analysis for idempotent response",
					"lien_id", existingLien.ID.String(), "error", err)
			}
		}
	}

	return resp, true, nil
}

// hydrateAccountWithBalance returns a new CurrentAccount with balance populated from Position Keeping.
// Position Keeping is the source of truth for account balances - this method MUST be called
// before any operation that requires the current balance.
func (s *Service) hydrateAccountWithBalance(ctx context.Context, account domain.CurrentAccount) (domain.CurrentAccount, error) {
	balanceCents, err := s.getAccountBalanceCents(ctx, account.AccountID())
	if err != nil {
		return domain.CurrentAccount{}, fmt.Errorf("failed to get balance from Position Keeping: %w", err)
	}

	// Create balance Money object
	balance, err := domain.NewAmountFromInstrument(account.InstrumentCode(), account.Dimension(), 0, balanceCents)
	if err != nil {
		return domain.CurrentAccount{}, fmt.Errorf("failed to create balance: %w", err)
	}

	// Calculate available balance: balance - active liens
	// Overdraft is now product-type behavior and no longer managed in the domain.
	availableBalance := s.calculateAvailableBalance(ctx, account.ID(), balance)

	// Use builder to reconstruct account with new balance
	return domain.NewCurrentAccountBuilder().
		WithID(account.ID()).
		WithAccountID(account.AccountID()).
		WithExternalIdentifier(account.ExternalIdentifier()).
		WithInstrumentCode(account.InstrumentCode()).
		WithDimension(account.Dimension()).
		WithPartyID(account.PartyID()).
		WithOrgPartyID(account.OrgPartyID()).
		WithBalance(balance).
		WithAvailableBalance(availableBalance).
		WithStatus(account.Status()).
		WithFreezeReason(account.FreezeReason()).
		WithStatusHistory(account.StatusHistory()).
		WithVersion(account.Version()).
		WithCreatedAt(account.CreatedAt()).
		WithUpdatedAt(account.UpdatedAt()).
		WithProductTypeCode(account.ProductTypeCode()).
		WithProductTypeVersion(account.ProductTypeVersion()).
		Build(), nil
}

// hydrateAccountWithPrefetchedBalance reconstructs account with a pre-fetched balance.
// Use this inside transactions to avoid making external service calls while holding database locks.
// The balanceCents parameter should be fetched from Position Keeping BEFORE entering the transaction.
func (s *Service) hydrateAccountWithPrefetchedBalance(account domain.CurrentAccount, balanceCents int64) (domain.CurrentAccount, error) {
	// Create balance Money object
	balance, err := domain.NewAmountFromInstrument(account.InstrumentCode(), account.Dimension(), 0, balanceCents)
	if err != nil {
		return domain.CurrentAccount{}, fmt.Errorf("failed to create balance: %w", err)
	}

	// For available balance, just use balance (no lien subtraction needed for ExecuteLien
	// since the lien will be executed immediately - the reservation converts to actual debit).
	// Overdraft is now product-type behavior and no longer managed in the domain.
	availableBalance := balance

	// Use builder to reconstruct account with new balance
	return domain.NewCurrentAccountBuilder().
		WithID(account.ID()).
		WithAccountID(account.AccountID()).
		WithExternalIdentifier(account.ExternalIdentifier()).
		WithInstrumentCode(account.InstrumentCode()).
		WithDimension(account.Dimension()).
		WithPartyID(account.PartyID()).
		WithOrgPartyID(account.OrgPartyID()).
		WithBalance(balance).
		WithAvailableBalance(availableBalance).
		WithStatus(account.Status()).
		WithFreezeReason(account.FreezeReason()).
		WithStatusHistory(account.StatusHistory()).
		WithVersion(account.Version()).
		WithCreatedAt(account.CreatedAt()).
		WithUpdatedAt(account.UpdatedAt()).
		WithProductTypeCode(account.ProductTypeCode()).
		WithProductTypeVersion(account.ProductTypeVersion()).
		Build(), nil
}

// currentAccountInstrumentCode is the instrument code used for Current Account balance queries.
// Current Account operates exclusively with GBP currency (CURRENCY dimension).
// The Internal Account service will use different instrument codes for multi-asset support.
const currentAccountInstrumentCode = "GBP"

// getAccountBalanceCents gets the account balance in cents from Position Keeping service.
// Position Keeping is the mandatory source of truth for all account balances.
// Uses the multi-asset API with explicit instrument_code="GBP" for currency operations.
// Returns balance in minor units (cents/pence).
func (s *Service) getAccountBalanceCents(ctx context.Context, accountID string) (int64, error) {
	resp, err := s.posKeepingClient.GetAccountBalance(ctx, &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:      accountID,
		BalanceType:    positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		InstrumentCode: currentAccountInstrumentCode, // Explicit instrument for multi-asset API
	})
	if err != nil {
		s.logger.Error("failed to get balance from Position Keeping",
			"account_id", accountID, "instrument_code", currentAccountInstrumentCode, "error", err)
		return 0, fmt.Errorf("failed to get balance from Position Keeping: %w", err)
	}

	if resp.Amount == nil || resp.Amount.Amount == "" {
		return 0, nil
	}

	// Validate that the response instrument matches the requested instrument.
	// This guards against configuration mismatches where Position Keeping might
	// return a different currency than expected.
	if resp.Amount.InstrumentCode != currentAccountInstrumentCode {
		s.logger.Error("instrument code mismatch in balance response",
			"account_id", accountID,
			"expected", currentAccountInstrumentCode,
			"received", resp.Amount.InstrumentCode)
		return 0, fmt.Errorf("%w: expected %s, got %s",
			ErrInstrumentCodeMismatch, currentAccountInstrumentCode, resp.Amount.InstrumentCode)
	}

	// Parse InstrumentAmount as decimal
	amount, err := decimal.NewFromString(resp.Amount.Amount)
	if err != nil {
		s.logger.Error("failed to parse balance amount",
			"account_id", accountID, "amount", resp.Amount.Amount, "error", err)
		return 0, fmt.Errorf("failed to parse balance amount: %w", err)
	}

	// Convert to minor units (cents/pence) - multiply by 100 for 2 decimal currencies.
	// Uses banker's rounding (round-to-even) which differs from half-up at .5 boundaries:
	// e.g., 0.015 -> 2 (rounds to even), 0.025 -> 2 (rounds to even), 0.035 -> 4 (rounds to even)
	cents := amount.Mul(decimal.NewFromInt(100)).RoundBank(0)

	// Check for overflow using int64 bounds
	maxInt64 := decimal.NewFromInt(math.MaxInt64)
	minInt64 := decimal.NewFromInt(math.MinInt64)
	if cents.GreaterThan(maxInt64) || cents.LessThan(minInt64) {
		return 0, ErrAmountOverflow
	}

	return cents.IntPart(), nil
}

// calculateAvailableBalance calculates available balance with active liens.
// Logs errors but returns best-effort values since primary operations already succeeded.
// Context is required for organization scoping in multi-org mode.
func (s *Service) calculateAvailableBalance(ctx context.Context, accountID uuid.UUID, currentBalance domain.Amount) domain.Amount {
	return s.calculateAvailableBalanceByBucket(ctx, accountID, "", currentBalance)
}

// calculateAvailableBalanceByBucket calculates available balance with active liens scoped to bucket.
// If bucketID is empty, calculates against all liens for the account (backward compatible).
// Logs errors but returns best-effort values since primary operations already succeeded.
// Context is required for organization scoping in multi-org mode.
func (s *Service) calculateAvailableBalanceByBucket(ctx context.Context, accountID uuid.UUID, bucketID string, currentBalance domain.Amount) domain.Amount {
	if s.lienRepo == nil {
		// Lien repository not configured - return balance without lien adjustment
		return currentBalance
	}

	var activeLiensTotal int64
	var err error
	if bucketID != "" {
		activeLiensTotal, err = s.lienRepo.SumActiveAmountByAccountIDAndBucket(ctx, accountID, bucketID)
	} else {
		activeLiensTotal, err = s.lienRepo.SumActiveAmountByAccountID(ctx, accountID)
	}
	if err != nil {
		s.logger.Error("failed to sum active liens for response",
			"account_id", accountID,
			"bucket_id", bucketID,
			"error", err)
		return currentBalance // Best effort: return current balance if liens can't be summed
	}

	currentBalanceCents, err := currentBalance.ToMinorUnits()
	if err != nil {
		s.logger.Error("failed to convert balance to minor units", "error", err)
		return currentBalance // Best effort
	}

	availableBalance := currentBalanceCents - activeLiensTotal
	availableMoney, err := domain.NewAmountFromInstrument(
		currentBalance.InstrumentCode(),
		currentBalance.Dimension(),
		currentBalance.Instrument().Precision,
		availableBalance,
	)
	if err != nil {
		s.logger.Error("failed to create available balance for response", "error", err)
		return currentBalance // Best effort
	}
	return availableMoney
}

// releaseReservation calls Position Keeping to release the reservation associated with a lien.
// This is best-effort: failures are logged but do not fail the lien operation, because the
// lien state transition has already been committed to the database.
func (s *Service) releaseReservation(ctx context.Context, lienID string, reason positionkeepingv1.ReservationStatus) {
	if s.posKeepingClient == nil {
		return
	}

	releaseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := s.posKeepingClient.ReleaseReservation(releaseCtx, &positionkeepingv1.ReleaseReservationRequest{
		LienId: lienID,
		Reason: reason,
	})
	if err != nil {
		// Best-effort: log and continue. The reservation will remain ACTIVE in Position Keeping
		// but the lien is already in its terminal state. A background reconciliation process
		// can clean up orphaned reservations.
		s.logger.Warn("failed to release Position Keeping reservation (best-effort)",
			"lien_id", lienID,
			"reason", reason.String(),
			"error", err)
	} else {
		s.logger.Info("released Position Keeping reservation",
			"lien_id", lienID,
			"reason", reason.String())
	}
}

// checkBasisDrift checks if a valued lien's valuation basis is stale and logs a warning.
// This detects situations where the price lock was computed long ago and the underlying
// rate may have changed significantly. The execution still proceeds with the price lock.
func (s *Service) checkBasisDrift(lien *domain.Lien) {
	if lien.ValuationAnalysis == nil {
		return
	}

	// Parse the knowledgeAt from the valuation analysis JSON.
	// The analysis contains a "knowledge_at" field from the valuation computation.
	var analysis struct {
		KnowledgeAt string `json:"knowledgeAt"`
	}
	if err := json.Unmarshal(lien.ValuationAnalysis, &analysis); err != nil {
		// Cannot parse - skip drift detection
		return
	}

	if analysis.KnowledgeAt == "" {
		return
	}

	knowledgeAt, err := time.Parse(time.RFC3339, analysis.KnowledgeAt)
	if err != nil {
		return
	}

	basisAge := time.Since(knowledgeAt)
	if basisAge > basisDriftThreshold {
		s.logger.Warn("VALUATION_STALE: lien valuation basis exceeds drift threshold",
			"lien_id", lien.ID.String(),
			"knowledge_at", knowledgeAt.Format(time.RFC3339),
			"basis_age_days", int(basisAge.Hours()/24),
			"threshold_days", int(basisDriftThreshold.Hours()/24))
	}
}

// toAmountBlockProto converts a domain Lien to proto AmountBlock.
// Liens are mapped to PENDING block type since they represent holds awaiting settlement.
func toAmountBlockProto(lien *domain.Lien) *pb.AmountBlock {
	block := &pb.AmountBlock{
		BlockId:   lien.ID.String(),
		Amount:    toMoneyAmount(lien.Amount),
		BlockType: pb.AmountBlockType_AMOUNT_BLOCK_TYPE_PENDING, // All liens are pending holds
		Purpose:   fmt.Sprintf("Payment Order: %s", lien.PaymentOrderReference),
	}

	// Only set expires_at if the lien has an expiry time
	if lien.ExpiresAt != nil {
		block.ExpiresAt = timestamppb.New(*lien.ExpiresAt)
	}

	return block
}
