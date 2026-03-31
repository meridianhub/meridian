package email

import (
	"context"
	"log/slog"
)

// DeliveryStatusRecorder encapsulates the business logic for recording email
// delivery status updates from webhook callbacks. It updates the audit log and
// adds addresses to the suppression list on bounces/complaints.
type DeliveryStatusRecorder struct {
	auditRepo       AuditRepository
	suppressionRepo SuppressionRepository
	metrics         *Metrics
	logger          *slog.Logger
}

// NewDeliveryStatusRecorder creates a new recorder. suppressionRepo and metrics
// may be nil to skip suppression recording and metric tracking respectively.
func NewDeliveryStatusRecorder(auditRepo AuditRepository, suppressionRepo SuppressionRepository, metrics *Metrics, logger *slog.Logger) *DeliveryStatusRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &DeliveryStatusRecorder{
		auditRepo:       auditRepo,
		suppressionRepo: suppressionRepo,
		metrics:         metrics,
		logger:          logger.With("component", "delivery-status-recorder"),
	}
}

// RecordDeliveryStatus records a delivery status update for an email identified
// by providerID. If the status is BOUNCED or COMPLAINED and a suppression
// repository is configured, the recipient addresses are added to the
// suppression list using data from the audit trail.
//
// Returns ErrAuditEntryNotFound if no audit entry exists for the providerID.
func (r *DeliveryStatusRecorder) RecordDeliveryStatus(ctx context.Context, providerID string, status AuditStatus, metadata map[string]any) error {
	// Record audit entry first - this is the primary concern.
	if err := r.auditRepo.RecordByProviderID(ctx, providerID, status, metadata); err != nil {
		return err
	}

	needMetric := status == AuditStatusComplained && r.metrics != nil
	needSuppression := r.suppressionRepo != nil && (status == AuditStatusBounced || status == AuditStatusComplained)

	if !needMetric && !needSuppression {
		return nil
	}

	// Resolve audit entries once for both metrics and suppression.
	entries, err := r.auditRepo.FindByProviderID(ctx, providerID)
	if err != nil || len(entries) == 0 {
		r.logger.WarnContext(ctx, "cannot resolve audit entries for post-delivery processing",
			"provider_id", providerID,
			"error", err,
		)
		return nil
	}

	// Use the oldest entry (original SENT record) for tenant/recipient info.
	original := entries[len(entries)-1]

	if needMetric {
		r.metrics.RecordEmailComplaint(original.TenantID)
	}

	if needSuppression {
		r.recordSuppressions(ctx, providerID, status, original)
	}

	return nil
}

// recordSuppressions writes suppression records for each recipient. Errors are
// logged but not propagated - the audit record is the primary concern.
func (r *DeliveryStatusRecorder) recordSuppressions(ctx context.Context, providerID string, status AuditStatus, original AuditEntry) {
	suppType := SuppressionBounce
	if status == AuditStatusComplained {
		suppType = SuppressionComplaint
	}

	for _, addr := range original.ToAddresses {
		if err := r.suppressionRepo.AddSuppression(ctx, &SuppressionEntry{
			EmailAddress:    addr,
			SuppressionType: suppType,
			ProviderID:      providerID,
			TenantID:        original.TenantID,
		}); err != nil {
			r.logger.WarnContext(ctx, "failed to add suppression",
				"address", addr,
				"provider_id", providerID,
				"error", err,
			)
		}
	}
}
