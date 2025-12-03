// Package main provides PaymentOrder uniqueness verification for the Horizon Integrity Proof demo.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
)

// OrderAuditConfig holds configuration for the PaymentOrder uniqueness audit.
type OrderAuditConfig struct {
	// AccountID is the debtor account to query
	AccountID string
	// IdempotencyKey is the key used for the demo payment
	IdempotencyKey string
	// Logger for structured logging
	Logger *slog.Logger
}

// OrderAuditResult captures the outcome of the PaymentOrder uniqueness verification.
type OrderAuditResult struct {
	// AccountID is the audited account
	AccountID string
	// IdempotencyKey is the key that was verified
	IdempotencyKey string
	// OrdersFound is the number of PaymentOrders matching the IdempotencyKey
	OrdersFound int
	// MatchingOrders contains details of orders with matching idempotency key
	MatchingOrders []PaymentOrderSummary
	// DuplicateOrdersFound indicates if more than one order has the same idempotency key
	DuplicateOrdersFound bool
	// OrderStatus describes the uniqueness verification outcome
	OrderStatus OrderStatus
	// Verdict is the audit determination for this check
	Verdict AuditVerdict
	// Error captures any error during audit (nil on success)
	Error error
}

// PaymentOrderSummary holds key fields from a PaymentOrder for audit reporting.
type PaymentOrderSummary struct {
	// PaymentOrderID is the unique identifier
	PaymentOrderID string
	// Status is the order status (COMPLETED, EXECUTING, etc.)
	Status string
	// LienID is the associated reservation ID
	LienID string
	// GatewayReferenceID is the external gateway reference
	GatewayReferenceID string
	// CreatedAt is when the order was created
	CreatedAt string
	// UpdatedAt is when the order was last modified
	UpdatedAt string
}

// OrderStatus represents the outcome of PaymentOrder uniqueness verification.
type OrderStatus int

const (
	// OrderStatusUniqueFound indicates exactly one order was found (correct).
	OrderStatusUniqueFound OrderStatus = iota
	// OrderStatusDuplicatesFound indicates multiple orders with same idempotency key (breach).
	OrderStatusDuplicatesFound
	// OrderStatusNoneFound indicates no orders were found (saga never persisted).
	OrderStatusNoneFound
)

// OrderStatus string constants.
const (
	orderStatusUniqueFoundStr     = "UNIQUE_FOUND"
	orderStatusDuplicatesFoundStr = "DUPLICATES_FOUND"
	orderStatusNoneFoundStr       = "NONE_FOUND"
)

func (s OrderStatus) String() string {
	switch s {
	case OrderStatusUniqueFound:
		return orderStatusUniqueFoundStr
	case OrderStatusDuplicatesFound:
		return orderStatusDuplicatesFoundStr
	case OrderStatusNoneFound:
		return orderStatusNoneFoundStr
	default:
		return statusUnknownStr
	}
}

// PaymentOrder audit errors.
var (
	ErrOrderAuditConfigInvalid = errors.New("invalid order audit configuration")
	ErrOrderAuditListFailed    = errors.New("failed to list payment orders")
	ErrOrderAuditDuplicates    = errors.New("idempotency breach: multiple orders with same key")
	ErrOrderAuditNoneFound     = errors.New("no payment orders found: saga never persisted")
)

// RunOrderAudit executes the PaymentOrder uniqueness verification.
// This queries PaymentOrders by debtor_account_id and filters by IdempotencyKey.
//
// Assertion branches:
// 1. Exactly 1 order found: PASS - idempotency working correctly
// 2. More than 1 order found: FAIL - idempotency breach detected
// 3. No orders found: FAIL - saga never persisted the order
func RunOrderAudit(ctx context.Context, clients *Clients, cfg *OrderAuditConfig) (*OrderAuditResult, error) {
	if err := validateOrderAuditConfig(cfg); err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	result := &OrderAuditResult{
		AccountID:      cfg.AccountID,
		IdempotencyKey: cfg.IdempotencyKey,
		MatchingOrders: make([]PaymentOrderSummary, 0),
	}

	logger.Info("order audit: starting uniqueness verification",
		"account_id", cfg.AccountID,
		"idempotency_key", cfg.IdempotencyKey,
	)

	// Query PaymentOrders for the debtor account
	listResp, err := clients.PaymentOrder.ListPaymentOrders(ctx, &paymentorderv1.ListPaymentOrdersRequest{
		DebtorAccountId: cfg.AccountID,
	})
	if err != nil {
		result.Error = fmt.Errorf("%w: %w", ErrOrderAuditListFailed, err)
		result.Verdict = AuditVerdictError
		logger.Error("order audit: failed to list payment orders",
			"account_id", cfg.AccountID,
			"error", err,
		)
		return result, result.Error
	}

	// Filter orders by idempotency key
	var matchingOrders []*paymentorderv1.PaymentOrder
	for _, order := range listResp.GetPaymentOrders() {
		if order.GetIdempotencyKey() == cfg.IdempotencyKey {
			matchingOrders = append(matchingOrders, order)
		}
	}

	result.OrdersFound = len(matchingOrders)

	// Convert matching orders to summaries
	for _, order := range matchingOrders {
		summary := PaymentOrderSummary{
			PaymentOrderID:     order.GetPaymentOrderId(),
			Status:             order.GetStatus().String(),
			LienID:             order.GetLienId(),
			GatewayReferenceID: order.GetGatewayReferenceId(),
		}
		if order.GetCreatedAt() != nil {
			summary.CreatedAt = order.GetCreatedAt().AsTime().String()
		}
		if order.GetUpdatedAt() != nil {
			summary.UpdatedAt = order.GetUpdatedAt().AsTime().String()
		}
		result.MatchingOrders = append(result.MatchingOrders, summary)
	}

	logger.Info("order audit: found orders with matching idempotency key",
		"account_id", cfg.AccountID,
		"idempotency_key", cfg.IdempotencyKey,
		"orders_found", result.OrdersFound,
	)

	// Perform assertion branches
	switch {
	case result.OrdersFound == 1:
		// PASS: Exactly one order found - idempotency working correctly
		result.DuplicateOrdersFound = false
		result.OrderStatus = OrderStatusUniqueFound
		result.Verdict = AuditVerdictPass

		order := matchingOrders[0]
		logger.Info("order audit: PASS - unique order found",
			"account_id", cfg.AccountID,
			"idempotency_key", cfg.IdempotencyKey,
			"payment_order_id", order.GetPaymentOrderId(),
			"status", order.GetStatus().String(),
		)

		// Verify order is in a valid state (COMPLETED, EXECUTING, or RESERVED)
		status := order.GetStatus()
		if status != paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED &&
			status != paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_EXECUTING &&
			status != paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_RESERVED {
			logger.Warn("order audit: order in unexpected state",
				"payment_order_id", order.GetPaymentOrderId(),
				"status", status.String(),
			)
		}

	case result.OrdersFound > 1:
		// FAIL: Multiple orders with same idempotency key - breach detected
		result.DuplicateOrdersFound = true
		result.OrderStatus = OrderStatusDuplicatesFound
		result.Verdict = AuditVerdictFail
		result.Error = fmt.Errorf("%w: found %d orders with key %s",
			ErrOrderAuditDuplicates, result.OrdersFound, cfg.IdempotencyKey)

		logger.Error("order audit: FAIL - duplicate orders detected",
			"account_id", cfg.AccountID,
			"idempotency_key", cfg.IdempotencyKey,
			"orders_found", result.OrdersFound,
		)

	default:
		// FAIL: No orders found - saga never persisted
		result.DuplicateOrdersFound = false
		result.OrderStatus = OrderStatusNoneFound
		result.Verdict = AuditVerdictFail
		result.Error = fmt.Errorf("%w: no orders for key %s",
			ErrOrderAuditNoneFound, cfg.IdempotencyKey)

		logger.Error("order audit: FAIL - no orders found",
			"account_id", cfg.AccountID,
			"idempotency_key", cfg.IdempotencyKey,
		)
	}

	return result, result.Error
}

// validateOrderAuditConfig validates the order audit configuration.
func validateOrderAuditConfig(cfg *OrderAuditConfig) error {
	if cfg == nil {
		return fmt.Errorf("%w: config is nil", ErrOrderAuditConfigInvalid)
	}

	if cfg.AccountID == "" {
		return fmt.Errorf("%w: AccountID is required", ErrOrderAuditConfigInvalid)
	}

	if cfg.IdempotencyKey == "" {
		return fmt.Errorf("%w: IdempotencyKey is required", ErrOrderAuditConfigInvalid)
	}

	return nil
}

// NewOrderAuditConfig creates an OrderAuditConfig with the given parameters.
func NewOrderAuditConfig(accountID, idempotencyKey string, logger *slog.Logger) *OrderAuditConfig {
	if logger == nil {
		logger = slog.Default()
	}
	return &OrderAuditConfig{
		AccountID:      accountID,
		IdempotencyKey: idempotencyKey,
		Logger:         logger,
	}
}
