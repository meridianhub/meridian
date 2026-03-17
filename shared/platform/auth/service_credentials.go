package auth

import (
	"context"
	"fmt"
)

// ServiceCredentials implements grpc.PerRPCCredentials using an OAuth2Client
// to attach JWT bearer tokens to outbound gRPC calls for service-to-service
// authentication.
type ServiceCredentials struct {
	client *OAuth2Client
}

// NewServiceCredentials creates ServiceCredentials backed by the given OAuth2Client.
func NewServiceCredentials(client *OAuth2Client) (*ServiceCredentials, error) {
	if client == nil {
		return nil, fmt.Errorf("failed to create service credentials: %w", ErrOAuthProviderNil)
	}
	return &ServiceCredentials{client: client}, nil
}

// GetRequestMetadata fetches a token from the OAuth2Client and returns it as
// a bearer authorization header. Called by gRPC before each RPC.
func (s *ServiceCredentials) GetRequestMetadata(ctx context.Context, _ ...string) (map[string]string, error) {
	token, err := s.client.GetToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("service credentials: %w", err)
	}
	return map[string]string{
		"authorization": "Bearer " + token,
	}, nil
}

// RequireTransportSecurity returns false because TLS is handled at the service
// mesh level (mTLS), not by individual gRPC connections.
func (s *ServiceCredentials) RequireTransportSecurity() bool {
	return false
}
