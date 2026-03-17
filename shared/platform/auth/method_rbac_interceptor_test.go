package auth

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// noopHandler is a gRPC unary handler that returns nil for testing.
func noopHandler(_ context.Context, _ interface{}) (interface{}, error) {
	return "ok", nil
}

func TestMethodRBACInterceptor_FailClosed_UnmappedMethod(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/AllowedMethod": {AllowedRoles: []Role{RoleAdmin}},
		},
	})

	// Put valid claims in context so we isolate the fail-closed behavior
	claims := &Claims{Roles: []string{"admin"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/UnknownMethod"}, noopHandler)
	if err == nil {
		t.Fatal("expected error for unmapped method, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

func TestMethodRBACInterceptor_CorrectRole_Passes(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/GetItem": {AllowedRoles: []Role{RoleAdmin, RoleOperator}},
		},
	})

	claims := &Claims{Roles: []string{"operator"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/GetItem"}, noopHandler)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected handler response 'ok', got %v", resp)
	}
}

func TestMethodRBACInterceptor_InsufficientRole_Denied(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/AdminOnly": {AllowedRoles: []Role{RoleAdmin}},
		},
	})

	claims := &Claims{Roles: []string{"auditor"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/AdminOnly"}, noopHandler)
	if err == nil {
		t.Fatal("expected error for insufficient role, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

func TestMethodRBACInterceptor_MissingAuthContext_Unauthenticated(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/GetItem": {AllowedRoles: []Role{RoleAdmin}},
		},
	})

	// No claims in context
	ctx := context.Background()

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/GetItem"}, noopHandler)
	if err == nil {
		t.Fatal("expected error for missing auth context, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %v", st.Code())
	}
}

func TestMethodRBACInterceptor_AllowUnmapped_PermitsUnconfiguredMethods(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/GetItem": {AllowedRoles: []Role{RoleAdmin}},
		},
		AllowUnmapped: true,
	})

	claims := &Claims{Roles: []string{"auditor"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/UnknownMethod"}, noopHandler)
	if err != nil {
		t.Fatalf("expected no error with AllowUnmapped=true, got %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected handler response 'ok', got %v", resp)
	}
}

func TestMethodRBACInterceptor_PermissionBased_Passes(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/ReadAccount": {
				ResourceType: ResourceTypeAccount,
				Permission:   PermissionRead,
			},
		},
	})

	// Auditor has read permission on accounts
	claims := &Claims{Roles: []string{"auditor"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/ReadAccount"}, noopHandler)
	if err != nil {
		t.Fatalf("expected no error for permission-based check, got %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected handler response 'ok', got %v", resp)
	}
}

func TestMethodRBACInterceptor_PermissionBased_Denied(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/WriteAccount": {
				ResourceType: ResourceTypeAccount,
				Permission:   PermissionWrite,
			},
		},
	})

	// Auditor does NOT have write permission on accounts
	claims := &Claims{Roles: []string{"auditor"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/WriteAccount"}, noopHandler)
	if err == nil {
		t.Fatal("expected error for permission-based denial, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

func TestMethodRBACInterceptor_GroupsFallback(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/GetItem": {AllowedRoles: []Role{RoleAdmin}},
		},
	})

	// Claims with Groups instead of Roles (OIDC provider like Dex)
	claims := &Claims{Groups: []string{"admin"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/GetItem"}, noopHandler)
	if err != nil {
		t.Fatalf("expected no error with groups fallback, got %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected handler response 'ok', got %v", resp)
	}
}

func TestMethodRBACInterceptor_PermissionBased_GroupsFallback(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/ReadAccount": {
				ResourceType: ResourceTypeAccount,
				Permission:   PermissionRead,
			},
		},
	})

	// Claims with Groups instead of Roles (OIDC provider like Dex)
	// Auditor role has read permission on accounts
	claims := &Claims{Groups: []string{"auditor"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/ReadAccount"}, noopHandler)
	if err != nil {
		t.Fatalf("expected no error for permission-based check with groups fallback, got %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected handler response 'ok', got %v", resp)
	}
}

func TestMethodRBACInterceptor_AuditLog(t *testing.T) {
	var logged []string
	logger := func(method, userID, decision string) {
		logged = append(logged, method+":"+userID+":"+decision)
	}

	interceptor := NewMethodRBACInterceptorWithAudit(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/GetItem": {AllowedRoles: []Role{RoleAdmin}},
		},
	}, logger)

	// Successful access
	claims := &Claims{UserID: "user-1", Roles: []string{"admin"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	_, _ = interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/GetItem"}, noopHandler)

	// Denied access (insufficient role)
	claims2 := &Claims{UserID: "user-2", Roles: []string{"auditor"}}
	ctx2 := context.WithValue(context.Background(), ClaimsContextKey, claims2)

	_, _ = interceptor(ctx2, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/GetItem"}, noopHandler)

	// Denied access (unmapped method, fail-closed)
	claims3 := &Claims{UserID: "user-3", Roles: []string{"admin"}}
	ctx3 := context.WithValue(context.Background(), ClaimsContextKey, claims3)

	_, _ = interceptor(ctx3, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/Unknown"}, noopHandler)

	if len(logged) != 3 {
		t.Fatalf("expected 3 audit log entries, got %d: %v", len(logged), logged)
	}
	if logged[0] != "/some.Service/GetItem:user-1:allowed" {
		t.Errorf("unexpected audit log entry: %s", logged[0])
	}
	if logged[1] != "/some.Service/GetItem:user-2:denied" {
		t.Errorf("unexpected audit log entry: %s", logged[1])
	}
	if logged[2] != "/some.Service/Unknown:user-3:denied_unmapped" {
		t.Errorf("unexpected audit log entry: %s", logged[2])
	}
}

func TestMethodRBACInterceptor_PublicMethod_NoClaimsAllowed(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/Login": {Public: true},
			"/some.Service/Admin": {AllowedRoles: []Role{RoleAdmin}},
		},
	})

	// No claims in context - should still pass for public method
	ctx := context.Background()

	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/Login"}, noopHandler)
	if err != nil {
		t.Fatalf("expected no error for public method without claims, got %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected handler response 'ok', got %v", resp)
	}
}

func TestMethodRBACInterceptor_PublicMethod_WithClaimsAllowed(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/Login": {Public: true},
		},
	})

	// Claims present - should still pass (public method doesn't check roles)
	claims := &Claims{Roles: []string{"auditor"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/Login"}, noopHandler)
	if err != nil {
		t.Fatalf("expected no error for public method with claims, got %v", err)
	}
	if resp != "ok" {
		t.Errorf("expected handler response 'ok', got %v", resp)
	}
}

func TestMethodRBACInterceptor_PublicMethod_AuditLog(t *testing.T) {
	var logged []string
	logger := func(method, userID, decision string) {
		logged = append(logged, method+":"+userID+":"+decision)
	}

	interceptor := NewMethodRBACInterceptorWithAudit(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/Login": {Public: true},
		},
	}, logger)

	// Without claims
	ctx := context.Background()
	_, _ = interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/Login"}, noopHandler)

	// With claims
	claims := &Claims{UserID: "user-1", Roles: []string{"admin"}}
	ctx2 := context.WithValue(context.Background(), ClaimsContextKey, claims)
	_, _ = interceptor(ctx2, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/Login"}, noopHandler)

	if len(logged) != 2 {
		t.Fatalf("expected 2 audit log entries, got %d: %v", len(logged), logged)
	}
	if logged[0] != "/some.Service/Login::allowed_public" {
		t.Errorf("unexpected audit log entry: %s", logged[0])
	}
	if logged[1] != "/some.Service/Login:user-1:allowed_public" {
		t.Errorf("unexpected audit log entry: %s", logged[1])
	}
}

func TestMethodRBACInterceptor_PublicWithAllowedRoles_FailsClosed(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/Misconfigured": {
				Public:       true,
				AllowedRoles: []Role{RoleAdmin},
			},
		},
	})

	// Even with valid admin claims, the misconfiguration should be rejected
	claims := &Claims{Roles: []string{"admin"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/Misconfigured"}, noopHandler)
	if err == nil {
		t.Fatal("expected error for Public+AllowedRoles misconfiguration, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("expected Internal, got %v", st.Code())
	}
}

func TestMethodRBACInterceptor_PublicWithAllowedRoles_NoClaims_FailsClosed(t *testing.T) {
	interceptor := NewMethodRBACInterceptor(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/Misconfigured": {
				Public:       true,
				AllowedRoles: []Role{RoleAdmin},
			},
		},
	})

	// No claims - should still fail closed due to misconfiguration
	ctx := context.Background()

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/Misconfigured"}, noopHandler)
	if err == nil {
		t.Fatal("expected error for Public+AllowedRoles misconfiguration without claims, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("expected Internal, got %v", st.Code())
	}
}

func TestMethodRBACInterceptor_PublicWithAllowedRoles_AuditLog(t *testing.T) {
	var logged []string
	logger := func(method, userID, decision string) {
		logged = append(logged, method+":"+userID+":"+decision)
	}

	interceptor := NewMethodRBACInterceptorWithAudit(MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/some.Service/Misconfigured": {
				Public:       true,
				AllowedRoles: []Role{RoleAdmin},
			},
		},
	}, logger)

	claims := &Claims{UserID: "user-1", Roles: []string{"admin"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	_, _ = interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/Misconfigured"}, noopHandler)

	if len(logged) != 1 {
		t.Fatalf("expected 1 audit log entry, got %d: %v", len(logged), logged)
	}
	if logged[0] != "/some.Service/Misconfigured:user-1:denied_misconfigured" {
		t.Errorf("unexpected audit log entry: %s", logged[0])
	}
}

func TestMethodRBACInterceptor_AuditLog_AllowUnmapped(t *testing.T) {
	var logged []string
	logger := func(method, userID, decision string) {
		logged = append(logged, method+":"+userID+":"+decision)
	}

	interceptor := NewMethodRBACInterceptorWithAudit(MethodRBACConfig{
		Permissions:   map[string]MethodPermission{},
		AllowUnmapped: true,
	}, logger)

	claims := &Claims{UserID: "user-1", Roles: []string{"admin"}}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	_, _ = interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/some.Service/Any"}, noopHandler)

	if len(logged) != 1 {
		t.Fatalf("expected 1 audit log entry, got %d: %v", len(logged), logged)
	}
	if logged[0] != "/some.Service/Any:user-1:allowed_unmapped" {
		t.Errorf("unexpected audit log entry: %s", logged[0])
	}
}
