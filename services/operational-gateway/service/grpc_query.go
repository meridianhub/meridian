package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Sentinel errors for page token parsing.
var (
	errInvalidPageTokenFormat = errors.New("invalid token format")
	errNegativePageOffset     = errors.New("offset cannot be negative")
)

// GetInstruction retrieves a specific instruction by ID.
func (s *OperationalGatewayService) GetInstruction(
	ctx context.Context,
	req *opgatewayv1.GetInstructionRequest,
) (*opgatewayv1.GetInstructionResponse, error) {
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
		s.logger.Error("failed to retrieve instruction", "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve instruction")
	}

	// Verify tenant ownership.
	if instruction.TenantID.String() != tenantIDToUUID(tid) {
		return nil, status.Errorf(codes.NotFound, "instruction not found: %s", req.InstructionId)
	}

	return &opgatewayv1.GetInstructionResponse{
		Instruction: instructionToProto(instruction),
	}, nil
}

// ListInstructions returns a paginated list of instructions with optional filtering.
func (s *OperationalGatewayService) ListInstructions(
	ctx context.Context,
	req *opgatewayv1.ListInstructionsRequest,
) (*opgatewayv1.ListInstructionsResponse, error) {
	tid, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}

	// Parse pagination.
	pageSize := defaultPageSize
	offset := 0
	if req.Pagination != nil {
		if req.Pagination.PageSize > 0 {
			pageSize = int(req.Pagination.PageSize)
			if pageSize > maxPageSize {
				pageSize = maxPageSize
			}
		}
		if req.Pagination.PageToken != "" {
			offset, err = decodeOffsetToken(req.Pagination.PageToken)
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, "invalid page_token")
			}
		}
	}

	// Build list params.
	params := ports.ListInstructionsParams{
		TenantID:             tenantIDToUUID(tid),
		InstructionType:      req.InstructionType,
		ProviderConnectionID: req.ProviderConnectionId,
		Limit:                pageSize,
		Offset:               offset,
	}

	// Parse status filters.
	for _, s := range req.Status {
		if s == opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_UNSPECIFIED {
			continue
		}
		domainStatus := protoToDomainStatus(s)
		if domainStatus == "" {
			return nil, status.Errorf(codes.InvalidArgument, "invalid status filter: %v", s)
		}
		params.Statuses = append(params.Statuses, domainStatus)
	}

	// Parse date range.
	if req.DateRange != nil {
		if req.DateRange.StartDate != "" {
			t, parseErr := parseDate(req.DateRange.StartDate)
			if parseErr != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid date_range.start_date: %v", parseErr)
			}
			params.CreatedAfter = t
		}
		if req.DateRange.EndDate != "" {
			t, parseErr := parseDate(req.DateRange.EndDate)
			if parseErr != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid date_range.end_date: %v", parseErr)
			}
			// Include records created through the end of the specified day.
			params.CreatedBefore = t.Add(24*time.Hour - time.Nanosecond)
		}
	}

	instructions, total, err := s.instructionRepo.ListByTenant(ctx, params)
	if err != nil {
		s.logger.Error("failed to list instructions", "error", err)
		return nil, status.Error(codes.Internal, "failed to list instructions")
	}

	protoInstructions := make([]*opgatewayv1.Instruction, 0, len(instructions))
	for _, inst := range instructions {
		protoInstructions = append(protoInstructions, instructionToProto(inst))
	}

	// Build next page token if there are more results.
	nextOffset := offset + len(instructions)
	var nextPageToken string
	if int64(nextOffset) < total {
		nextPageToken = encodeOffsetToken(nextOffset)
	}

	return &opgatewayv1.ListInstructionsResponse{
		Instructions: protoInstructions,
		Pagination: &commonpb.PaginationResponse{
			NextPageToken: nextPageToken,
			TotalCount:    total,
		},
	}, nil
}

// ProcessCallback handles an inbound callback from a provider, acknowledging an instruction.
func (s *OperationalGatewayService) ProcessCallback(
	ctx context.Context,
	req *opgatewayv1.ProcessCallbackRequest,
) (*opgatewayv1.ProcessCallbackResponse, error) {
	tid, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}

	if req.IdempotencyKey == nil || req.IdempotencyKey.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}
	if req.Callback == nil {
		return nil, status.Error(codes.InvalidArgument, "callback is required")
	}

	// Resolve the instruction by ID or provider_reference.
	// provider_reference lookup requires a repository method not yet implemented; return
	// Unimplemented until that capability is added in a future iteration.
	var id uuid.UUID
	if req.InstructionId != "" {
		id, err = uuid.Parse(req.InstructionId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid instruction_id: %v", err)
		}
	} else if req.ProviderReference != "" {
		return nil, status.Error(codes.Unimplemented, "lookup by provider_reference is not yet supported; provide instruction_id instead")
	} else {
		return nil, status.Error(codes.InvalidArgument, "at least one of instruction_id or provider_reference must be provided")
	}

	instruction, err := s.instructionRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, ports.ErrInstructionNotFound) {
			return nil, status.Errorf(codes.NotFound, "instruction not found: %s", req.InstructionId)
		}
		s.logger.Error("failed to retrieve instruction for callback", "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve instruction")
	}

	// Verify tenant ownership.
	if instruction.TenantID.String() != tenantIDToUUID(tid) {
		return nil, status.Errorf(codes.NotFound, "instruction not found: %s", req.InstructionId)
	}

	// Idempotency: if already acknowledged, return the current state without re-applying.
	if instruction.Status == domain.InstructionStatusAcknowledged {
		return &opgatewayv1.ProcessCallbackResponse{
			Instruction: instructionToProto(instruction),
		}, nil
	}

	// Transition to ACKNOWLEDGED from DELIVERED.
	if err := instruction.MarkAcknowledged(); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "instruction cannot be acknowledged in status %s", instruction.Status)
	}

	if err := s.instructionRepo.Save(ctx, instruction, req.IdempotencyKey.Key); err != nil {
		if errors.Is(err, ports.ErrInstructionConflict) {
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		}
		if errors.Is(err, ports.ErrDuplicateIdempotency) {
			// Idempotent: return the current state.
			return &opgatewayv1.ProcessCallbackResponse{
				Instruction: instructionToProto(instruction),
			}, nil
		}
		s.logger.Error("failed to save acknowledged instruction", "error", err)
		return nil, status.Error(codes.Internal, "failed to save instruction")
	}

	return &opgatewayv1.ProcessCallbackResponse{
		Instruction: instructionToProto(instruction),
	}, nil
}

// encodeOffsetToken encodes a numeric offset as an opaque page token.
func encodeOffsetToken(offset int) string {
	return fmt.Sprintf("offset:%d", offset)
}

// decodeOffsetToken decodes an opaque page token back to a numeric offset.
func decodeOffsetToken(token string) (int, error) {
	const prefix = "offset:"
	if !strings.HasPrefix(token, prefix) {
		return 0, errInvalidPageTokenFormat
	}
	n, err := strconv.Atoi(strings.TrimPrefix(token, prefix))
	if err != nil {
		return 0, fmt.Errorf("invalid token value: %w", err)
	}
	if n < 0 {
		return 0, errNegativePageOffset
	}
	return n, nil
}

// parseDate parses a YYYY-MM-DD date string to a time.Time (UTC midnight).
func parseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}
