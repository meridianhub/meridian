// Package auth provides API key extraction and gRPC client authentication
// for the MCP server. It reads the API key from environment variables and
// supplies a gRPC UnaryClientInterceptor that injects an Authorization header
// into every outgoing request.
package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	// EnvAPIKey is the environment variable name for the Meridian API key.
	EnvAPIKey = "MERIDIAN_API_KEY"
	// EnvAPIURL is the environment variable name for the Meridian gateway URL.
	EnvAPIURL = "MERIDIAN_API_URL"
)

var (
	// ErrMissingAPIKey is returned when the API key environment variable is not set.
	ErrMissingAPIKey = errors.New("MERIDIAN_API_KEY environment variable is required")
	// ErrMissingAPIURL is returned when the gateway URL environment variable is not set.
	ErrMissingAPIURL = errors.New("MERIDIAN_API_URL environment variable is required")
)

// Config holds the API key and gateway URL used to authenticate outgoing
// gRPC calls from the MCP server to Meridian Core services.
type Config struct {
	// APIKey is the bearer token sent in the Authorization header.
	APIKey string
	// APIUrl is the address of the Meridian gateway (host:port).
	APIUrl string
}

// String returns a safe representation of Config that redacts the API key,
// preventing accidental secret leakage via fmt.Printf or structured loggers.
func (a Config) String() string {
	return fmt.Sprintf("Config{APIKey: [REDACTED], APIUrl: %q}", a.APIUrl)
}

// LoadFromEnv reads Config from environment variables.
// It returns ErrMissingAPIKey when MERIDIAN_API_KEY is absent or blank,
// and ErrMissingAPIURL when MERIDIAN_API_URL is absent or blank.
func LoadFromEnv() (*Config, error) {
	apiKey := strings.TrimSpace(os.Getenv(EnvAPIKey))
	if apiKey == "" {
		return nil, fmt.Errorf("load auth config: %w", ErrMissingAPIKey)
	}

	apiURL := strings.TrimSpace(os.Getenv(EnvAPIURL))
	if apiURL == "" {
		return nil, fmt.Errorf("load auth config: %w", ErrMissingAPIURL)
	}

	return &Config{
		APIKey: apiKey,
		APIUrl: apiURL,
	}, nil
}

// UnaryInterceptor returns a gRPC UnaryClientInterceptor that injects the API
// key as a Bearer token in the outgoing metadata of every request.
func (a *Config) UnaryInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+a.APIKey)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
