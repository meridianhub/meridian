package auth

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/organization"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestOrganizationExtractionInterceptor(t *testing.T) {
	t.Run("extracts org from metadata when not in context", func(t *testing.T) {
		md := metadata.Pairs(organization.OrgIDKey, "acme_bank")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		interceptor := OrganizationExtractionInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify organization was extracted into context
		resultCtx := resp.(context.Context)
		orgID, ok := organization.FromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, organization.OrganizationID("acme_bank"), orgID)
	})

	t.Run("is no-op when org already in context", func(t *testing.T) {
		// Set org in context directly (simulating JWT auth having set it)
		ctx := organization.WithOrganization(context.Background(), "jwt_org")

		// Also set a different org in metadata
		md := metadata.Pairs(organization.OrgIDKey, "metadata_org")
		ctx = metadata.NewIncomingContext(ctx, md)

		interceptor := OrganizationExtractionInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify original org from context is preserved (JWT takes precedence)
		resultCtx := resp.(context.Context)
		orgID, ok := organization.FromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, organization.OrganizationID("jwt_org"), orgID)
	})

	t.Run("context unchanged when no org in metadata", func(t *testing.T) {
		// No org in context and no org in metadata
		md := metadata.Pairs("other-header", "value")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		interceptor := OrganizationExtractionInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify no org in context
		resultCtx := resp.(context.Context)
		_, ok := organization.FromContext(resultCtx)
		assert.False(t, ok)
	})

	t.Run("context unchanged when no metadata present", func(t *testing.T) {
		// No metadata at all
		ctx := context.Background()

		interceptor := OrganizationExtractionInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify no org in context
		resultCtx := resp.(context.Context)
		_, ok := organization.FromContext(resultCtx)
		assert.False(t, ok)
	})

	t.Run("extracts first org value when multiple present", func(t *testing.T) {
		// Multiple org values in metadata (edge case)
		md := metadata.Pairs(
			organization.OrgIDKey, "first_org",
			organization.OrgIDKey, "second_org",
		)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		interceptor := OrganizationExtractionInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Should use the first value
		resultCtx := resp.(context.Context)
		orgID, ok := organization.FromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, organization.OrganizationID("first_org"), orgID)
	})

	t.Run("ignores invalid org ID format in metadata", func(t *testing.T) {
		// Invalid org ID with special characters (validation fails)
		md := metadata.Pairs(organization.OrgIDKey, "invalid org!")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		interceptor := OrganizationExtractionInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Org should NOT be in context due to validation failure
		resultCtx := resp.(context.Context)
		_, ok := organization.FromContext(resultCtx)
		assert.False(t, ok)
	})
}

func TestOrganizationExtractionStreamInterceptor(t *testing.T) {
	t.Run("extracts org from metadata when not in context", func(t *testing.T) {
		md := metadata.Pairs(organization.OrgIDKey, "acme_bank")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		stream := &mockServerStream{ctx: ctx}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}

		var capturedCtx context.Context
		handler := func(_ interface{}, ss grpc.ServerStream) error {
			capturedCtx = ss.Context()
			return nil
		}

		interceptor := OrganizationExtractionStreamInterceptor()
		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)

		// Verify organization was extracted into context
		orgID, ok := organization.FromContext(capturedCtx)
		assert.True(t, ok)
		assert.Equal(t, organization.OrganizationID("acme_bank"), orgID)
	})

	t.Run("is no-op when org already in context", func(t *testing.T) {
		// Set org in context directly (simulating JWT auth having set it)
		ctx := organization.WithOrganization(context.Background(), "jwt_org")

		// Also set a different org in metadata
		md := metadata.Pairs(organization.OrgIDKey, "metadata_org")
		ctx = metadata.NewIncomingContext(ctx, md)

		stream := &mockServerStream{ctx: ctx}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}

		var capturedCtx context.Context
		handler := func(_ interface{}, ss grpc.ServerStream) error {
			capturedCtx = ss.Context()
			return nil
		}

		interceptor := OrganizationExtractionStreamInterceptor()
		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)

		// Verify original org from context is preserved (JWT takes precedence)
		orgID, ok := organization.FromContext(capturedCtx)
		assert.True(t, ok)
		assert.Equal(t, organization.OrganizationID("jwt_org"), orgID)
	})

	t.Run("context unchanged when no org in metadata", func(t *testing.T) {
		// No org in context and no org in metadata
		md := metadata.Pairs("other-header", "value")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		stream := &mockServerStream{ctx: ctx}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}

		var capturedCtx context.Context
		handler := func(_ interface{}, ss grpc.ServerStream) error {
			capturedCtx = ss.Context()
			return nil
		}

		interceptor := OrganizationExtractionStreamInterceptor()
		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)

		// Verify no org in context
		_, ok := organization.FromContext(capturedCtx)
		assert.False(t, ok)
	})

	t.Run("context unchanged when no metadata present", func(t *testing.T) {
		// No metadata at all
		ctx := context.Background()

		stream := &mockServerStream{ctx: ctx}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}

		var capturedCtx context.Context
		handler := func(_ interface{}, ss grpc.ServerStream) error {
			capturedCtx = ss.Context()
			return nil
		}

		interceptor := OrganizationExtractionStreamInterceptor()
		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)

		// Verify no org in context
		_, ok := organization.FromContext(capturedCtx)
		assert.False(t, ok)
	})

	t.Run("extracts first org value when multiple present", func(t *testing.T) {
		// Multiple org values in metadata (edge case)
		md := metadata.Pairs(
			organization.OrgIDKey, "first_org",
			organization.OrgIDKey, "second_org",
		)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		stream := &mockServerStream{ctx: ctx}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}

		var capturedCtx context.Context
		handler := func(_ interface{}, ss grpc.ServerStream) error {
			capturedCtx = ss.Context()
			return nil
		}

		interceptor := OrganizationExtractionStreamInterceptor()
		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)

		// Should use the first value
		orgID, ok := organization.FromContext(capturedCtx)
		assert.True(t, ok)
		assert.Equal(t, organization.OrganizationID("first_org"), orgID)
	})

	t.Run("ignores invalid org ID format in metadata", func(t *testing.T) {
		// Invalid org ID with special characters (validation fails)
		md := metadata.Pairs(organization.OrgIDKey, "invalid org!")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		stream := &mockServerStream{ctx: ctx}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}

		var capturedCtx context.Context
		handler := func(_ interface{}, ss grpc.ServerStream) error {
			capturedCtx = ss.Context()
			return nil
		}

		interceptor := OrganizationExtractionStreamInterceptor()
		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)

		// Org should NOT be in context due to validation failure
		_, ok := organization.FromContext(capturedCtx)
		assert.False(t, ok)
	})
}
