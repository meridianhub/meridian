// Package topics provides a centralized registry of all Kafka topic names used across Meridian services.
//
// All topics follow the naming convention: <service>.<event-name>.<version>
// as defined in ADR-0004.
//
// This package is the single source of truth for topic names and is used both by
// service code and by the AsyncAPI specification generator.
package topics

// Audit topics
const (
	// AuditEventsV1 is the Kafka topic for audit events.
	AuditEventsV1 = "audit.events.v1"
	// AuditEventsDLQV1 is the dead letter queue for failed audit events.
	AuditEventsDLQV1 = "audit.events.v1.dlq"
)

// Current Account topics
const (
	// CurrentAccountAccountFrozenV1 is the Kafka topic for account frozen events.
	CurrentAccountAccountFrozenV1 = "current-account.account-frozen.v1"
	// CurrentAccountAccountUnfrozenV1 is the Kafka topic for account unfrozen events.
	CurrentAccountAccountUnfrozenV1 = "current-account.account-unfrozen.v1"
	// CurrentAccountAccountClosedV1 is the Kafka topic for account closed events.
	CurrentAccountAccountClosedV1 = "current-account.account-closed.v1"
	// CurrentAccountWithdrawalStatusV1 is the Kafka topic for withdrawal status events.
	CurrentAccountWithdrawalStatusV1 = "current-account.withdrawal-status.v1"
)

// Financial Accounting topics
const (
	// FinancialAccountingBookingLogControlledV1 is the canonical Kafka topic for booking log
	// control events, following the standard <service>.<event-name>.<version> naming convention.
	FinancialAccountingBookingLogControlledV1 = "financial-accounting.booking-log-controlled.v1"

	// FinancialAccountingBookingLogControlled is the legacy Kafka topic for booking log control events.
	// Retained for dual-publishing during migration.
	//
	// Deprecated: Does not follow the standard naming convention. Use FinancialAccountingBookingLogControlledV1.
	FinancialAccountingBookingLogControlled = "financial-accounting.booking-log.controlled"
)

// Market Information topics
const (
	// MarketInformationObservationRecordedV1 is the Kafka topic for observation recorded events.
	MarketInformationObservationRecordedV1 = "market-information.observation-recorded.v1"
)

// Payment Order topics
const (
	// PaymentOrderInitiatedV1 is the Kafka topic for payment order initiated events.
	PaymentOrderInitiatedV1 = "payment-order.initiated.v1"
	// PaymentOrderReservedV1 is the Kafka topic for payment order reserved events.
	PaymentOrderReservedV1 = "payment-order.reserved.v1"
	// PaymentOrderExecutingV1 is the Kafka topic for payment order executing events.
	PaymentOrderExecutingV1 = "payment-order.executing.v1"
	// PaymentOrderCompletedV1 is the Kafka topic for payment order completed events.
	PaymentOrderCompletedV1 = "payment-order.completed.v1"
	// PaymentOrderFailedV1 is the Kafka topic for payment order failed events.
	PaymentOrderFailedV1 = "payment-order.failed.v1"
	// PaymentOrderCancelledV1 is the Kafka topic for payment order cancelled events.
	PaymentOrderCancelledV1 = "payment-order.cancelled.v1"
	// PaymentOrderReversedV1 is the Kafka topic for payment order reversed events.
	PaymentOrderReversedV1 = "payment-order.reversed.v1"
)

// Position Keeping topics
const (
	// PositionKeepingTransactionCapturedV1 is the Kafka topic for transaction captured events.
	PositionKeepingTransactionCapturedV1 = "position-keeping.transaction-captured.v1"
	// PositionKeepingTransactionAmendedV1 is the Kafka topic for transaction amended events.
	PositionKeepingTransactionAmendedV1 = "position-keeping.transaction-amended.v1"
	// PositionKeepingTransactionReconciledV1 is the Kafka topic for transaction reconciled events.
	PositionKeepingTransactionReconciledV1 = "position-keeping.transaction-reconciled.v1"
	// PositionKeepingTransactionPostedV1 is the Kafka topic for transaction posted events.
	PositionKeepingTransactionPostedV1 = "position-keeping.transaction-posted.v1"
	// PositionKeepingTransactionRejectedV1 is the Kafka topic for transaction rejected events.
	PositionKeepingTransactionRejectedV1 = "position-keeping.transaction-rejected.v1"
	// PositionKeepingTransactionFailedV1 is the Kafka topic for transaction failed events.
	PositionKeepingTransactionFailedV1 = "position-keeping.transaction-failed.v1"
	// PositionKeepingTransactionCancelledV1 is the Kafka topic for transaction cancelled events.
	PositionKeepingTransactionCancelledV1 = "position-keeping.transaction-cancelled.v1"
	// PositionKeepingBulkTransactionCapturedV1 is the Kafka topic for bulk transaction captured events.
	PositionKeepingBulkTransactionCapturedV1 = "position-keeping.bulk-transaction-captured.v1"
	// PositionKeepingOpeningBalanceRecordedV1 is the Kafka topic for opening balance recorded events.
	PositionKeepingOpeningBalanceRecordedV1 = "position-keeping.opening-balance-recorded.v1"
)

// Party topics
const (
	// PartyCreatedV1 is the Kafka topic for party created events.
	PartyCreatedV1 = "party.created.v1"
	// PartyUpdatedV1 is the Kafka topic for party updated events.
	PartyUpdatedV1 = "party.updated.v1"
	// PartyControlledV1 is the Kafka topic for party controlled events (suspend, terminate, reactivate).
	PartyControlledV1 = "party.controlled.v1"
	// PartyVerificationCompletedV1 is the Kafka topic for party verification completed events.
	PartyVerificationCompletedV1 = "party.verification-completed.v1"
)

// Internal Account topics
const (
	// InternalAccountFacilityCreatedV1 is the Kafka topic for internal account facility created events.
	InternalAccountFacilityCreatedV1 = "internal-account.facility-created.v1"
	// InternalAccountBookingCreatedV1 is the Kafka topic for internal account booking created events.
	InternalAccountBookingCreatedV1 = "internal-account.booking-created.v1"
)

// Operational Gateway topics
const (
	// OperationalGatewayInstructionCreatedV1 is the Kafka topic for instruction created events.
	OperationalGatewayInstructionCreatedV1 = "operational-gateway.instruction-created.v1"
	// OperationalGatewayInstructionDispatchedV1 is the Kafka topic for instruction dispatched events.
	OperationalGatewayInstructionDispatchedV1 = "operational-gateway.instruction-dispatched.v1"
	// OperationalGatewayInstructionDeliveredV1 is the Kafka topic for instruction delivered events.
	OperationalGatewayInstructionDeliveredV1 = "operational-gateway.instruction-delivered.v1"
	// OperationalGatewayInstructionAcknowledgedV1 is the Kafka topic for instruction acknowledged events.
	OperationalGatewayInstructionAcknowledgedV1 = "operational-gateway.instruction-acknowledged.v1"
	// OperationalGatewayInstructionFailedV1 is the Kafka topic for instruction failed events.
	OperationalGatewayInstructionFailedV1 = "operational-gateway.instruction-failed.v1"
	// OperationalGatewayInstructionExpiredV1 is the Kafka topic for instruction expired events.
	OperationalGatewayInstructionExpiredV1 = "operational-gateway.instruction-expired.v1"
	// OperationalGatewayInstructionCancelledV1 is the Kafka topic for instruction cancelled events.
	OperationalGatewayInstructionCancelledV1 = "operational-gateway.instruction-cancelled.v1"
)

// Financial Gateway topics
const (
	// FinancialGatewayPaymentCapturedV1 is the Kafka topic for payment captured events.
	// Published when a Stripe payment_intent.succeeded webhook is received and validated.
	FinancialGatewayPaymentCapturedV1 = "financial-gateway.payment-captured.v1"

	// FinancialGatewayPaymentFailedV1 is the Kafka topic for payment failed events.
	// Published when a Stripe payment_intent.payment_failed webhook is received and validated.
	FinancialGatewayPaymentFailedV1 = "financial-gateway.payment-failed.v1"

	// FinancialGatewayPaymentRefundedV1 is the Kafka topic for payment refunded events.
	// Published when a Stripe charge.refunded webhook is received and validated.
	FinancialGatewayPaymentRefundedV1 = "financial-gateway.payment-refunded.v1"

	// FinancialGatewayPaymentDisputedV1 is the Kafka topic for payment disputed events.
	// Published when a Stripe charge.dispute.created webhook is received and validated.
	FinancialGatewayPaymentDisputedV1 = "financial-gateway.payment-disputed.v1"
)

// Reconciliation topics
const (
	// ReconciliationRunStartedV1 is the Kafka topic for reconciliation run started events.
	ReconciliationRunStartedV1 = "reconciliation.run-started.v1"
	// ReconciliationRunCompletedV1 is the Kafka topic for reconciliation run completed events.
	ReconciliationRunCompletedV1 = "reconciliation.run-completed.v1"
	// ReconciliationVarianceDetectedV1 is the Kafka topic for variance detected events.
	ReconciliationVarianceDetectedV1 = "reconciliation.variance-detected.v1"
	// ReconciliationPositionLockRequestedV1 is the Kafka topic for position lock requested events.
	ReconciliationPositionLockRequestedV1 = "reconciliation.position-lock-requested.v1"
	// ReconciliationDisputeCreatedV1 is the Kafka topic for dispute created events.
	ReconciliationDisputeCreatedV1 = "reconciliation.dispute-created.v1"
	// ReconciliationDisputeResolvedV1 is the Kafka topic for dispute resolved events.
	ReconciliationDisputeResolvedV1 = "reconciliation.dispute-resolved.v1"
)

// All returns a slice of all canonical topic names registered in this package.
// Deprecated topics are excluded. This is used for validation and AsyncAPI generation.
func All() []string {
	return []string{
		AuditEventsV1,
		AuditEventsDLQV1,
		CurrentAccountAccountFrozenV1,
		CurrentAccountAccountUnfrozenV1,
		CurrentAccountAccountClosedV1,
		CurrentAccountWithdrawalStatusV1,
		FinancialAccountingBookingLogControlledV1,
		MarketInformationObservationRecordedV1,
		PaymentOrderInitiatedV1,
		PaymentOrderReservedV1,
		PaymentOrderExecutingV1,
		PaymentOrderCompletedV1,
		PaymentOrderFailedV1,
		PaymentOrderCancelledV1,
		PaymentOrderReversedV1,
		PositionKeepingTransactionCapturedV1,
		PositionKeepingTransactionAmendedV1,
		PositionKeepingTransactionReconciledV1,
		PositionKeepingTransactionPostedV1,
		PositionKeepingTransactionRejectedV1,
		PositionKeepingTransactionFailedV1,
		PositionKeepingTransactionCancelledV1,
		PositionKeepingBulkTransactionCapturedV1,
		PositionKeepingOpeningBalanceRecordedV1,
		PartyCreatedV1,
		PartyUpdatedV1,
		PartyControlledV1,
		PartyVerificationCompletedV1,
		InternalAccountFacilityCreatedV1,
		InternalAccountBookingCreatedV1,
		ReconciliationRunStartedV1,
		ReconciliationRunCompletedV1,
		ReconciliationVarianceDetectedV1,
		ReconciliationPositionLockRequestedV1,
		ReconciliationDisputeCreatedV1,
		ReconciliationDisputeResolvedV1,
		OperationalGatewayInstructionCreatedV1,
		OperationalGatewayInstructionDispatchedV1,
		OperationalGatewayInstructionDeliveredV1,
		OperationalGatewayInstructionAcknowledgedV1,
		OperationalGatewayInstructionFailedV1,
		OperationalGatewayInstructionExpiredV1,
		OperationalGatewayInstructionCancelledV1,
		FinancialGatewayPaymentCapturedV1,
		FinancialGatewayPaymentFailedV1,
		FinancialGatewayPaymentRefundedV1,
		FinancialGatewayPaymentDisputedV1,
	}
}
