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

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Sentinel errors for the Stripe gateway adapter.
var (
	ErrMissingStripeAccount = errors.New("stripe connected account ID not found in context")
	ErrInvalidRequest       = errors.New("invalid stripe request")
)

// Prometheus metrics for Stripe gateway operations.
var (
	stripePaymentTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stripe_gateway_payment_total",
			Help: "Total number of Stripe PaymentIntent creation attempts",
		},
		[]string{"status", "currency"},
	)
	stripePaymentDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "stripe_gateway_payment_duration_seconds",
			Help:    "Duration of Stripe PaymentIntent creation in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"status"},
	)
	stripePaymentErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stripe_gateway_payment_errors_total",
			Help: "Total number of Stripe PaymentIntent errors by type",
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

// GatewayAdapterConfig holds configuration for the Stripe gateway adapter.
type GatewayAdapterConfig struct {
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

// GatewayAdapter implements gateway.PaymentGateway using Stripe PaymentIntents.
type GatewayAdapter struct {
	creator PaymentIntentCreator
	config  GatewayAdapterConfig
	logger  *slog.Logger
}

// NewGatewayAdapter creates a new Stripe gateway adapter.
func NewGatewayAdapter(creator PaymentIntentCreator, config GatewayAdapterConfig, logger *slog.Logger) *GatewayAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &GatewayAdapter{
		creator: creator,
		config:  config,
		logger:  logger,
	}
}

// SendPayment creates a Stripe PaymentIntent on the tenant's Connected Account.
// The Stripe Connected Account ID must be present in the context (via WithStripeAccount).
func (a *GatewayAdapter) SendPayment(ctx context.Context, req gateway.PaymentRequest) (gateway.PaymentResponse, error) {
	accountID, ok := AccountFromContext(ctx)
	if !ok {
		return gateway.PaymentResponse{}, ErrMissingStripeAccount
	}

	tenantID := ""
	if tid, ok := tenant.FromContext(ctx); ok {
		tenantID = tid.String()
	}

	params, platformFeeAmount, err := a.buildPaymentIntentParams(req, accountID, tenantID)
	if err != nil {
		return gateway.PaymentResponse{}, err
	}

	a.logger.Debug("creating stripe payment intent",
		"payment_order_id", req.PaymentOrderID.String(),
		"tenant_id", tenantID,
		"amount", domain.ToMinorUnits(req.Amount),
		"currency", strings.ToLower(domain.CurrencyCode(req.Amount)),
		"connected_account", accountID,
	)

	start := time.Now()
	pi, err := a.creator.Create(ctx, params)
	duration := time.Since(start)

	if err != nil {
		return a.handleError(err, strings.ToLower(domain.CurrencyCode(req.Amount)), duration)
	}

	return a.buildSuccessResponse(pi, req.PaymentOrderID.String(), platformFeeAmount, duration), nil
}

// buildPaymentIntentParams constructs the Stripe PaymentIntent creation parameters.
func (a *GatewayAdapter) buildPaymentIntentParams(req gateway.PaymentRequest, accountID, tenantID string) (*stripego.PaymentIntentCreateParams, int64, error) {
	amountMinor := domain.ToMinorUnits(req.Amount)
	currencyLower := strings.ToLower(domain.CurrencyCode(req.Amount))

	params := &stripego.PaymentIntentCreateParams{
		Amount:        stripego.Int64(amountMinor),
		Currency:      stripego.String(currencyLower),
		Customer:      stripego.String(req.DebtorAccountID),
		PaymentMethod: stripego.String(req.CreditorReference),
		Confirm:       stripego.Bool(true),
		OffSession:    stripego.Bool(true),
		Metadata: map[string]string{
			"payment_order_id": req.PaymentOrderID.String(),
			"tenant_id":        tenantID,
		},
	}

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

	params.IdempotencyKey = stripego.String(IdempotencyKey(req.PaymentOrderID, "payment_intent"))
	params.SetStripeAccount(accountID)

	return params, platformFeeAmount, nil
}

// buildSuccessResponse constructs the gateway response from a successful PaymentIntent creation.
func (a *GatewayAdapter) buildSuccessResponse(pi *stripego.PaymentIntent, paymentOrderID string, platformFeeAmount int64, duration time.Duration) gateway.PaymentResponse {
	status := mapPaymentIntentStatus(pi.Status)
	currencyLower := strings.ToLower(string(pi.Currency))

	stripePaymentTotal.WithLabelValues(string(status), currencyLower).Inc()
	stripePaymentDuration.WithLabelValues(string(status)).Observe(duration.Seconds())

	a.logger.Info("stripe payment intent created",
		"payment_order_id", paymentOrderID,
		"payment_intent_id", pi.ID,
		"status", string(pi.Status),
		"mapped_status", string(status),
		"duration_ms", duration.Milliseconds(),
	)

	return gateway.PaymentResponse{
		GatewayReferenceID: pi.ID,
		Status:             status,
		Message:            string(pi.Status),
		PlatformFeeAmount:  platformFeeAmount,
	}
}

// handleError processes Stripe API errors and maps them to appropriate responses.
func (a *GatewayAdapter) handleError(err error, currency string, duration time.Duration) (gateway.PaymentResponse, error) {
	var stripeErr *stripego.Error
	if !errors.As(err, &stripeErr) {
		// Non-Stripe error (network, context, etc.) - propagate for retry
		stripePaymentErrors.WithLabelValues("network").Inc()
		stripePaymentDuration.WithLabelValues("error").Observe(duration.Seconds())
		return gateway.PaymentResponse{}, fmt.Errorf("stripe payment failed: %w", err)
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

		stripePaymentTotal.WithLabelValues(string(gateway.StatusRejected), currency).Inc()

		return gateway.PaymentResponse{
			GatewayReferenceID: refID,
			Status:             gateway.StatusRejected,
			Message:            stripeErr.Msg,
		}, nil

	case stripego.ErrorTypeInvalidRequest:
		a.logger.Error("stripe invalid request error",
			"message", stripeErr.Msg,
			"param", stripeErr.Param,
		)
		return gateway.PaymentResponse{}, fmt.Errorf("%w: %s", ErrInvalidRequest, stripeErr.Msg)

	case stripego.ErrorTypeAPI,
		stripego.ErrorTypeIdempotency,
		stripego.ErrorTypeTemporarySessionExpired:
		// API errors, idempotency errors, session expired - propagate for retry
		a.logger.Warn("stripe api error",
			"type", string(stripeErr.Type),
			"message", stripeErr.Msg,
			"http_status", stripeErr.HTTPStatusCode,
		)
		return gateway.PaymentResponse{}, fmt.Errorf("stripe api error (%s): %w", stripeErr.Type, err)
	}

	// Unknown error type - propagate for retry
	a.logger.Warn("stripe unknown error type",
		"type", string(stripeErr.Type),
		"message", stripeErr.Msg,
	)
	return gateway.PaymentResponse{}, fmt.Errorf("stripe error (%s): %w", stripeErr.Type, err)
}

// mapPaymentIntentStatus maps a Stripe PaymentIntent status to a gateway.Status.
func mapPaymentIntentStatus(status stripego.PaymentIntentStatus) gateway.Status {
	switch status {
	case stripego.PaymentIntentStatusSucceeded:
		return gateway.StatusAccepted
	case stripego.PaymentIntentStatusRequiresPaymentMethod,
		stripego.PaymentIntentStatusCanceled:
		return gateway.StatusRejected
	case stripego.PaymentIntentStatusProcessing,
		stripego.PaymentIntentStatusRequiresAction,
		stripego.PaymentIntentStatusRequiresCapture,
		stripego.PaymentIntentStatusRequiresConfirmation:
		return gateway.StatusPending
	}
	// Unknown status - treat as pending
	return gateway.StatusPending
}

// Compile-time interface check
var _ gateway.PaymentGateway = (*GatewayAdapter)(nil)
