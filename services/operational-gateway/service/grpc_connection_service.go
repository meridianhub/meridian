package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UpsertConnection creates or updates a provider connection.
func (s *ProviderConnectionService) UpsertConnection(
	ctx context.Context,
	req *opgatewayv1.UpsertConnectionRequest,
) (*opgatewayv1.UpsertConnectionResponse, error) {
	tid, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}

	authConfig := protoToDomainAuthConfig(req)
	if authConfig == nil {
		return nil, status.Error(codes.InvalidArgument, "auth_config is required")
	}

	retryPolicy := protoToDomainRetryPolicy(req.RetryPolicy)
	rateLimit := protoToDomainRateLimit(req.RateLimit)
	protocol := protoToDomainProtocol(req.Protocol)

	conn, err := domain.NewProviderConnection(
		tenantIDToUUID(tid),
		req.ProviderName,
		req.ProviderType,
		protocol,
		req.BaseUrl,
		authConfig,
		retryPolicy,
		rateLimit,
	)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid connection: %v", err)
	}

	// Override the generated UUID if the caller specified one (upsert semantics).
	if req.ConnectionId != "" {
		if _, parseErr := uuid.Parse(req.ConnectionId); parseErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid connection_id: %v", parseErr)
		}
		conn.ConnectionID = req.ConnectionId
	}

	if err := s.connectionRepo.Upsert(ctx, conn); err != nil {
		s.logger.Error("failed to upsert provider connection", "error", err)
		return nil, status.Error(codes.Internal, "failed to upsert connection")
	}

	return &opgatewayv1.UpsertConnectionResponse{
		Connection: connectionToProto(conn),
	}, nil
}

// GetConnection retrieves a specific provider connection by ID.
func (s *ProviderConnectionService) GetConnection(
	ctx context.Context,
	req *opgatewayv1.GetConnectionRequest,
) (*opgatewayv1.GetConnectionResponse, error) {
	tid, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}

	conn, err := s.connectionRepo.FindByID(ctx, tenantIDToUUID(tid), req.ConnectionId)
	if err != nil {
		if errors.Is(err, ports.ErrConnectionNotFound) {
			return nil, status.Errorf(codes.NotFound, "connection not found: %s", req.ConnectionId)
		}
		s.logger.Error("failed to retrieve connection", "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve connection")
	}

	return &opgatewayv1.GetConnectionResponse{
		Connection: connectionToProto(conn),
	}, nil
}

// ListConnections returns a paginated list of provider connections.
func (s *ProviderConnectionService) ListConnections(
	ctx context.Context,
	req *opgatewayv1.ListConnectionsRequest,
) (*opgatewayv1.ListConnectionsResponse, error) {
	tid, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}

	pageSize, offset, err := parsePagination(req.Pagination)
	if err != nil {
		return nil, err
	}

	all, err := s.connectionRepo.ListByTenant(ctx, tenantIDToUUID(tid))
	if err != nil {
		s.logger.Error("failed to list connections", "error", err)
		return nil, status.Error(codes.Internal, "failed to list connections")
	}

	filtered := filterConnections(all, req)

	// Apply pagination.
	total := int64(len(filtered))
	if offset > len(filtered) {
		offset = len(filtered)
	}
	page := filtered[offset:]
	if len(page) > pageSize {
		page = page[:pageSize]
	}

	protoConns := make([]*opgatewayv1.ProviderConnection, 0, len(page))
	for _, conn := range page {
		protoConns = append(protoConns, connectionToProto(conn))
	}

	nextOffset := offset + len(page)
	var nextPageToken string
	if int64(nextOffset) < total {
		nextPageToken = encodeOffsetToken(nextOffset)
	}

	return &opgatewayv1.ListConnectionsResponse{
		Connections: protoConns,
		Pagination: &commonpb.PaginationResponse{
			NextPageToken: nextPageToken,
			TotalCount:    total,
		},
	}, nil
}

// filterConnections applies optional protocol and health status filters to a connection list.
func filterConnections(all []*domain.ProviderConnection, req *opgatewayv1.ListConnectionsRequest) []*domain.ProviderConnection {
	filtered := make([]*domain.ProviderConnection, 0, len(all))
	for _, conn := range all {
		if req.Protocol != opgatewayv1.Protocol_PROTOCOL_UNSPECIFIED &&
			protoToDomainProtocol(req.Protocol) != conn.Protocol {
			continue
		}
		if req.HealthStatus != opgatewayv1.HealthStatus_HEALTH_STATUS_UNSPECIFIED &&
			domainToProtoHealthStatus(conn.HealthStatus) != req.HealthStatus {
			continue
		}
		filtered = append(filtered, conn)
	}
	return filtered
}

// TestConnection performs a health check on a provider connection (Phase 2 placeholder).
func (s *ProviderConnectionService) TestConnection(
	ctx context.Context,
	req *opgatewayv1.TestConnectionRequest,
) (*opgatewayv1.TestConnectionResponse, error) {
	tid, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}

	// Verify the connection exists and belongs to the tenant.
	_, err = s.connectionRepo.FindByID(ctx, tenantIDToUUID(tid), req.ConnectionId)
	if err != nil {
		if errors.Is(err, ports.ErrConnectionNotFound) {
			return nil, status.Errorf(codes.NotFound, "connection not found: %s", req.ConnectionId)
		}
		s.logger.Error("failed to retrieve connection for health check", "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve connection")
	}

	return nil, status.Error(codes.Unimplemented, "TestConnection is reserved for Phase 2")
}
