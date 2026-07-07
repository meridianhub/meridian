package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/messaging"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// SagaRuntime defines the contract for invoking Starlark sagas.
type SagaRuntime interface {
	InvokeSaga(ctx context.Context, sagaName string, params map[string]interface{}) error
}

// EventPublisher defines the contract for publishing domain events.
type EventPublisher interface {
	// Publish writes the event to the outbox in its own transaction.
	Publish(ctx context.Context, topic string, event interface{}) error

	// PublishTx writes the event to the outbox within the provided transaction,
	// making the outbox row atomic with the domain write on the same transaction.
	PublishTx(ctx context.Context, tx *gorm.DB, topic string, event interface{}) error
}

// VarianceFinder retrieves variances for dispute validation.
type VarianceFinder interface {
	FindByID(ctx context.Context, varianceID uuid.UUID) (*domain.Variance, error)
	UpdateStatus(ctx context.Context, varianceID uuid.UUID, status domain.VarianceStatus) error
}

// DisputeCreatedEvent is published when a dispute is created.
type DisputeCreatedEvent struct {
	DisputeID  string `json:"dispute_id"`
	VarianceID string `json:"variance_id"`
	RunID      string `json:"run_id"`
	AccountID  string `json:"account_id"`
	Reason     string `json:"reason"`
	RaisedBy   string `json:"raised_by"`
}

// GetDisputeID returns the dispute ID for outbox event routing.
func (e DisputeCreatedEvent) GetDisputeID() string { return e.DisputeID }

// GetVarianceID returns the variance ID for outbox event routing.
func (e DisputeCreatedEvent) GetVarianceID() string { return e.VarianceID }

// GetRunID returns the run ID for outbox event routing.
func (e DisputeCreatedEvent) GetRunID() string { return e.RunID }

// GetAccountID returns the account ID for outbox event routing.
func (e DisputeCreatedEvent) GetAccountID() string { return e.AccountID }

// GetReason returns the dispute reason for outbox event routing.
func (e DisputeCreatedEvent) GetReason() string { return e.Reason }

// GetRaisedBy returns who raised the dispute for outbox event routing.
func (e DisputeCreatedEvent) GetRaisedBy() string { return e.RaisedBy }

// DisputeResolvedEvent is published when a dispute is resolved.
type DisputeResolvedEvent struct {
	DisputeID  string `json:"dispute_id"`
	VarianceID string `json:"variance_id"`
	RunID      string `json:"run_id"`
	AccountID  string `json:"account_id"`
	Action     string `json:"action"`
	Resolution string `json:"resolution"`
	ResolvedBy string `json:"resolved_by"`
}

// GetDisputeID returns the dispute ID for outbox event routing.
func (e DisputeResolvedEvent) GetDisputeID() string { return e.DisputeID }

// GetVarianceID returns the variance ID for outbox event routing.
func (e DisputeResolvedEvent) GetVarianceID() string { return e.VarianceID }

// GetRunID returns the run ID for outbox event routing.
func (e DisputeResolvedEvent) GetRunID() string { return e.RunID }

// GetAccountID returns the account ID for outbox event routing.
func (e DisputeResolvedEvent) GetAccountID() string { return e.AccountID }

// GetAction returns the dispute action for outbox event routing.
func (e DisputeResolvedEvent) GetAction() string { return e.Action }

// GetResolution returns the resolution for outbox event routing.
func (e DisputeResolvedEvent) GetResolution() string { return e.Resolution }

// GetResolvedBy returns who resolved the dispute for outbox event routing.
func (e DisputeResolvedEvent) GetResolvedBy() string { return e.ResolvedBy }

// InitiateDispute raises a formal dispute against a variance.
func (s *AccountReconciliationService) InitiateDispute(
	ctx context.Context,
	req *reconciliationv1.InitiateDisputeRequest,
) (*reconciliationv1.InitiateDisputeResponse, error) {
	if s.disputeRepo == nil {
		return nil, status.Error(codes.Unimplemented, "InitiateDispute not yet implemented")
	}

	varianceID, err := uuid.Parse(req.GetVarianceId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid variance_id: %v", err)
	}

	runID, err := uuid.Parse(req.GetRunId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid run_id: %v", err)
	}

	if err := s.validateVarianceExists(ctx, varianceID); err != nil {
		return nil, err
	}

	dispute, err := domain.NewDispute(
		varianceID,
		runID,
		req.GetAccountId(),
		req.GetReason(),
		req.GetRaisedBy(),
	)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid dispute: %v", err)
	}

	if req.GetAttributes() != nil {
		dispute.Attributes = req.GetAttributes()
	}

	if err := s.disputeRepo.CreateWithOutbox(ctx, dispute, s.disputeCreatedOutboxFn(ctx, dispute)); err != nil {
		slog.ErrorContext(ctx, "failed to create dispute", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to create dispute: %v", err)
	}

	s.markVarianceDisputed(ctx, varianceID)

	return &reconciliationv1.InitiateDisputeResponse{
		Dispute: toDisputeDetailProto(dispute),
	}, nil
}

// validateVarianceExists checks that the variance exists in the repository.
func (s *AccountReconciliationService) validateVarianceExists(ctx context.Context, varianceID uuid.UUID) error {
	if s.varianceRepo == nil {
		return nil
	}
	_, err := s.varianceRepo.FindByID(ctx, varianceID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return status.Errorf(codes.NotFound, "variance %s not found", varianceID)
		}
		return status.Errorf(codes.Internal, "failed to validate variance: %v", err)
	}
	return nil
}

// markVarianceDisputed updates the variance status to DISPUTED.
func (s *AccountReconciliationService) markVarianceDisputed(ctx context.Context, varianceID uuid.UUID) {
	if s.varianceRepo == nil {
		return
	}
	if err := s.varianceRepo.UpdateStatus(ctx, varianceID, domain.VarianceStatusDisputed); err != nil {
		slog.WarnContext(ctx, "failed to update variance status to disputed",
			"variance_id", varianceID, "error", err)
	}
}

// disputeCreatedOutboxFn returns a transaction callback that writes a
// DisputeCreatedEvent to the outbox within the dispute's insert transaction,
// making event delivery atomic with the domain write. Returns nil when no
// event publisher is configured (the insert then runs without an outbox write).
func (s *AccountReconciliationService) disputeCreatedOutboxFn(ctx context.Context, dispute *domain.Dispute) func(tx *gorm.DB) error {
	if s.eventPublisher == nil {
		return nil
	}
	event := DisputeCreatedEvent{
		DisputeID:  dispute.DisputeID.String(),
		VarianceID: dispute.VarianceID.String(),
		RunID:      dispute.RunID.String(),
		AccountID:  dispute.AccountID,
		Reason:     dispute.Reason,
		RaisedBy:   dispute.RaisedBy,
	}
	return func(tx *gorm.DB) error {
		return s.eventPublisher.PublishTx(ctx, tx, messaging.TopicDisputeCreated, event)
	}
}

// ControlDispute controls a dispute lifecycle (escalate, resolve, reject).
func (s *AccountReconciliationService) ControlDispute(
	ctx context.Context,
	req *reconciliationv1.ControlDisputeRequest,
) (*reconciliationv1.ControlDisputeResponse, error) {
	if s.disputeRepo == nil {
		return nil, status.Error(codes.Unimplemented, "ControlDispute not yet implemented")
	}

	disputeID, err := uuid.Parse(req.GetDisputeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid dispute_id: %v", err)
	}

	dispute, err := s.disputeRepo.FindByID(ctx, disputeID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "dispute %s not found", disputeID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve dispute: %v", err)
	}

	action := req.GetAction()
	if err := s.applyDisputeAction(ctx, dispute, action, req); err != nil {
		return nil, err
	}

	if err := s.disputeRepo.UpdateWithOutbox(ctx, dispute, s.disputeResolvedOutboxFn(ctx, dispute, action.String())); err != nil {
		slog.ErrorContext(ctx, "failed to update dispute", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to update dispute: %v", err)
	}

	return &reconciliationv1.ControlDisputeResponse{
		Dispute: toDisputeDetailProto(dispute),
	}, nil
}

// applyDisputeAction applies the requested control action to a dispute.
func (s *AccountReconciliationService) applyDisputeAction(
	ctx context.Context,
	dispute *domain.Dispute,
	action reconciliationv1.DisputeControlAction,
	req *reconciliationv1.ControlDisputeRequest,
) error {
	switch action { //nolint:exhaustive // UNSPECIFIED handled by default
	case reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_ESCALATE:
		if err := dispute.Escalate(); err != nil {
			return status.Errorf(codes.FailedPrecondition, "cannot escalate dispute: %v", err)
		}

	case reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_RESOLVE:
		if err := requireAdminOrOperator(ctx); err != nil {
			return err
		}
		if err := dispute.Resolve(req.GetResolution(), req.GetResolvedBy()); err != nil {
			return status.Errorf(codes.FailedPrecondition, "cannot resolve dispute: %v", err)
		}
		s.invokeReconciliationAdjustment(ctx, dispute)

	case reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_REJECT:
		if err := requireAdminOrOperator(ctx); err != nil {
			return err
		}
		if err := dispute.Reject(req.GetResolution(), req.GetResolvedBy()); err != nil {
			return status.Errorf(codes.FailedPrecondition, "cannot reject dispute: %v", err)
		}

	default:
		return status.Errorf(codes.InvalidArgument, "unknown dispute control action: %v", action)
	}

	return nil
}

// invokeReconciliationAdjustment triggers the reconciliation_adjustment saga for resolved disputes.
func (s *AccountReconciliationService) invokeReconciliationAdjustment(ctx context.Context, dispute *domain.Dispute) {
	if s.sagaRuntime == nil {
		return
	}
	sagaParams := map[string]interface{}{
		"variance_id": dispute.VarianceID.String(),
		"dispute_id":  dispute.DisputeID.String(),
		"account_id":  dispute.AccountID,
		"resolved_by": dispute.ResolvedBy,
		"resolution":  dispute.Resolution,
	}
	if err := s.sagaRuntime.InvokeSaga(ctx, "reconciliation_adjustment", sagaParams); err != nil {
		slog.WarnContext(ctx, "failed to invoke reconciliation_adjustment saga",
			"dispute_id", dispute.DisputeID, "error", err)
	}
}

// disputeResolvedOutboxFn returns a transaction callback that writes a
// DisputeResolvedEvent to the outbox within the dispute's update transaction,
// making event delivery atomic with the domain write. Returns nil when the
// dispute is not in a final state or no event publisher is configured, in which
// case the update runs without an outbox write.
func (s *AccountReconciliationService) disputeResolvedOutboxFn(ctx context.Context, dispute *domain.Dispute, actionStr string) func(tx *gorm.DB) error {
	if !dispute.Status.IsFinal() || s.eventPublisher == nil {
		return nil
	}
	event := DisputeResolvedEvent{
		DisputeID:  dispute.DisputeID.String(),
		VarianceID: dispute.VarianceID.String(),
		RunID:      dispute.RunID.String(),
		AccountID:  dispute.AccountID,
		Action:     actionStr,
		Resolution: dispute.Resolution,
		ResolvedBy: dispute.ResolvedBy,
	}
	return func(tx *gorm.DB) error {
		return s.eventPublisher.PublishTx(ctx, tx, messaging.TopicDisputeResolved, event)
	}
}

// RetrieveDispute retrieves a dispute by ID.
func (s *AccountReconciliationService) RetrieveDispute(
	ctx context.Context,
	req *reconciliationv1.RetrieveDisputeRequest,
) (*reconciliationv1.RetrieveDisputeResponse, error) {
	if s.disputeRepo == nil {
		return nil, status.Error(codes.Unimplemented, "RetrieveDispute not yet implemented")
	}

	disputeID, err := uuid.Parse(req.GetDisputeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid dispute_id: %v", err)
	}

	dispute, err := s.disputeRepo.FindByID(ctx, disputeID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "dispute %s not found", disputeID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve dispute: %v", err)
	}

	return &reconciliationv1.RetrieveDisputeResponse{
		Dispute: toDisputeDetailProto(dispute),
	}, nil
}

// requireAdminOrOperator checks that the caller has admin or operator role.
func requireAdminOrOperator(ctx context.Context) error {
	claims, ok := auth.GetClaimsFromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing authentication context")
	}
	if err := auth.CheckAnyRole(claims, auth.RoleAdmin, auth.RoleOperator); err != nil {
		return status.Errorf(codes.PermissionDenied, "resolve/reject dispute requires admin or operator role: %v", err)
	}
	return nil
}

// toDisputeDetailProto converts a domain Dispute to a proto DisputeDetail.
func toDisputeDetailProto(d *domain.Dispute) *reconciliationv1.DisputeDetail {
	detail := &reconciliationv1.DisputeDetail{
		DisputeId:  d.DisputeID.String(),
		VarianceId: d.VarianceID.String(),
		RunId:      d.RunID.String(),
		AccountId:  d.AccountID,
		Status:     toDisputeStatusProto(d.Status),
		Reason:     d.Reason,
		Resolution: d.Resolution,
		RaisedBy:   d.RaisedBy,
		ResolvedBy: d.ResolvedBy,
		Attributes: d.Attributes,
		CreatedAt:  timestamppb.New(d.CreatedAt),
		UpdatedAt:  timestamppb.New(d.UpdatedAt),
	}

	if d.ResolvedAt != nil {
		detail.ResolvedAt = timestamppb.New(*d.ResolvedAt)
	}

	return detail
}

// toDisputeStatusProto converts a domain DisputeStatus to proto enum.
func toDisputeStatusProto(s domain.DisputeStatus) reconciliationv1.DisputeStatus {
	switch s {
	case domain.DisputeStatusOpen:
		return reconciliationv1.DisputeStatus_DISPUTE_STATUS_OPEN
	case domain.DisputeStatusUnderReview:
		return reconciliationv1.DisputeStatus_DISPUTE_STATUS_UNDER_REVIEW
	case domain.DisputeStatusEscalated:
		return reconciliationv1.DisputeStatus_DISPUTE_STATUS_ESCALATED
	case domain.DisputeStatusResolved:
		return reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED
	case domain.DisputeStatusRejected:
		return reconciliationv1.DisputeStatus_DISPUTE_STATUS_REJECTED
	default:
		return reconciliationv1.DisputeStatus_DISPUTE_STATUS_UNSPECIFIED
	}
}
