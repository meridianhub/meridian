package handler

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
)

// GetDefinition retrieves a specific account type definition by ID.
func (s *AccountTypeService) GetDefinition(ctx context.Context, req *pb.GetDefinitionRequest) (*pb.GetDefinitionResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	def, err := s.registry.GetDefinitionByID(ctx, id)
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "GetDefinition", req.GetId())
	}

	return &pb.GetDefinitionResponse{
		Definition: accountTypeToProto(def),
	}, nil
}

// GetActiveDefinition retrieves the currently active definition for a given code.
func (s *AccountTypeService) GetActiveDefinition(ctx context.Context, req *pb.GetActiveDefinitionRequest) (*pb.GetActiveDefinitionResponse, error) {
	if req.GetCode() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "code is required")
	}

	def, err := s.registry.GetActiveDefinition(ctx, req.GetCode())
	if err != nil {
		return nil, s.mapDomainError(ctx, err, "GetActiveDefinition", req.GetCode())
	}

	return &pb.GetActiveDefinitionResponse{
		Definition: accountTypeToProto(def),
	}, nil
}

// ListActive returns all active account type definitions.
func (s *AccountTypeService) ListActive(ctx context.Context, req *pb.ListActiveRequest) (*pb.ListActiveResponse, error) {
	defs, err := s.registry.ListActive(ctx)
	if err != nil {
		s.logger.Error("failed to list active account types", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list active account types: %v", err)
	}

	defs = filterByBehaviorClass(defs, req.GetBehaviorClassFilter())
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Code < defs[j].Code
	})

	page, nextPageToken := paginateDefinitions(defs, int(req.GetPageSize()), req.GetPageToken())

	definitions := make([]*pb.AccountTypeDefinition, len(page))
	for i, def := range page {
		definitions[i] = accountTypeToProto(def)
	}

	return &pb.ListActiveResponse{
		Definitions:   definitions,
		NextPageToken: nextPageToken,
	}, nil
}

// ListAll returns account type definitions across all statuses.
func (s *AccountTypeService) ListAll(ctx context.Context, req *pb.ListAllRequest) (*pb.ListAllResponse, error) {
	var statusFilter []accounttype.Status
	for _, ps := range req.GetStatusFilter() {
		if d := protoAccountTypeStatusToDomain(ps); d != "" {
			statusFilter = append(statusFilter, d)
		}
	}
	// If the caller provided a non-empty filter but all values were UNSPECIFIED, reject.
	if len(req.GetStatusFilter()) > 0 && len(statusFilter) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "status_filter contains only unrecognised values")
	}

	defs, err := s.registry.ListAll(ctx, statusFilter)
	if err != nil {
		s.logger.Error("failed to list account types", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list account types: %v", err)
	}

	sort.Slice(defs, func(i, j int) bool {
		if defs[i].Code != defs[j].Code {
			return defs[i].Code < defs[j].Code
		}
		return defs[i].Version > defs[j].Version
	})

	page, nextPageToken := paginateAllDefinitions(defs, int(req.GetPageSize()), req.GetPageToken())

	definitions := make([]*pb.AccountTypeDefinition, len(page))
	for i, def := range page {
		definitions[i] = accountTypeToProto(def)
	}

	return &pb.ListAllResponse{
		Definitions:   definitions,
		NextPageToken: nextPageToken,
	}, nil
}

// paginateAllDefinitions paginates a list using a composite code+version cursor.
// This is needed because ListAll may return multiple versions per code.
func paginateAllDefinitions(defs []*accounttype.Definition, reqPageSize int, pageToken string) ([]*accounttype.Definition, string) {
	pageSize := normalizeAccountTypePageSize(reqPageSize)
	startIdx := findAllStartIndex(defs, pageToken)

	if startIdx >= len(defs) {
		return nil, ""
	}

	end := startIdx + pageSize
	if end > len(defs) {
		end = len(defs)
	}
	page := defs[startIdx:end]

	var nextPageToken string
	if end < len(defs) {
		last := page[len(page)-1]
		nextPageToken = fmt.Sprintf("%s\x00%d", last.Code, last.Version)
	}
	return page, nextPageToken
}

// findAllStartIndex finds the start index for paginating a ListAll result using a composite cursor.
// The cursor format is "code\x00version".
func findAllStartIndex(defs []*accounttype.Definition, pageToken string) int {
	if pageToken == "" {
		return 0
	}
	parts := strings.SplitN(pageToken, "\x00", 2)
	if len(parts) != 2 {
		return 0
	}
	cursorCode := parts[0]
	cursorVersion, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	for i, def := range defs {
		if def.Code > cursorCode {
			return i
		}
		if def.Code == cursorCode && def.Version < cursorVersion {
			return i
		}
	}
	return len(defs)
}

func filterByBehaviorClass(defs []*accounttype.Definition, filter pb.BehaviorClass) []*accounttype.Definition {
	if filter == pb.BehaviorClass_BEHAVIOR_CLASS_UNSPECIFIED {
		return defs
	}
	domainBC := protoBehaviorClassToDomain(filter)
	var filtered []*accounttype.Definition
	for _, def := range defs {
		if def.BehaviorClass == domainBC {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

func paginateDefinitions(defs []*accounttype.Definition, reqPageSize int, pageToken string) ([]*accounttype.Definition, string) {
	pageSize := normalizeAccountTypePageSize(reqPageSize)
	startIdx := findStartIndex(defs, pageToken)

	if startIdx >= len(defs) {
		return nil, ""
	}

	end := startIdx + pageSize
	if end > len(defs) {
		end = len(defs)
	}
	page := defs[startIdx:end]

	var nextPageToken string
	if end < len(defs) {
		nextPageToken = page[len(page)-1].Code
	}
	return page, nextPageToken
}

func findStartIndex(defs []*accounttype.Definition, pageToken string) int {
	if pageToken == "" {
		return 0
	}
	for i, def := range defs {
		if def.Code > pageToken {
			return i
		}
	}
	return len(defs)
}

func normalizeAccountTypePageSize(pageSize int) int {
	if pageSize <= 0 {
		return DefaultPageSize
	}
	if pageSize > MaxPageSize {
		return MaxPageSize
	}
	return pageSize
}
