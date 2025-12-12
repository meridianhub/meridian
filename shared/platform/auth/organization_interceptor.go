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
//
// Security: The organization ID is validated before being added to context.
// Invalid org IDs are silently ignored (context remains unchanged).
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
				// Validate org ID to prevent malformed values from untrusted callers
				orgID, err := organization.NewOrganizationID(vals[0])
				if err == nil {
					ctx = organization.WithOrganization(ctx, orgID)
				}
				// Invalid org IDs are silently ignored - context unchanged
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
//
// Security: The organization ID is validated before being added to context.
// Invalid org IDs are silently ignored (context remains unchanged).
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
				// Validate org ID to prevent malformed values from untrusted callers
				orgID, err := organization.NewOrganizationID(vals[0])
				if err == nil {
					ctx = organization.WithOrganization(ctx, orgID)

					// Wrap stream with the new context containing organization
					wrappedStream := &wrappedServerStream{
						ServerStream: ss,
						ctx:          ctx,
					}

					return handler(srv, wrappedStream)
				}
				// Invalid org IDs are silently ignored - use original stream
			}
		}

		return handler(srv, ss)
	}
}
