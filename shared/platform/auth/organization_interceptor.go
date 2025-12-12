package auth

import (
	"context"

	"github.com/meridianhub/meridian/shared/platform/organization"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// OrganizationExtractionInterceptor extracts organization ID from gRPC metadata.
// This works alongside JWT auth interceptor for service-to-service calls.
// If organization is already in context (from JWT auth), this is a no-op.
//
// Use case: Service A calls Service B with organization in metadata. Service B
// extracts the org from metadata and injects it into context, enabling multi-hop
// call chains to propagate organization context.
func OrganizationExtractionInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Check if organization is already in context (from JWT auth)
		if _, ok := organization.FromContext(ctx); ok {
			return handler(ctx, req)
		}

		// Extract from incoming metadata
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			if vals := md.Get(organization.OrgIDKey); len(vals) > 0 {
				orgID := organization.OrganizationID(vals[0])
				ctx = organization.WithOrganization(ctx, orgID)
			}
		}

		return handler(ctx, req)
	}
}

// OrganizationExtractionStreamInterceptor extracts organization ID from gRPC
// metadata for streaming RPCs. This is the streaming equivalent of
// OrganizationExtractionInterceptor.
//
// If organization is already in context (from JWT auth), this is a no-op.
func OrganizationExtractionStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		_ *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		ctx := ss.Context()

		// Check if organization is already in context (from JWT auth)
		if _, ok := organization.FromContext(ctx); ok {
			return handler(srv, ss)
		}

		// Extract from incoming metadata
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			if vals := md.Get(organization.OrgIDKey); len(vals) > 0 {
				orgID := organization.OrganizationID(vals[0])
				ctx = organization.WithOrganization(ctx, orgID)

				// Wrap stream with the new context containing organization
				wrappedStream := &wrappedServerStream{
					ServerStream: ss,
					ctx:          ctx,
				}

				return handler(srv, wrappedStream)
			}
		}

		return handler(srv, ss)
	}
}
