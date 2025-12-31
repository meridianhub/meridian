// Package main provides gRPC client setup for the Horizon Integrity Proof demo.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// Default ports for services in local/Tilt environment.
// These are aliases to the centralized port constants for package-level access.
const (
	CurrentAccountPort = ports.CurrentAccount
	PaymentOrderPort   = ports.PaymentOrder
)

// Client errors.
var (
	ErrTargetRequired     = errors.New("target address is required")
	ErrConnectionFailed   = errors.New("failed to establish gRPC connection")
	ErrHealthCheckFailed  = errors.New("service health check failed")
	ErrServiceUnreachable = errors.New("service is unreachable")
)

// Clients holds the gRPC client connections for the demo.
type Clients struct {
	// CurrentAccount is the client for CurrentAccountService
	CurrentAccount currentaccountv1.CurrentAccountServiceClient
	// PaymentOrder is the client for PaymentOrderService
	PaymentOrder paymentorderv1.PaymentOrderServiceClient

	// conns holds the underlying connections for cleanup
	currentAccountConn *grpc.ClientConn
	paymentOrderConn   *grpc.ClientConn

	logger *slog.Logger
}

// ClientsConfig holds configuration for creating gRPC clients.
type ClientsConfig struct {
	// CurrentAccountTarget is the gRPC target for CurrentAccountService
	// Format: "host:port" (e.g., "localhost:50051")
	CurrentAccountTarget string

	// PaymentOrderTarget is the gRPC target for PaymentOrderService
	// Format: "host:port" (e.g., "localhost:50054")
	PaymentOrderTarget string

	// Logger is the structured logger for client operations
	Logger *slog.Logger
}

// NewClients creates gRPC clients for CurrentAccountService and PaymentOrderService.
// It establishes connections with insecure credentials suitable for local/Tilt environments.
func NewClients(cfg *ClientsConfig) (*Clients, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: ClientsConfig is nil", ErrTargetRequired)
	}

	if cfg.CurrentAccountTarget == "" {
		return nil, fmt.Errorf("%w: CurrentAccountTarget", ErrTargetRequired)
	}
	if cfg.PaymentOrderTarget == "" {
		return nil, fmt.Errorf("%w: PaymentOrderTarget", ErrTargetRequired)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Create CurrentAccountService connection
	logger.Debug("connecting to CurrentAccountService", "target", cfg.CurrentAccountTarget)
	currentAccountConn, err := grpc.NewClient(
		cfg.CurrentAccountTarget,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("%w to CurrentAccountService at %s: %w",
			ErrConnectionFailed, cfg.CurrentAccountTarget, err)
	}

	// Create PaymentOrderService connection
	logger.Debug("connecting to PaymentOrderService", "target", cfg.PaymentOrderTarget)
	paymentOrderConn, err := grpc.NewClient(
		cfg.PaymentOrderTarget,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		// Clean up the first connection on failure
		if closeErr := currentAccountConn.Close(); closeErr != nil {
			logger.Warn("failed to close CurrentAccountService connection", "error", closeErr)
		}
		return nil, fmt.Errorf("%w to PaymentOrderService at %s: %w",
			ErrConnectionFailed, cfg.PaymentOrderTarget, err)
	}

	return &Clients{
		CurrentAccount:     currentaccountv1.NewCurrentAccountServiceClient(currentAccountConn),
		PaymentOrder:       paymentorderv1.NewPaymentOrderServiceClient(paymentOrderConn),
		currentAccountConn: currentAccountConn,
		paymentOrderConn:   paymentOrderConn,
		logger:             logger,
	}, nil
}

// Close terminates all gRPC connections.
// It attempts to close all connections and returns the first error encountered.
func (c *Clients) Close() error {
	var errs []error

	if c.currentAccountConn != nil {
		if err := c.currentAccountConn.Close(); err != nil {
			c.logger.Warn("failed to close CurrentAccountService connection", "error", err)
			errs = append(errs, fmt.Errorf("failed to close CurrentAccountService connection: %w", err))
		}
	}

	if c.paymentOrderConn != nil {
		if err := c.paymentOrderConn.Close(); err != nil {
			c.logger.Warn("failed to close PaymentOrderService connection", "error", err)
			errs = append(errs, fmt.Errorf("failed to close PaymentOrderService connection: %w", err))
		}
	}

	if len(errs) > 0 {
		return errs[0] // Return first error
	}
	return nil
}

// CheckHealth verifies that both services are reachable.
// It returns an error if either service is not in a connectable state.
func (c *Clients) CheckHealth(_ context.Context) error {
	// Check CurrentAccountService connection state
	caState := c.currentAccountConn.GetState()
	c.logger.Debug("CurrentAccountService connection state", "state", caState.String())
	if caState == connectivity.Shutdown || caState == connectivity.TransientFailure {
		return fmt.Errorf("%w: CurrentAccountService is in state %s",
			ErrServiceUnreachable, caState.String())
	}

	// Check PaymentOrderService connection state
	poState := c.paymentOrderConn.GetState()
	c.logger.Debug("PaymentOrderService connection state", "state", poState.String())
	if poState == connectivity.Shutdown || poState == connectivity.TransientFailure {
		return fmt.Errorf("%w: PaymentOrderService is in state %s",
			ErrServiceUnreachable, poState.String())
	}

	return nil
}

// WaitForReady blocks until both services are ready or the context is canceled.
// This is useful for startup health checks before beginning the demo.
func (c *Clients) WaitForReady(ctx context.Context) error {
	// Wait for CurrentAccountService
	if err := c.waitForConnReady(ctx, c.currentAccountConn, "CurrentAccountService"); err != nil {
		return err
	}

	// Wait for PaymentOrderService
	return c.waitForConnReady(ctx, c.paymentOrderConn, "PaymentOrderService")
}

// waitForConnReady waits for a single connection to reach Ready state.
func (c *Clients) waitForConnReady(ctx context.Context, conn *grpc.ClientConn, serviceName string) error {
	c.logger.Debug("waiting for service to be ready", "service", serviceName)

	// Trigger connection attempt (grpc.NewClient starts in Idle without I/O)
	conn.Connect()

	for {
		state := conn.GetState()
		c.logger.Debug("connection state", "service", serviceName, "state", state.String())

		if state == connectivity.Ready {
			return nil
		}

		if state == connectivity.Shutdown {
			return fmt.Errorf("%w: %s is in state %s",
				ErrServiceUnreachable, serviceName, state.String())
		}

		// TransientFailure is temporary; keep waiting unless context expires
		if !conn.WaitForStateChange(ctx, state) {
			// Context was canceled or timed out
			return fmt.Errorf("%w: context expired while waiting for %s: %w",
				ErrHealthCheckFailed, serviceName, ctx.Err())
		}
	}
}

// ContextWithCorrelationID creates a new context with the correlation ID set in gRPC metadata.
// This ensures distributed tracing works across service calls.
func ContextWithCorrelationID(ctx context.Context, correlationID string) context.Context {
	if correlationID == "" {
		return ctx
	}

	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	} else {
		md = md.Copy()
	}

	md.Set("x-correlation-id", correlationID)
	return metadata.NewOutgoingContext(ctx, md)
}

// ContextWithTenantID creates a new context with the tenant ID set in gRPC metadata.
// All service calls require tenant context for multi-tenant isolation.
func ContextWithTenantID(ctx context.Context, tenantID string) context.Context {
	if tenantID == "" {
		return ctx
	}

	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	} else {
		md = md.Copy()
	}

	md.Set("x-tenant-id", tenantID)
	return metadata.NewOutgoingContext(ctx, md)
}

// ExtractCorrelationID attempts to extract a correlation ID from the context.
// It checks multiple common keys used for correlation/request tracking.
func ExtractCorrelationID(ctx context.Context) string {
	keys := []string{"correlation-id", "x-correlation-id", "x-request-id", "request-id"}

	// Check context values first
	for _, key := range keys {
		if val := ctx.Value(key); val != nil {
			if id, ok := val.(string); ok && id != "" {
				return id
			}
		}
	}

	// Check incoming metadata as fallback
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for _, key := range keys {
			if vals := md.Get(key); len(vals) > 0 && vals[0] != "" {
				return vals[0]
			}
		}
	}

	return ""
}

// CurrentAccountState returns the current connectivity state of CurrentAccountService.
func (c *Clients) CurrentAccountState() connectivity.State {
	return c.currentAccountConn.GetState()
}

// PaymentOrderState returns the current connectivity state of PaymentOrderService.
func (c *Clients) PaymentOrderState() connectivity.State {
	return c.paymentOrderConn.GetState()
}
