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
	"github.com/meridianhub/meridian/shared/platform/tenant"
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

	id, err := uuid.Parse(req.GetInstructionId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid instruction_id: %v", err)
	}

	instruction, err := s.instructionRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, ports.ErrInstructionNotFound) {
			return nil, status.Errorf(codes.NotFound, "instruction not found: %s", req.GetInstructionId())
		}
		s.logger.Error("failed to retrieve instruction", "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve instruction")
	}

	// Verify tenant ownership.
	if instruction.TenantID.String() != tenantIDToUUID(tid) {
		return nil, status.Errorf(codes.NotFound, "instruction not found: %s", req.GetInstructionId())
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

	pageSize, offset, err := parsePagination(req.GetPagination())
	if err != nil {
		return nil, err
	}

	params := ports.ListInstructionsParams{
		TenantID:             tenantIDToUUID(tid),
		InstructionType:      req.GetInstructionType(),
		ProviderConnectionID: req.GetProviderConnectionId(),
		Limit:                pageSize,
		Offset:               offset,
	}

	if err := applyStatusFilters(&params, req.GetStatus()); err != nil {
		return nil, err
	}
	if err := applyDateRange(&params, req.GetDateRange()); err != nil {
		return nil, err
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

// parsePagination extracts page size and offset from a pagination request.
func parsePagination(pag *commonpb.Pagination) (int, int, error) {
	pageSize := defaultPageSize
	offset := 0
	if pag == nil {
		return pageSize, offset, nil
	}
	if pag.GetPageSize() > 0 {
		pageSize = int(pag.GetPageSize())
		if pageSize > maxPageSize {
			pageSize = maxPageSize
		}
	}
	if pag.GetPageToken() != "" {
		var err error
		offset, err = decodeOffsetToken(pag.GetPageToken())
		if err != nil {
			return 0, 0, status.Error(codes.InvalidArgument, "invalid page_token")
		}
	}
	return pageSize, offset, nil
}

// applyStatusFilters parses proto status values and appends domain statuses to the params.
func applyStatusFilters(params *ports.ListInstructionsParams, statuses []opgatewayv1.InstructionStatus) error {
	for _, s := range statuses {
		if s == opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_UNSPECIFIED {
			continue
		}
		domainStatus := protoToDomainStatus(s)
		if domainStatus == "" {
			return status.Errorf(codes.InvalidArgument, "invalid status filter: %v", s)
		}
		params.Statuses = append(params.Statuses, domainStatus)
	}
	return nil
}

// applyDateRange parses and validates the date range filter, updating the params.
func applyDateRange(params *ports.ListInstructionsParams, dateRange *commonpb.DateRange) error {
	if dateRange == nil {
		return nil
	}

	var startDate, endDate time.Time
	if dateRange.GetStartDate() != "" {
		t, parseErr := parseDate(dateRange.GetStartDate())
		if parseErr != nil {
			return status.Errorf(codes.InvalidArgument, "invalid date_range.start_date: %v", parseErr)
		}
		startDate = t
		params.CreatedAfter = t
	}
	if dateRange.GetEndDate() != "" {
		t, parseErr := parseDate(dateRange.GetEndDate())
		if parseErr != nil {
			return status.Errorf(codes.InvalidArgument, "invalid date_range.end_date: %v", parseErr)
		}
		endDate = t.Add(24*time.Hour - time.Nanosecond)
		params.CreatedBefore = endDate
	}
	if !startDate.IsZero() && !endDate.IsZero() && startDate.After(endDate) {
		return status.Error(codes.InvalidArgument, "date_range.start_date must not be after date_range.end_date")
	}
	return nil
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

	if err := validateCallbackRequest(req); err != nil {
		return nil, err
	}

	instruction, err := s.resolveCallbackInstruction(ctx, tid, req)
	if err != nil {
		return nil, err
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

	if err := s.instructionRepo.Save(ctx, instruction, req.GetIdempotencyKey().GetKey()); err != nil {
		if errors.Is(err, ports.ErrInstructionConflict) {
			return nil, status.Error(codes.Aborted, "concurrent modification detected, please retry")
		}
		if errors.Is(err, ports.ErrDuplicateIdempotency) {
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

// validateCallbackRequest validates required fields on the callback request.
func validateCallbackRequest(req *opgatewayv1.ProcessCallbackRequest) error {
	if req.GetIdempotencyKey() == nil || req.GetIdempotencyKey().GetKey() == "" {
		return status.Error(codes.InvalidArgument, "idempotency_key is required")
	}
	if req.GetCallback() == nil {
		return status.Error(codes.InvalidArgument, "callback is required")
	}
	return nil
}

// resolveCallbackInstruction resolves and validates the instruction referenced by the callback.
func (s *OperationalGatewayService) resolveCallbackInstruction(ctx context.Context, tid tenant.TenantID, req *opgatewayv1.ProcessCallbackRequest) (*domain.Instruction, error) {
	var id uuid.UUID
	var err error
	if req.GetInstructionId() != "" {
		id, err = uuid.Parse(req.GetInstructionId())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid instruction_id: %v", err)
		}
	} else if req.GetProviderReference() != "" {
		return nil, status.Error(codes.Unimplemented, "lookup by provider_reference is not yet supported; provide instruction_id instead")
	} else {
		return nil, status.Error(codes.InvalidArgument, "at least one of instruction_id or provider_reference must be provided")
	}

	instruction, err := s.instructionRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, ports.ErrInstructionNotFound) {
			return nil, status.Errorf(codes.NotFound, "instruction not found: %s", req.GetInstructionId())
		}
		s.logger.Error("failed to retrieve instruction for callback", "error", err)
		return nil, status.Error(codes.Internal, "failed to retrieve instruction")
	}

	if instruction.TenantID.String() != tenantIDToUUID(tid) {
		return nil, status.Errorf(codes.NotFound, "instruction not found: %s", req.GetInstructionId())
	}

	return instruction, nil
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
