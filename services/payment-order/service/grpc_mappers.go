package service

import (
	"context"
	"strings"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// publishEvent publishes a Kafka event if the publisher is configured.
// This is best-effort/fire-and-forget: errors are logged but not retried or persisted.
// Failed events are NOT automatically recovered. For guaranteed delivery, consider
// implementing a transactional outbox pattern in the future.
func (s *Service) publishEvent(ctx context.Context, topic string, key string, event proto.Message) {
	if s.kafkaPublisher == nil {
		return
	}
	if err := s.kafkaPublisher.Publish(ctx, topic, key, event); err != nil {
		s.logger.Error("failed to publish event",
			"topic", topic,
			"key", key,
			"error", err)
	} else {
		s.logger.Info("published event",
			"topic", topic,
			"key", key)
	}
}

// toProto converts a domain PaymentOrder to proto PaymentOrder
func toProto(po *domain.PaymentOrder) *pb.PaymentOrder {
	proto := &pb.PaymentOrder{
		PaymentOrderId:        po.ID.String(),
		DebtorAccountId:       po.DebtorAccountID,
		CreditorReference:     po.CreditorReference,
		Amount:                toMoneyAmount(po.Amount),
		Status:                mapStatusToProto(po.Status),
		LienId:                po.LienID,
		GatewayReferenceId:    po.GatewayReferenceID,
		CorrelationId:         po.CorrelationID,
		IdempotencyKey:        po.IdempotencyKey,
		FailureReason:         po.FailureReason,
		CreatedAt:             timestamppb.New(po.CreatedAt),
		UpdatedAt:             timestamppb.New(po.UpdatedAt),
		Version:               int64(po.Version),
		LedgerBookingId:       po.LedgerBookingID,
		CausationId:           po.CausationID,
		ErrorCode:             po.ErrorCode,
		LienExecutionStatus:   mapLienExecutionStatusToProto(po.LienExecutionStatus),
		LienExecutionAttempts: safeIntToInt32(po.LienExecutionAttempts),
		LienExecutionError:    po.LienExecutionError,
	}

	// Set optional timestamps
	if po.ReservedAt != nil {
		proto.ReservedAt = timestamppb.New(*po.ReservedAt)
	}
	if po.ExecutingAt != nil {
		proto.ExecutingAt = timestamppb.New(*po.ExecutingAt)
	}
	if po.CompletedAt != nil {
		proto.CompletedAt = timestamppb.New(*po.CompletedAt)
	}
	if po.FailedAt != nil {
		proto.FailedAt = timestamppb.New(*po.FailedAt)
	}
	if po.CancelledAt != nil {
		proto.CancelledAt = timestamppb.New(*po.CancelledAt)
	}
	if po.ReversedAt != nil {
		proto.ReversedAt = timestamppb.New(*po.ReversedAt)
	}

	return proto
}

// toMoneyAmount converts domain Money (in cents) to proto MoneyAmount (units + nanos).
// The conversion splits cents into units (dollars) and nanos:
//   - units = cents / 100 (integer division)
//   - nanos = (cents % 100) * 10^7 (remainder converted to nanos)
//
// This is a lossless conversion since cents are exact representations.
// The inverse operation protoToMoney uses symmetric rounding for any sub-cent precision.
func toMoneyAmount(m domain.Money) *commonpb.MoneyAmount {
	// Use domain.ToMinorUnits for the conversion
	amountCents := domain.ToMinorUnits(m)
	units := amountCents / 100
	remainder := amountCents % 100
	// #nosec G115 - remainder is always -99 to 99
	nanos := int32(remainder * nanosPerCent)

	return &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: domain.CurrencyCode(m),
			Units:        units,
			Nanos:        nanos,
		},
	}
}

// safeMinorUnits converts Money to minor units (cents).
// Used for logging and metrics where returning an error is not practical.
// The new quantity package uses arbitrary precision, so overflow is not a concern.
func safeMinorUnits(m domain.Money) int64 {
	return domain.ToMinorUnits(m)
}

// protoToMoney converts proto MoneyAmount to domain Money
func protoToMoney(amount *commonpb.MoneyAmount) (domain.Money, error) {
	if amount == nil || amount.Amount == nil {
		return domain.Money{}, ErrAmountRequired
	}

	// Validate nanos is within bounds (per Google Money spec)
	const maxNanos = 999999999
	if amount.Amount.Nanos < -maxNanos || amount.Amount.Nanos > maxNanos {
		return domain.Money{}, ErrInvalidNanos
	}

	// Convert to cents with symmetric rounding (round half away from zero)
	unitsCents := amount.Amount.Units * 100
	var nanosCents int64
	if amount.Amount.Nanos >= 0 {
		nanosCents = int64((amount.Amount.Nanos + nanosRoundingOffset) / nanosPerCent)
	} else {
		nanosCents = int64((amount.Amount.Nanos - nanosRoundingOffset) / nanosPerCent)
	}
	totalCents := unitsCents + nanosCents

	return domain.NewMoney(amount.Amount.CurrencyCode, totalCents)
}

// mapStatusToProto maps domain status to proto status
func mapStatusToProto(status domain.PaymentOrderStatus) pb.PaymentOrderStatus {
	switch status {
	case domain.PaymentOrderStatusInitiated:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_INITIATED
	case domain.PaymentOrderStatusReserved:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_RESERVED
	case domain.PaymentOrderStatusExecuting:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_EXECUTING
	case domain.PaymentOrderStatusCompleted:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED
	case domain.PaymentOrderStatusFailed:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_FAILED
	case domain.PaymentOrderStatusCancelled:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_CANCELLED
	case domain.PaymentOrderStatusReversed:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_REVERSED
	default:
		return pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_UNSPECIFIED
	}
}

// mapLienExecutionStatusToProto maps domain LienExecutionStatus to proto
func mapLienExecutionStatusToProto(status domain.LienExecutionStatus) pb.LienExecutionStatus {
	switch status {
	case domain.LienExecutionStatusUnspecified:
		return pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_UNSPECIFIED
	case domain.LienExecutionStatusPending:
		return pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_PENDING
	case domain.LienExecutionStatusSucceeded:
		return pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_SUCCEEDED
	case domain.LienExecutionStatusFailed:
		return pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_FAILED
	default:
		return pb.LienExecutionStatus_LIEN_EXECUTION_STATUS_UNSPECIFIED
	}
}

// safeIntToInt32 safely converts an int to int32 with bounds checking.
// Returns math.MaxInt32 if the value exceeds int32 range.
// This is used for lien execution attempts which should never exceed
// a small number in practice, but we need safe conversion for gosec.
func safeIntToInt32(n int) int32 {
	const maxInt32 = 1<<31 - 1
	if n > maxInt32 {
		return maxInt32
	}
	if n < -maxInt32-1 {
		return -maxInt32 - 1
	}
	return int32(n)
}

// extractGatewayIDFromRef extracts the gateway identifier from a gateway reference ID.
//
// Gateway Reference ID Format:
// Real gateways should use the format "{gateway_id}-{unique_reference}", e.g.:
//   - "stripe-pm_1234abcd" -> returns "stripe"
//   - "adyen-PSP-REF-123"  -> returns "adyen"
//
// Mock/Test gateways use special prefixes:
//   - "GW-{uuid}"      -> returns "mock" (mock gateway format)
//   - "gateway-{ref}"  -> returns "mock" (test helper format)
//
// Returns "unknown" for empty or invalid references.
func extractGatewayIDFromRef(gatewayRefID string) string {
	if gatewayRefID == "" {
		return "unknown"
	}

	// Detect mock/test gateway patterns
	if strings.HasPrefix(gatewayRefID, "GW-") || strings.HasPrefix(gatewayRefID, "gateway-") {
		return "mock"
	}

	// For other gateways, extract the prefix before the first dash
	parts := strings.SplitN(gatewayRefID, "-", 2)
	if len(parts) > 0 && parts[0] != "" {
		return strings.ToLower(parts[0])
	}
	return "unknown"
}
