package stripe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	stripego "github.com/stripe/stripe-go/v82"

	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Sentinel errors for the Stripe payment intent adapter.
var (
	ErrMissingStripeAccount  = errors.New("stripe connected account ID not found in context")
	ErrInvalidRequest        = errors.New("invalid stripe request")
	ErrNilCreator            = errors.New("payment intent creator must not be nil")
	ErrPaymentIntentNotFound = errors.New("payment intent not found for payment order")
	ErrCancelNotConfigured   = errors.New("cancel support not configured on adapter")
)

// Prometheus metrics for Stripe gateway operations.
var (
	stripePaymentTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_gateway_stripe_payment_total",
			Help: "Total number of Stripe PaymentIntent creation attempts via financial-gateway",
		},
		[]string{"status", "currency"},
	)
	stripePaymentDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "financial_gateway_stripe_payment_duration_seconds",
			Help:    "Duration of Stripe PaymentIntent creation via financial-gateway in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"status"},
	)
	stripePaymentErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "financial_gateway_stripe_payment_errors_total",
			Help: "Total number of Stripe PaymentIntent errors via financial-gateway by type",
		},
		[]string{"error_type"},
	)
)

func init() {
	prometheus.MustRegister(stripePaymentTotal)
	prometheus.MustRegister(stripePaymentDuration)
	prometheus.MustRegister(stripePaymentErrors)
}

// PaymentIntentCreator abstracts Stripe PaymentIntent creation for testability.
type PaymentIntentCreator interface {
	Create(ctx context.Context, params *stripego.PaymentIntentCreateParams) (*stripego.PaymentIntent, error)
}

// PaymentIntentCanceller abstracts Stripe PaymentIntent cancellation for testability.
type PaymentIntentCanceller interface {
	Cancel(ctx context.Context, id string, params *stripego.PaymentIntentCancelParams) (*stripego.PaymentIntent, error)
}

// PaymentIntentResolver abstracts finding a Stripe PaymentIntent ID by payment order metadata.
type PaymentIntentResolver interface {
	// FindByPaymentOrderID returns the Stripe PaymentIntent ID for the given payment_order_id.
	FindByPaymentOrderID(ctx context.Context, paymentOrderID string) (string, error)
}

// PaymentIntentAdapterConfig holds configuration for the Stripe payment intent adapter.
type PaymentIntentAdapterConfig struct {
	// PlatformFee configures the platform fee calculation.
	// If nil or zero, no platform fee is applied.
	PlatformFee *PlatformFeeConfig

	// Canceller handles Stripe PaymentIntent cancellation. Optional; if nil, CancelPayment returns ErrCancelNotConfigured.
	Canceller PaymentIntentCanceller

	// Resolver finds Stripe PaymentIntent IDs by metadata. Optional; required for CancelPayment.
	Resolver PaymentIntentResolver
}

// stripeAccountKey is the context key for the Stripe Connected Account ID.
type stripeAccountKey struct{}

// WithStripeAccount adds a Stripe Connected Account ID to the context.
func WithStripeAccount(ctx context.Context, accountID string) context.Context {
	return context.WithValue(ctx, stripeAccountKey{}, accountID)
}

// AccountFromContext extracts the Stripe Connected Account ID from the context.
func AccountFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(stripeAccountKey{}).(string)
	return id, ok && id != ""
}

// DispatchResult captures the outcome of a Stripe payment dispatch.
type DispatchResult struct {
	// ProviderReference is the Stripe PaymentIntent ID.
	ProviderReference string
	// Status is the mapped dispatch status.
	Status financialgatewayv1.DispatchStatus
	// Message is the human-readable status message from Stripe.
	Message string
	// PlatformFeeAmount is the platform fee in minor units charged on this payment.
	PlatformFeeAmount int64
}

// PaymentIntentAdapter dispatches payments via Stripe PaymentIntents.
type PaymentIntentAdapter struct {
	creator PaymentIntentCreator
	config  PaymentIntentAdapterConfig
	logger  *slog.Logger
}

// NewPaymentIntentAdapter creates a new Stripe payment intent adapter.
// Returns an error if creator is nil.
func NewPaymentIntentAdapter(creator PaymentIntentCreator, config PaymentIntentAdapterConfig, logger *slog.Logger) (*PaymentIntentAdapter, error) {
	if creator == nil {
		return nil, ErrNilCreator
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PaymentIntentAdapter{
		creator: creator,
		config:  config,
		logger:  logger,
	}, nil
}

// DispatchPayment creates a Stripe PaymentIntent on the tenant's Connected Account.
// The Stripe Connected Account ID must be present in the context (via WithStripeAccount).
func (a *PaymentIntentAdapter) DispatchPayment(ctx context.Context, req *financialgatewayv1.DispatchPaymentRequest) (DispatchResult, error) {
	accountID, ok := AccountFromContext(ctx)
	if !ok {
		return DispatchResult{}, ErrMissingStripeAccount
	}

	tenantID := ""
	if tid, ok := tenant.FromContext(ctx); ok {
		tenantID = tid.String()
	}

	currencyLower := strings.ToLower(req.GetInstrumentCode())

	params, platformFeeAmount, err := a.buildPaymentIntentParams(req, accountID, tenantID, currencyLower)
	if err != nil {
		return DispatchResult{}, err
	}

	a.logger.Debug("creating stripe payment intent",
		"payment_order_id", req.GetPaymentOrderId(),
		"tenant_id", tenantID,
		"amount", req.GetAmountUnits(),
		"currency", currencyLower,
		"connected_account", accountID,
	)

	start := time.Now()
	pi, err := a.creator.Create(ctx, params)
	duration := time.Since(start)

	if err != nil {
		return a.handleError(err, currencyLower, duration)
	}

	status := mapPaymentIntentStatus(pi.Status)

	stripePaymentTotal.WithLabelValues(status.String(), currencyLower).Inc()
	stripePaymentDuration.WithLabelValues(status.String()).Observe(duration.Seconds())

	a.logger.Info("stripe payment intent created",
		"payment_order_id", req.GetPaymentOrderId(),
		"payment_intent_id", pi.ID,
		"status", string(pi.Status),
		"mapped_status", status.String(),
		"duration_ms", duration.Milliseconds(),
	)

	return DispatchResult{
		ProviderReference: pi.ID,
		Status:            status,
		Message:           string(pi.Status),
		PlatformFeeAmount: platformFeeAmount,
	}, nil
}

// buildPaymentIntentParams constructs the Stripe PaymentIntent creation parameters
// including metadata merging and platform fee calculation.
func (a *PaymentIntentAdapter) buildPaymentIntentParams(
	req *financialgatewayv1.DispatchPaymentRequest,
	accountID, tenantID, currencyLower string,
) (*stripego.PaymentIntentCreateParams, int64, error) {
	amountMinor := req.GetAmountUnits()

	params := &stripego.PaymentIntentCreateParams{
		Amount:        stripego.Int64(amountMinor),
		Currency:      stripego.String(currencyLower),
		Customer:      stripego.String(req.GetDebtorAccountId()),
		PaymentMethod: stripego.String(req.GetReference()),
		Confirm:       stripego.Bool(true),
		OffSession:    stripego.Bool(true),
		Metadata: map[string]string{
			"payment_order_id": req.GetPaymentOrderId(),
			"tenant_id":        tenantID,
		},
	}

	// Merge request metadata, protecting reserved keys
	for k, v := range req.GetMetadata() {
		if k == "payment_order_id" || k == "tenant_id" {
			continue
		}
		params.Metadata[k] = v
	}

	// Calculate and set platform fee if configured
	var platformFeeAmount int64
	if a.config.PlatformFee != nil && !a.config.PlatformFee.IsZero() {
		var err error
		platformFeeAmount, err = a.config.PlatformFee.CalculateFee(amountMinor)
		if err != nil {
			return nil, 0, fmt.Errorf("platform fee calculation failed: %w", err)
		}
		if platformFeeAmount > 0 {
			params.ApplicationFeeAmount = stripego.Int64(platformFeeAmount)
		}
	}

	params.IdempotencyKey = stripego.String(IdempotencyKey(req.GetPaymentOrderId(), "payment_intent"))
	params.SetStripeAccount(accountID)

	return params, platformFeeAmount, nil
}

// handleError processes Stripe API errors and maps them to appropriate responses.
func (a *PaymentIntentAdapter) handleError(err error, currency string, duration time.Duration) (DispatchResult, error) {
	var stripeErr *stripego.Error
	if !errors.As(err, &stripeErr) {
		// Non-Stripe error (network, context, etc.) - propagate for retry
		stripePaymentErrors.WithLabelValues("network").Inc()
		stripePaymentDuration.WithLabelValues("error").Observe(duration.Seconds())
		return DispatchResult{}, fmt.Errorf("stripe payment failed: %w", err)
	}

	stripePaymentErrors.WithLabelValues(string(stripeErr.Type)).Inc()
	stripePaymentDuration.WithLabelValues("error").Observe(duration.Seconds())

	switch stripeErr.Type {
	case stripego.ErrorTypeCard:
		// Card errors are business rejections, not infrastructure failures.
		refID := ""
		if stripeErr.PaymentIntent != nil {
			refID = stripeErr.PaymentIntent.ID
		}

		a.logger.Info("stripe card error",
			"error_code", string(stripeErr.Code),
			"message", stripeErr.Msg,
			"decline_code", string(stripeErr.DeclineCode),
		)

		stripePaymentTotal.WithLabelValues(financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED.String(), currency).Inc()

		return DispatchResult{
			ProviderReference: refID,
			Status:            financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED,
			Message:           stripeErr.Msg,
		}, nil

	case stripego.ErrorTypeInvalidRequest:
		a.logger.Error("stripe invalid request error",
			"message", stripeErr.Msg,
			"param", stripeErr.Param,
		)
		return DispatchResult{}, fmt.Errorf("%w: %s", ErrInvalidRequest, stripeErr.Msg)

	case stripego.ErrorTypeAPI,
		stripego.ErrorTypeIdempotency,
		stripego.ErrorTypeTemporarySessionExpired:
		// API errors, idempotency errors, session expired - propagate for retry
		a.logger.Warn("stripe api error",
			"type", string(stripeErr.Type),
			"message", stripeErr.Msg,
			"http_status", stripeErr.HTTPStatusCode,
		)
		return DispatchResult{}, fmt.Errorf("stripe api error (%s): %w", stripeErr.Type, err)
	}

	// Unknown error type - propagate for retry
	a.logger.Warn("stripe unknown error type",
		"type", string(stripeErr.Type),
		"message", stripeErr.Msg,
	)
	return DispatchResult{}, fmt.Errorf("stripe error (%s): %w", stripeErr.Type, err)
}

// CancelResult captures the outcome of a Stripe payment cancellation.
type CancelResult struct {
	// ProviderReference is the Stripe PaymentIntent ID.
	ProviderReference string
	// Status is the mapped dispatch status after cancellation.
	Status financialgatewayv1.DispatchStatus
}

// CancelPayment finds and cancels the Stripe PaymentIntent associated with the given payment order.
// If the PaymentIntent is already cancelled, it succeeds idempotently.
func (a *PaymentIntentAdapter) CancelPayment(ctx context.Context, paymentOrderID, reason string) (CancelResult, error) {
	if a.config.Canceller == nil || a.config.Resolver == nil {
		return CancelResult{}, ErrCancelNotConfigured
	}

	accountID, ok := AccountFromContext(ctx)
	if !ok {
		return CancelResult{}, ErrMissingStripeAccount
	}

	piID, err := a.config.Resolver.FindByPaymentOrderID(ctx, paymentOrderID)
	if err != nil {
		return CancelResult{}, fmt.Errorf("failed to find payment intent for order %s: %w", paymentOrderID, err)
	}

	params := &stripego.PaymentIntentCancelParams{}
	if reason != "" {
		params.CancellationReason = stripego.String("requested_by_customer")
	}
	params.SetStripeAccount(accountID)

	a.logger.Debug("cancelling stripe payment intent",
		"payment_order_id", paymentOrderID,
		"payment_intent_id", piID,
		"connected_account", accountID,
	)

	pi, err := a.config.Canceller.Cancel(ctx, piID, params)
	if err != nil {
		return a.handleCancelError(err, paymentOrderID, piID)
	}

	status := mapPaymentIntentStatus(pi.Status)

	a.logger.Info("stripe payment intent cancelled",
		"payment_order_id", paymentOrderID,
		"payment_intent_id", pi.ID,
		"status", string(pi.Status),
	)

	return CancelResult{
		ProviderReference: pi.ID,
		Status:            status,
	}, nil
}

// handleCancelError maps Stripe cancellation errors to appropriate results.
// Already-cancelled intents are treated as idempotent success; non-cancellable states
// (e.g., succeeded) are returned as invalid request errors.
func (a *PaymentIntentAdapter) handleCancelError(err error, paymentOrderID, piID string) (CancelResult, error) {
	var stripeErr *stripego.Error
	if errors.As(err, &stripeErr) && stripeErr.Code == stripego.ErrorCodePaymentIntentUnexpectedState {
		if strings.Contains(stripeErr.Msg, "status of canceled") {
			a.logger.Info("stripe payment intent already cancelled",
				"payment_order_id", paymentOrderID,
				"payment_intent_id", piID,
			)
			return CancelResult{
				ProviderReference: piID,
				Status:            financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED,
			}, nil
		}
		return CancelResult{}, fmt.Errorf("payment intent %s cannot be cancelled: %w", piID, ErrInvalidRequest)
	}
	return CancelResult{}, fmt.Errorf("stripe cancel failed: %w", err)
}

// mapPaymentIntentStatus maps a Stripe PaymentIntent status to a gateway DispatchStatus.
func mapPaymentIntentStatus(status stripego.PaymentIntentStatus) financialgatewayv1.DispatchStatus {
	switch status {
	case stripego.PaymentIntentStatusSucceeded:
		return financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DELIVERED
	case stripego.PaymentIntentStatusRequiresPaymentMethod,
		stripego.PaymentIntentStatusCanceled:
		return financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED
	case stripego.PaymentIntentStatusProcessing,
		stripego.PaymentIntentStatusRequiresAction,
		stripego.PaymentIntentStatusRequiresCapture,
		stripego.PaymentIntentStatusRequiresConfirmation:
		return financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DISPATCHING
	}
	// Unknown status - treat as dispatching (in progress)
	return financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DISPATCHING
}
