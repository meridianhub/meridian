package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// AssertBalance evaluates a balance assertion against current positions.
func (s *AccountReconciliationService) AssertBalance(
	ctx context.Context,
	req *reconciliationv1.AssertBalanceRequest,
) (*reconciliationv1.AssertBalanceResponse, error) {
	if s.assertor == nil {
		return nil, status.Error(codes.Unimplemented, "AssertBalance not yet implemented")
	}

	expectedBalance, err := decimal.NewFromString(req.GetExpectedBalance())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid expected_balance: %v", err)
	}

	var runID *uuid.UUID
	if req.GetRunId() != "" {
		parsed, err := uuid.Parse(req.GetRunId())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid run_id: %v", err)
		}
		runID = &parsed
	}

	// Extract caller role from gRPC metadata
	callerRole := extractCallerRole(ctx)

	// Determine scope from expression or default
	scope := inferScope(req.GetExpression(), req.GetAccountId())

	result, err := s.assertor.ExecuteBalanceAssertion(ctx, AssertBalanceRequest{
		AccountID:       req.GetAccountId(),
		InstrumentCode:  req.GetInstrumentCode(),
		Expression:      req.GetExpression(),
		ExpectedBalance: expectedBalance,
		RunID:           runID,
		Scope:           scope,
		CallerRole:      callerRole,
	})
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		if errors.Is(err, domain.ErrUnimplemented) {
			return nil, status.Error(codes.Unimplemented, "NOSTRO_VOSTRO scope not yet implemented")
		}
		if errors.Is(err, domain.ErrEmptyAccountID) ||
			errors.Is(err, domain.ErrEmptyInstrumentCode) ||
			errors.Is(err, domain.ErrEmptyAssertionExpression) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "balance assertion failed")
	}

	return &reconciliationv1.AssertBalanceResponse{
		Assertion: toProtoAssertionDetail(result.Assertion),
	}, nil
}

// extractCallerRole determines the caller's role from validated JWT claims.
// When auth is enabled, the auth interceptor validates the JWT and stores claims
// in context. This function extracts the role from those validated claims.
// When auth is disabled (development mode), no claims are present in context,
// so it falls back to reading the role from gRPC metadata headers.
func extractCallerRole(ctx context.Context) CallerRole {
	// Primary: extract role from validated JWT claims (production path)
	if claims, ok := auth.GetClaimsFromContext(ctx); ok {
		return mapClaimsToCallerRole(claims)
	}

	// Fallback: no JWT claims in context means auth is disabled (development mode).
	// Trust metadata-based role extraction for local development and testing.
	return extractCallerRoleFromMetadata(ctx)
}

// mapClaimsToCallerRole maps validated JWT claims to a CallerRole.
// The role hierarchy uses auth.Role constants from the RBAC system:
//   - auth.RoleService ("service") -> CallerRoleSystem (service-to-service calls)
//   - auth.RoleAdmin ("admin") -> CallerRoleSystem (admin has full access)
//   - auth.RoleAuditor ("auditor") -> CallerRoleAuditor (read-only audit access)
//   - All others -> CallerRoleTenantAdmin (default tenant-scoped access)
func mapClaimsToCallerRole(claims *auth.Claims) CallerRole {
	if claims.HasRole(auth.RoleService.String()) || claims.HasRole(auth.RoleAdmin.String()) {
		return CallerRoleSystem
	}
	if claims.HasRole(auth.RoleAuditor.String()) {
		return CallerRoleAuditor
	}
	return CallerRoleTenantAdmin
}

// extractCallerRoleFromMetadata reads the caller's role from gRPC metadata.
// This is only used when auth is disabled (AUTH_ENABLED=false) for development.
func extractCallerRoleFromMetadata(ctx context.Context) CallerRole {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return CallerRoleTenantAdmin
	}

	roles := md.Get("x-meridian-role")
	if len(roles) == 0 {
		return CallerRoleTenantAdmin
	}

	switch roles[0] {
	case "SYSTEM":
		return CallerRoleSystem
	case "AUDITOR":
		return CallerRoleAuditor
	default:
		return CallerRoleTenantAdmin
	}
}

// inferScope determines the assertion scope from the expression and account ID.
func inferScope(expression, accountID string) domain.AssertionScope {
	// If the expression mentions cross-account or the account is a system marker
	if accountID == "SYSTEM" || accountID == "*" {
		return domain.AssertionScopeCrossAccount
	}
	_ = expression // Expression-based scope inference can be added later
	return domain.AssertionScopePositionLedger
}
