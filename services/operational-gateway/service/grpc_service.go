// Package service implements gRPC services for the operational gateway domain.
package service

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/google/uuid"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// Pagination defaults.
const (
	defaultPageSize = 50
	maxPageSize     = 1000
)

// Service errors.
var (
	ErrInstructionRepoNil = errors.New("instruction repository cannot be nil")
	ErrConnectionRepoNil  = errors.New("connection repository cannot be nil")
)

// OperationalGatewayService implements OperationalGatewayServiceServer.
type OperationalGatewayService struct {
	opgatewayv1.UnimplementedOperationalGatewayServiceServer
	instructionRepo ports.InstructionRepository
	connectionRepo  ports.ConnectionRepository
	logger          *slog.Logger
}

// ProviderConnectionService implements ProviderConnectionServiceServer.
type ProviderConnectionService struct {
	opgatewayv1.UnimplementedProviderConnectionServiceServer
	connectionRepo  ports.ConnectionRepository
	instructionRepo ports.InstructionRepository
	logger          *slog.Logger
}

// NewOperationalGatewayService creates a new OperationalGatewayService.
func NewOperationalGatewayService(
	instructionRepo ports.InstructionRepository,
	connectionRepo ports.ConnectionRepository,
	logger *slog.Logger,
) (*OperationalGatewayService, error) {
	if instructionRepo == nil {
		return nil, ErrInstructionRepoNil
	}
	if connectionRepo == nil {
		return nil, ErrConnectionRepoNil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &OperationalGatewayService{
		instructionRepo: instructionRepo,
		connectionRepo:  connectionRepo,
		logger:          logger,
	}, nil
}

// NewProviderConnectionService creates a new ProviderConnectionService.
func NewProviderConnectionService(
	connectionRepo ports.ConnectionRepository,
	instructionRepo ports.InstructionRepository,
	logger *slog.Logger,
) (*ProviderConnectionService, error) {
	if connectionRepo == nil {
		return nil, ErrConnectionRepoNil
	}
	if instructionRepo == nil {
		return nil, ErrInstructionRepoNil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &ProviderConnectionService{
		connectionRepo:  connectionRepo,
		instructionRepo: instructionRepo,
		logger:          logger,
	}, nil
}

// requireTenant extracts the tenant ID from context, returning FailedPrecondition if missing.
func requireTenant(ctx context.Context) (tenant.TenantID, error) {
	tid, ok := tenant.FromContext(ctx)
	if !ok {
		return "", status.Error(codes.FailedPrecondition, "tenant context is required")
	}
	return tid, nil
}

// DispatchInstruction accepts a new instruction and queues it for dispatch.
func (s *OperationalGatewayService) DispatchInstruction(
	ctx context.Context,
	req *opgatewayv1.DispatchInstructionRequest,
) (*opgatewayv1.DispatchInstructionResponse, error) {
	tid, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}

	// Validate required fields.
	if req.InstructionType == "" {
		return nil, status.Error(codes.InvalidArgument, "instruction_type is required")
	}
	if req.Payload == nil {
		return nil, status.Error(codes.InvalidArgument, "payload is required")
	}
	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}

	// Convert payload proto -> domain map.
	payload := structToMap(req.Payload)

	// Build functional options.
	opts := []domain.InstructionOption{
		domain.WithPriority(protoToDomainPriority(req.Priority)),
	}
	if req.CorrelationId != "" {
		opts = append(opts, domain.WithCorrelationID(req.CorrelationId))
	}
	if req.CausationId != "" {
		opts = append(opts, domain.WithCausationID(req.CausationId))
	}
	if len(req.Metadata) > 0 {
		opts = append(opts, domain.WithMetadata(req.Metadata))
	}
	if req.ScheduledAt != nil {
		t := req.ScheduledAt.AsTime()
		opts = append(opts, domain.WithScheduledAt(t))
	}
	if req.ExpiresAt != nil {
		t := req.ExpiresAt.AsTime()
		opts = append(opts, domain.WithExpiresAt(t))
	}

	// Convert the tenant ID string to a UUID for the domain model.
	// The tenant.TenantID may be a UUID string (from JWT claims) or an alphanumeric slug.
	// tenantIDToUUID produces a stable UUID in either case.
	tenantUUID, parseErr := uuid.Parse(tenantIDToUUID(tid))
	if parseErr != nil {
		s.logger.Error("failed to parse tenant ID as UUID", "tenant_id", tid, "error", parseErr)
		return nil, status.Error(codes.Internal, "invalid tenant context")
	}

	// Resolve provider connection: for dispatch, we don't require a connection_id upfront —
	// the worker resolves it via InstructionRoute. We use uuid.Nil as the sentinel that
	// the dispatcher replaces. uuid.Nil is a valid UUID string so it passes persistence
	// layer parsing and is clearly distinguishable from a real connection ID.
	instruction, err := domain.NewInstruction(
		tenantUUID,
		req.InstructionType,
		uuid.Nil.String(),
		payload,
		opts...,
	)
	if err != nil {
		s.logger.Error("failed to create instruction domain object", "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid instruction: %v", err)
	}

	// Assign a fresh UUID for this instruction.
	instruction.ID = uuid.New()

	if err := s.instructionRepo.Save(ctx, instruction, req.IdempotencyKey.Key); err != nil {
		if errors.Is(err, ports.ErrDuplicateIdempotency) {
			return nil, status.Error(codes.AlreadyExists, "instruction with this idempotency key already exists")
		}
		s.logger.Error("failed to save instruction", "error", err)
		return nil, status.Error(codes.Internal, "failed to save instruction")
	}

	return &opgatewayv1.DispatchInstructionResponse{
		Instruction: instructionToProto(instruction),
	}, nil
}

// CancelInstruction cancels a pending instruction.
func (s *OperationalGatewayService) CancelInstruction(
	ctx context.Context,
	req *opgatewayv1.CancelInstructionRequest,
) (*opgatewayv1.CancelInstructionResponse, error) {
	tid, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.InstructionId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid instruction_id: %v", err)
	}

	instruction, err := s.instructionRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, ports.ErrInstructionNotFound) {
			return nil, status.Errorf(codes.NotFound, "instruction not found: %s", req.InstructionId)
		}
		s.logger.Error("failed to find instruction", "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve instruction")
	}

	// Verify tenant ownership.
	if instruction.TenantID.String() != tenantIDToUUID(tid) {
		return nil, status.Errorf(codes.NotFound, "instruction not found: %s", req.InstructionId)
	}

	if err := instruction.Cancel(); err != nil {
		if errors.Is(err, domain.ErrInstructionNotCancellable) {
			return nil, status.Errorf(codes.FailedPrecondition, "instruction cannot be cancelled in status %s", instruction.Status)
		}
		return nil, status.Errorf(codes.Internal, "failed to cancel instruction: %v", err)
	}

	if err := s.instructionRepo.Save(ctx, instruction, ""); err != nil {
		if errors.Is(err, ports.ErrInstructionConflict) {
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		}
		s.logger.Error("failed to save cancelled instruction", "error", err)
		return nil, status.Error(codes.Internal, "failed to save instruction")
	}

	return &opgatewayv1.CancelInstructionResponse{
		Instruction: instructionToProto(instruction),
	}, nil
}

// tenantIDToUUID converts a tenant.TenantID string to a UUID string.
// Tenant IDs are stored as UUID strings in the instruction domain.
// If the tenant ID is already a valid UUID string, return it directly.
// Otherwise, generate a deterministic UUID from the tenant ID.
func tenantIDToUUID(tid tenant.TenantID) string {
	_, err := uuid.Parse(tid.String())
	if err == nil {
		return tid.String()
	}
	// Derive a deterministic UUID v5 from the tenant ID string.
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(tid.String())).String()
}

// structToMap converts a protobuf Struct to a Go map[string]any.
func structToMap(s *structpb.Struct) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(s.Fields))
	for k, v := range s.Fields {
		result[k] = protoValueToAny(v)
	}
	return result
}

// protoValueToAny converts a structpb.Value to a native Go value.
func protoValueToAny(v *structpb.Value) any {
	if v == nil {
		return nil
	}
	switch k := v.Kind.(type) {
	case *structpb.Value_NullValue:
		return nil
	case *structpb.Value_BoolValue:
		return k.BoolValue
	case *structpb.Value_NumberValue:
		return k.NumberValue
	case *structpb.Value_StringValue:
		return k.StringValue
	case *structpb.Value_ListValue:
		if k.ListValue == nil {
			return []any{}
		}
		items := make([]any, len(k.ListValue.Values))
		for i, item := range k.ListValue.Values {
			items[i] = protoValueToAny(item)
		}
		return items
	case *structpb.Value_StructValue:
		return structToMap(k.StructValue)
	default:
		return nil
	}
}
