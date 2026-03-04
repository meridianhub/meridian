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
	ErrMissingStripeAccount = errors.New("stripe connected account ID not found in context")
	ErrInvalidRequest       = errors.New("invalid stripe request")
	ErrNilCreator           = errors.New("payment intent creator must not be nil")
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

// PaymentIntentAdapterConfig holds configuration for the Stripe payment intent adapter.
type PaymentIntentAdapterConfig struct {
	// PlatformFee configures the platform fee calculation.
	// If nil or zero, no platform fee is applied.
	PlatformFee *PlatformFeeConfig
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

	amountMinor := req.GetAmountUnits()
	currencyLower := strings.ToLower(req.GetInstrumentCode())

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

	// Merge request metadata into Stripe metadata, protecting reserved keys
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
			return DispatchResult{}, fmt.Errorf("platform fee calculation failed: %w", err)
		}
		if platformFeeAmount > 0 {
			params.ApplicationFeeAmount = stripego.Int64(platformFeeAmount)
		}
	}

	// Set idempotency key from the proto request
	idempotencyKey := IdempotencyKey(req.GetPaymentOrderId(), "payment_intent")
	params.IdempotencyKey = stripego.String(idempotencyKey)

	// Set Connected Account header
	params.SetStripeAccount(accountID)

	a.logger.Debug("creating stripe payment intent",
		"payment_order_id", req.GetPaymentOrderId(),
		"tenant_id", tenantID,
		"amount", amountMinor,
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
