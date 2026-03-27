package clients

import (
	"context"
	"errors"
	"fmt"

	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ErrConnTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrConnTargetRequired = errors.New("either Target or ServiceName must be provided")

// ConnConfig holds configuration for creating a gRPC client connection.
// It supports both DNS-based Kubernetes discovery (preferred) and direct connections.
type ConnConfig struct {
	// Target is the gRPC server address (e.g., "localhost:50053").
	// If set, overrides Kubernetes DNS-based discovery.
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "position-keeping").
	ServiceName string

	// Namespace is the Kubernetes namespace. Defaults to "default".
	Namespace string

	// Port is the service port number.
	Port int

	// Tracer is an optional observability tracer for distributed tracing.
	Tracer *observability.Tracer

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption
}

// NewConn creates a gRPC client connection using either DNS-based discovery or direct target.
// Returns the connection, a cleanup function, and any error.
//
// When ServiceName is set, it uses the platform gRPC factory for DNS-based
// client-side load balancing. When Target is set, it creates a direct connection
// with insecure credentials. Tracing interceptors are added when Tracer is provided.
func NewConn(ctx context.Context, cfg ConnConfig) (*grpc.ClientConn, func(), error) {
	var conn *grpc.ClientConn
	var err error

	if cfg.ServiceName != "" {
		conn, err = newDNSConn(ctx, cfg)
	} else if cfg.Target != "" {
		conn, err = newDirectConn(cfg)
	} else {
		return nil, nil, ErrConnTargetRequired
	}

	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		if conn != nil {
			_ = conn.Close()
		}
	}

	return conn, cleanup, nil
}

// newDNSConn creates a connection via Kubernetes DNS-based discovery.
func newDNSConn(ctx context.Context, cfg ConnConfig) (*grpc.ClientConn, error) {
	dialOpts := appendTracingOpts(cfg.DialOptions, cfg.Tracer)

	conn, err := platformgrpc.NewClient(ctx, platformgrpc.ClientConfig{
		ServiceName: cfg.ServiceName,
		Namespace:   cfg.Namespace,
		Port:        cfg.Port,
		DialOptions: dialOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection via platform factory for %s: %w", cfg.ServiceName, err)
	}
	return conn, nil
}

// newDirectConn creates a direct gRPC connection to a target address.
func newDirectConn(cfg ConnConfig) (*grpc.ClientConn, error) {
	dialOpts := cfg.DialOptions
	if dialOpts == nil {
		dialOpts = []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}
	}

	dialOpts = appendTracingOpts(dialOpts, cfg.Tracer)

	conn, err := grpc.NewClient(cfg.Target, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to %s: %w", cfg.Target, err)
	}
	return conn, nil
}

// appendTracingOpts appends tracing interceptors to dial options if a tracer is provided.
func appendTracingOpts(opts []grpc.DialOption, tracer *observability.Tracer) []grpc.DialOption {
	if tracer == nil {
		return opts
	}
	return append(opts,
		grpc.WithChainUnaryInterceptor(tracer.UnaryClientInterceptor()),
		grpc.WithChainStreamInterceptor(tracer.StreamClientInterceptor()),
	)
}
