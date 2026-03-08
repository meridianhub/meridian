package server

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func contextWithClaims(roles []string, scopes []string) context.Context {
	claims := &auth.Claims{
		UserID: "test-user",
		Roles:  roles,
		Scopes: scopes,
	}
	return context.WithValue(context.Background(), auth.ClaimsContextKey, claims)
}

func noopUnaryHandler(_ context.Context, _ interface{}) (interface{}, error) {
	return "ok", nil
}

func TestManifestRBACUnaryInterceptor_AuditorCanReadHistory(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()
	ctx := contextWithClaims([]string{"auditor"}, nil)

	methods := []string{
		"/meridian.control_plane.v1.ManifestHistoryService/GetCurrentManifest",
		"/meridian.control_plane.v1.ManifestHistoryService/GetManifestVersion",
		"/meridian.control_plane.v1.ManifestHistoryService/ListManifestVersions",
	}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, noopUnaryHandler)
			require.NoError(t, err)
			assert.Equal(t, "ok", resp)
		})
	}
}

func TestManifestRBACUnaryInterceptor_AuditorCannotApplyManifest(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()
	ctx := contextWithClaims([]string{"auditor"}, nil)

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
		FullMethod: "/meridian.control_plane.v1.ApplyManifestService/ApplyManifest",
	}, noopUnaryHandler)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())
	assert.Contains(t, st.Message(), "admin role required")
	assert.Contains(t, st.Message(), "auditor")
}

func TestManifestRBACUnaryInterceptor_AuditorCannotExecuteSaga(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()
	ctx := contextWithClaims([]string{"auditor"}, nil)

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
		FullMethod: "/meridian.control_plane.v1.SagaExecutionService/ExecuteSaga",
	}, noopUnaryHandler)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())
	assert.Contains(t, st.Message(), "operator role required")
}

func TestManifestRBACUnaryInterceptor_OperatorCanExecuteSaga(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()
	ctx := contextWithClaims([]string{"operator"}, nil)

	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
		FullMethod: "/meridian.control_plane.v1.SagaExecutionService/ExecuteSaga",
	}, noopUnaryHandler)

	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestManifestRBACUnaryInterceptor_OperatorCannotApplyManifest(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()
	ctx := contextWithClaims([]string{"operator"}, nil)

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
		FullMethod: "/meridian.control_plane.v1.ApplyManifestService/ApplyManifest",
	}, noopUnaryHandler)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())
	assert.Contains(t, st.Message(), "admin role required")
}

func TestManifestRBACUnaryInterceptor_AdminCanCallAll(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()
	ctx := contextWithClaims([]string{"admin"}, nil)

	methods := []string{
		"/meridian.control_plane.v1.ManifestHistoryService/GetCurrentManifest",
		"/meridian.control_plane.v1.ManifestHistoryService/GetManifestVersion",
		"/meridian.control_plane.v1.ManifestHistoryService/ListManifestVersions",
		"/meridian.control_plane.v1.ApplyManifestService/ApplyManifest",
		"/meridian.control_plane.v1.SagaExecutionService/ExecuteSaga",
	}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, noopUnaryHandler)
			require.NoError(t, err)
			assert.Equal(t, "ok", resp)
		})
	}
}

func TestManifestRBACUnaryInterceptor_TenantOwnerCanCallAll(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()
	ctx := contextWithClaims([]string{"tenant-owner"}, nil)

	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
		FullMethod: "/meridian.control_plane.v1.ApplyManifestService/ApplyManifest",
	}, noopUnaryHandler)

	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestManifestRBACUnaryInterceptor_Unauthenticated(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()
	ctx := context.Background() // no claims

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
		FullMethod: "/meridian.control_plane.v1.ApplyManifestService/ApplyManifest",
	}, noopUnaryHandler)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
}

func TestManifestRBACUnaryInterceptor_UnprotectedMethodAllowed(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()
	ctx := context.Background() // no claims

	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
		FullMethod: "/grpc.health.v1.Health/Check",
	}, noopUnaryHandler)

	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestManifestRBACUnaryInterceptor_OperatorCanReadHistory(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()
	ctx := contextWithClaims([]string{"operator"}, nil)

	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
		FullMethod: "/meridian.control_plane.v1.ManifestHistoryService/GetCurrentManifest",
	}, noopUnaryHandler)

	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestManifestRBACUnaryInterceptor_ServiceRoleCanApply(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()
	ctx := contextWithClaims([]string{"service"}, nil)

	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
		FullMethod: "/meridian.control_plane.v1.ApplyManifestService/ApplyManifest",
	}, noopUnaryHandler)

	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestManifestRBACUnaryInterceptor_APIKeyScopeEnforcement(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()

	t.Run("read scope can access history", func(t *testing.T) {
		ctx := contextWithClaims([]string{"admin"}, []string{"manifest:read"})

		resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
			FullMethod: "/meridian.control_plane.v1.ManifestHistoryService/GetCurrentManifest",
		}, noopUnaryHandler)

		require.NoError(t, err)
		assert.Equal(t, "ok", resp)
	})

	t.Run("read scope cannot apply manifest", func(t *testing.T) {
		ctx := contextWithClaims([]string{"admin"}, []string{"manifest:read"})

		_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
			FullMethod: "/meridian.control_plane.v1.ApplyManifestService/ApplyManifest",
		}, noopUnaryHandler)

		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
		assert.Contains(t, st.Message(), "API key scope insufficient")
	})

	t.Run("admin scope can apply manifest", func(t *testing.T) {
		ctx := contextWithClaims([]string{"admin"}, []string{"manifest:admin"})

		resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
			FullMethod: "/meridian.control_plane.v1.ApplyManifestService/ApplyManifest",
		}, noopUnaryHandler)

		require.NoError(t, err)
		assert.Equal(t, "ok", resp)
	})

	t.Run("write scope can execute saga", func(t *testing.T) {
		ctx := contextWithClaims([]string{"operator"}, []string{"manifest:write"})

		resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
			FullMethod: "/meridian.control_plane.v1.SagaExecutionService/ExecuteSaga",
		}, noopUnaryHandler)

		require.NoError(t, err)
		assert.Equal(t, "ok", resp)
	})

	t.Run("no manifest scopes passes through to role check", func(t *testing.T) {
		ctx := contextWithClaims([]string{"admin"}, []string{"other:read"})

		resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
			FullMethod: "/meridian.control_plane.v1.ApplyManifestService/ApplyManifest",
		}, noopUnaryHandler)

		require.NoError(t, err)
		assert.Equal(t, "ok", resp)
	})
}

func TestManifestRBACUnaryInterceptor_DescriptiveErrorMessages(t *testing.T) {
	interceptor := ManifestRBACUnaryInterceptor()

	t.Run("includes required role in error", func(t *testing.T) {
		ctx := contextWithClaims([]string{"auditor"}, nil)
		_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
			FullMethod: "/meridian.control_plane.v1.ApplyManifestService/ApplyManifest",
		}, noopUnaryHandler)

		st, _ := status.FromError(err)
		assert.Contains(t, st.Message(), "admin role required")
	})

	t.Run("includes actual roles in error", func(t *testing.T) {
		ctx := contextWithClaims([]string{"auditor"}, nil)
		_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{
			FullMethod: "/meridian.control_plane.v1.ApplyManifestService/ApplyManifest",
		}, noopUnaryHandler)

		st, _ := status.FromError(err)
		assert.Contains(t, st.Message(), "auditor")
	})
}

func TestMeetsMinimumRole(t *testing.T) {
	tests := []struct {
		name        string
		userRoles   []string
		minimumRole auth.Role
		expected    bool
	}{
		{"auditor meets auditor", []string{"auditor"}, auth.RoleAuditor, true},
		{"operator meets auditor", []string{"operator"}, auth.RoleAuditor, true},
		{"admin meets auditor", []string{"admin"}, auth.RoleAuditor, true},
		{"auditor does not meet operator", []string{"auditor"}, auth.RoleOperator, false},
		{"auditor does not meet admin", []string{"auditor"}, auth.RoleAdmin, false},
		{"operator meets operator", []string{"operator"}, auth.RoleOperator, true},
		{"operator does not meet admin", []string{"operator"}, auth.RoleAdmin, false},
		{"admin meets admin", []string{"admin"}, auth.RoleAdmin, true},
		{"super-admin meets admin", []string{"super-admin"}, auth.RoleAdmin, true},
		{"tenant-owner meets admin", []string{"tenant-owner"}, auth.RoleAdmin, true},
		{"platform-admin meets admin", []string{"platform-admin"}, auth.RoleAdmin, true},
		{"empty roles meets nothing", []string{}, auth.RoleAuditor, false},
		{"invalid role meets nothing", []string{"viewer"}, auth.RoleAuditor, false},
		{"multiple roles use highest", []string{"auditor", "admin"}, auth.RoleAdmin, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := meetsMinimumRole(tt.userRoles, tt.minimumRole)
			assert.Equal(t, tt.expected, result)
		})
	}
}
