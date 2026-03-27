package saga

import (
	"context"
	"sort"
	"strconv"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
)

// GetSaga retrieves a specific saga by ID or by name+version.
func (h *RegistryHandler) GetSaga(
	ctx context.Context,
	req *sagav1.GetSagaRequest,
) (*sagav1.GetSagaResponse, error) {
	var def *Definition
	var err error

	if req.Id != "" {
		id, parseErr := uuid.Parse(req.Id)
		if parseErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid saga id: %v", parseErr)
		}
		def, err = h.registry.GetByID(ctx, id)
	} else if req.Name != "" {
		if req.Version < 0 {
			return nil, status.Errorf(codes.InvalidArgument, "version must be >= 0")
		}
		if req.Version == 0 {
			def, err = h.registry.GetActive(ctx, req.Name)
		} else {
			def, err = h.registry.GetDefinition(ctx, req.Name, int(req.Version))
		}
	} else {
		return nil, status.Errorf(codes.InvalidArgument, "either id or name must be provided")
	}

	if err != nil {
		identifier := req.Id
		if identifier == "" {
			identifier = req.Name
		}
		return nil, h.mapDomainError(err, "GetSaga", identifier)
	}

	return &sagav1.GetSagaResponse{
		Saga: h.definitionToProto(def),
	}, nil
}

// GetActiveSaga retrieves the active saga for a name using tenant resolution.
func (h *RegistryHandler) GetActiveSaga(
	ctx context.Context,
	req *sagav1.GetActiveSagaRequest,
) (*sagav1.GetActiveSagaResponse, error) {
	def, err := h.registry.GetActive(ctx, req.Name)
	if err != nil {
		return nil, h.mapDomainError(err, "GetActiveSaga", req.Name)
	}

	return &sagav1.GetActiveSagaResponse{
		Saga:             h.definitionToProto(def),
		IsTenantOverride: !def.IsSystem,
	}, nil
}

// ListSagas retrieves sagas matching the filter criteria.
func (h *RegistryHandler) ListSagas(
	ctx context.Context,
	req *sagav1.ListSagasRequest,
) (*sagav1.ListSagasResponse, error) {
	defs, err := h.fetchSagas(ctx, req.StatusFilter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list sagas: %v", err)
	}

	// Filter out system sagas if requested
	if req.ExcludeSystem {
		defs = h.filterNonSystemSagas(defs)
	}

	// Sort for stable pagination
	h.sortDefinitions(defs)

	// Parse and validate pagination
	offset, err := h.parsePageToken(req.PageToken)
	if err != nil {
		return nil, err
	}

	// Apply pagination
	pageSize := h.normalizePageSize(int(req.PageSize))
	paginatedDefs, nextToken := h.paginate(defs, offset, pageSize)

	// Convert to proto
	sagas := make([]*sagav1.SagaDefinition, len(paginatedDefs))
	for i, def := range paginatedDefs {
		sagas[i] = h.definitionToProto(def)
	}

	return &sagav1.ListSagasResponse{
		Sagas:         sagas,
		NextPageToken: nextToken,
	}, nil
}

// fetchSagas retrieves sagas filtered by status.
func (h *RegistryHandler) fetchSagas(ctx context.Context, statusFilter sagav1.SagaStatus) ([]*Definition, error) {
	if statusFilter != sagav1.SagaStatus_SAGA_STATUS_UNSPECIFIED {
		domainStatus := h.protoStatusToDomain(statusFilter)
		return h.registry.ListByStatus(ctx, domainStatus)
	}

	// Get all sagas by listing each status
	drafts, err1 := h.registry.ListByStatus(ctx, StatusDraft)
	actives, err2 := h.registry.ListByStatus(ctx, StatusActive)
	deprecated, err3 := h.registry.ListByStatus(ctx, StatusDeprecated)

	if err1 != nil {
		return nil, err1
	}
	if err2 != nil {
		return nil, err2
	}
	if err3 != nil {
		return nil, err3
	}

	defs := make([]*Definition, 0, len(drafts)+len(actives)+len(deprecated))
	defs = append(defs, drafts...)
	defs = append(defs, actives...)
	defs = append(defs, deprecated...)
	return defs, nil
}

// filterNonSystemSagas returns only non-system sagas.
func (h *RegistryHandler) filterNonSystemSagas(defs []*Definition) []*Definition {
	filtered := make([]*Definition, 0, len(defs))
	for _, def := range defs {
		if !def.IsSystem {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

// sortDefinitions sorts definitions by name, then version for stable pagination.
func (h *RegistryHandler) sortDefinitions(defs []*Definition) {
	sort.Slice(defs, func(i, j int) bool {
		if defs[i].Name == defs[j].Name {
			return defs[i].Version < defs[j].Version
		}
		return defs[i].Name < defs[j].Name
	})
}

// parsePageToken parses the offset from a page token.
func (h *RegistryHandler) parsePageToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(token)
	if err != nil || offset < 0 {
		return 0, status.Error(codes.InvalidArgument, "invalid page_token")
	}
	return offset, nil
}

// normalizePageSize ensures page size is within valid bounds.
func (h *RegistryHandler) normalizePageSize(pageSize int) int {
	if pageSize <= 0 {
		return 50
	}
	if pageSize > 100 {
		return 100
	}
	return pageSize
}

// paginate returns a slice of definitions and the next page token.
func (h *RegistryHandler) paginate(defs []*Definition, offset, pageSize int) ([]*Definition, string) {
	if offset > len(defs) {
		offset = len(defs)
	}

	end := offset + pageSize
	if end > len(defs) {
		end = len(defs)
	}

	nextToken := ""
	if end < len(defs) {
		nextToken = strconv.Itoa(end)
	}

	return defs[offset:end], nextToken
}
