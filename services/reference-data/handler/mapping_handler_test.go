package handler_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	handlerpkg "github.com/meridianhub/meridian/services/reference-data/handler"
	"github.com/meridianhub/meridian/services/reference-data/mapping"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// --- In-memory stub repository ---

type stubMappingRepo struct {
	defs   map[uuid.UUID]*mapping.Definition
	failOn string
}

func newStubRepo() *stubMappingRepo {
	return &stubMappingRepo{defs: make(map[uuid.UUID]*mapping.Definition)}
}

func (r *stubMappingRepo) Create(ctx context.Context, def *mapping.Definition) error {
	if r.failOn == "create" {
		return mapping.ErrAlreadyExists
	}
	if def.ID == uuid.Nil {
		def.ID = uuid.New()
	}
	def.Status = mapping.StatusDraft
	def.CreatedAt = time.Now()
	def.UpdatedAt = time.Now()

	tid, _ := tenant.FromContext(ctx)
	def.TenantID = tid.String()
	r.defs[def.ID] = def
	return nil
}

func (r *stubMappingRepo) GetByID(_ context.Context, id uuid.UUID) (*mapping.Definition, error) {
	if r.failOn == "get" {
		return nil, mapping.ErrNotFound
	}
	def, ok := r.defs[id]
	if !ok {
		return nil, mapping.ErrNotFound
	}
	return def, nil
}

func (r *stubMappingRepo) GetLatestActive(_ context.Context, name string) (*mapping.Definition, error) {
	for _, d := range r.defs {
		if d.Name == name && d.Status == mapping.StatusActive {
			return d, nil
		}
	}
	return nil, mapping.ErrNotFound
}

func (r *stubMappingRepo) GetByNameAndVersion(_ context.Context, name string, version int) (*mapping.Definition, error) {
	for _, d := range r.defs {
		if d.Name == name && d.Version == version {
			return d, nil
		}
	}
	return nil, mapping.ErrNotFound
}

func (r *stubMappingRepo) ListByTenant(_ context.Context, _ mapping.Status, _ string, _ int, _ string) ([]*mapping.Definition, int, error) {
	results := make([]*mapping.Definition, 0, len(r.defs))
	for _, d := range r.defs {
		results = append(results, d)
	}
	return results, len(results), nil
}

func (r *stubMappingRepo) Update(_ context.Context, def *mapping.Definition, _ time.Time) error {
	if r.failOn == "update" {
		return mapping.ErrNotDraft
	}
	existing, ok := r.defs[def.ID]
	if !ok {
		return mapping.ErrNotFound
	}
	existing.Name = def.Name
	existing.ExternalSchema = def.ExternalSchema
	existing.Fields = def.Fields
	existing.UpdatedAt = time.Now()
	return nil
}

func (r *stubMappingRepo) UpdateStatus(_ context.Context, id uuid.UUID, newStatus mapping.Status) error {
	def, ok := r.defs[id]
	if !ok {
		return mapping.ErrNotFound
	}
	def.Status = newStatus
	return nil
}

func (r *stubMappingRepo) Delete(_ context.Context, id uuid.UUID) error {
	if r.failOn == "delete" {
		return mapping.ErrNotActive
	}
	def, ok := r.defs[id]
	if !ok {
		return mapping.ErrNotFound
	}
	if def.Status == mapping.StatusActive {
		return mapping.ErrNotActive
	}
	delete(r.defs, id)
	return nil
}

// --- Helpers ---

func newMappingService(t *testing.T, repo mapping.Repository) *handlerpkg.MappingService {
	t.Helper()
	svc, err := handlerpkg.NewMappingService(repo, nil, nil)
	require.NoError(t, err)
	return svc
}

func tenantCtx() context.Context {
	return tenant.WithTenant(context.Background(), tenant.MustNewTenantID("testtenant01"))
}

// --- Tests ---

func TestMappingService_CreateMapping_Success(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)

	resp, err := svc.CreateMapping(tenantCtx(), &pb.CreateMappingRequest{
		Name:          "my-mapping",
		TargetService: "meridian.payment_order.v1.PaymentOrderService",
		TargetRpc:     "InitiatePaymentOrder",
		Version:       1,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.GetMapping().GetId())
	assert.Equal(t, "my-mapping", resp.GetMapping().GetName())
	assert.Equal(t, pb.MappingStatus_MAPPING_STATUS_DRAFT, resp.GetMapping().GetStatus())
}

func TestMappingService_CreateMapping_AlreadyExists(t *testing.T) {
	repo := newStubRepo()
	repo.failOn = "create"
	svc := newMappingService(t, repo)

	_, err := svc.CreateMapping(tenantCtx(), &pb.CreateMappingRequest{
		Name:          "dup",
		TargetService: "svc",
		TargetRpc:     "Rpc",
		Version:       1,
	})
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestMappingService_GetMapping_NotFound(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)

	_, err := svc.GetMapping(tenantCtx(), &pb.GetMappingRequest{Id: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestMappingService_GetMapping_InvalidUUID(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)

	_, err := svc.GetMapping(tenantCtx(), &pb.GetMappingRequest{Id: "not-a-uuid"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestMappingService_GetMapping_Success(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)
	ctx := tenantCtx()

	// Create first
	createResp, err := svc.CreateMapping(ctx, &pb.CreateMappingRequest{
		Name:          "get-test",
		TargetService: "svc",
		TargetRpc:     "Rpc",
		Version:       1,
	})
	require.NoError(t, err)

	getResp, err := svc.GetMapping(ctx, &pb.GetMappingRequest{Id: createResp.GetMapping().GetId()})
	require.NoError(t, err)
	assert.Equal(t, createResp.GetMapping().GetId(), getResp.GetMapping().GetId())
}

func TestMappingService_ListMappings(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)
	ctx := tenantCtx()

	for i := 1; i <= 3; i++ {
		_, err := svc.CreateMapping(ctx, &pb.CreateMappingRequest{
			Name:          "m-" + string(rune('0'+i)),
			TargetService: "svc",
			TargetRpc:     "Rpc",
			Version:       1,
		})
		require.NoError(t, err)
	}

	resp, err := svc.ListMappings(ctx, &pb.ListMappingsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.GetMappings(), 3)
	assert.Equal(t, int32(3), resp.GetTotalCount())
}

func TestMappingService_UpdateMapping_Success(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)
	ctx := tenantCtx()

	createResp, err := svc.CreateMapping(ctx, &pb.CreateMappingRequest{
		Name:          "to-update",
		TargetService: "svc",
		TargetRpc:     "Rpc",
		Version:       1,
	})
	require.NoError(t, err)

	updateResp, err := svc.UpdateMapping(ctx, &pb.UpdateMappingRequest{
		Id:             createResp.GetMapping().GetId(),
		Name:           "updated-name",
		ExternalSchema: `{"type":"object"}`,
	})
	require.NoError(t, err)
	assert.Equal(t, "updated-name", updateResp.GetMapping().GetName())
}

func TestMappingService_UpdateMapping_NotFound(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)

	_, err := svc.UpdateMapping(tenantCtx(), &pb.UpdateMappingRequest{
		Id:   uuid.New().String(),
		Name: "x",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestMappingService_DeleteMapping_Success(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)
	ctx := tenantCtx()

	createResp, err := svc.CreateMapping(ctx, &pb.CreateMappingRequest{
		Name:          "to-delete",
		TargetService: "svc",
		TargetRpc:     "Rpc",
		Version:       1,
	})
	require.NoError(t, err)

	delResp, err := svc.DeleteMapping(ctx, &pb.DeleteMappingRequest{Id: createResp.GetMapping().GetId()})
	require.NoError(t, err)
	assert.Equal(t, createResp.GetMapping().GetId(), delResp.GetId())
}

func TestMappingService_DeleteMapping_Active_Fails(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)
	ctx := tenantCtx()

	createResp, err := svc.CreateMapping(ctx, &pb.CreateMappingRequest{
		Name:          "active-mapping",
		TargetService: "svc",
		TargetRpc:     "Rpc",
		Version:       1,
	})
	require.NoError(t, err)

	id, _ := uuid.Parse(createResp.GetMapping().GetId())
	require.NoError(t, repo.UpdateStatus(ctx, id, mapping.StatusActive))

	_, err = svc.DeleteMapping(ctx, &pb.DeleteMappingRequest{Id: createResp.GetMapping().GetId()})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestMappingService_NewMappingService_NilRepo(t *testing.T) {
	_, err := handlerpkg.NewMappingService(nil, nil, nil)
	require.Error(t, err)
}

// --- DryRunMapping tests ---

// activeMappingWithFields creates a mapping definition in ACTIVE status with the given fields.
func activeMappingWithFields(t *testing.T, repo *stubMappingRepo, name string, fields []*pb.FieldCorrespondence) string {
	t.Helper()
	ctx := tenantCtx()

	pbFields := make([]*pb.FieldCorrespondence, len(fields))
	copy(pbFields, fields)

	svc, err := handlerpkg.NewMappingService(repo, nil, nil)
	require.NoError(t, err)

	createResp, err := svc.CreateMapping(ctx, &pb.CreateMappingRequest{
		Name:          name,
		TargetService: "svc",
		TargetRpc:     "Rpc",
		Version:       1,
		Fields:        pbFields,
	})
	require.NoError(t, err)

	id, _ := uuid.Parse(createResp.GetMapping().GetId())
	require.NoError(t, repo.UpdateStatus(ctx, id, mapping.StatusActive))

	return createResp.GetMapping().GetId()
}

func TestMappingService_DryRunMapping_Inbound_Success(t *testing.T) {
	repo := newStubRepo()
	activeMappingWithFields(t, repo, "test-mapping", []*pb.FieldCorrespondence{
		{ExternalPath: "customer_name", InternalPath: "name"},
		{ExternalPath: "customer_email", InternalPath: "email"},
	})

	svc := newMappingService(t, repo)

	resp, err := svc.DryRunMapping(tenantCtx(), &pb.DryRunMappingRequest{
		MappingName: "test-mapping",
		Direction:   "inbound",
		SampleJson:  `{"customer_name":"Alice","customer_email":"alice@example.com"}`,
	})
	require.NoError(t, err)
	assert.True(t, resp.GetValidationResult().GetPassed())
	assert.NotEmpty(t, resp.GetTransformedJson())
	assert.GreaterOrEqual(t, resp.GetExecutionTimeMs(), int64(0))
	assert.Len(t, resp.GetFieldMappingTrace(), 2)

	// Verify trace content
	trace0 := resp.GetFieldMappingTrace()[0]
	assert.Equal(t, "customer_name", trace0.GetSourcePath())
	assert.Equal(t, "name", trace0.GetTargetPath())
	assert.Equal(t, "none", trace0.GetTransformType())
}

func TestMappingService_DryRunMapping_Outbound_Success(t *testing.T) {
	repo := newStubRepo()
	activeMappingWithFields(t, repo, "outbound-mapping", []*pb.FieldCorrespondence{
		{ExternalPath: "customer_name", InternalPath: "name"},
	})

	svc := newMappingService(t, repo)

	resp, err := svc.DryRunMapping(tenantCtx(), &pb.DryRunMappingRequest{
		MappingName: "outbound-mapping",
		Direction:   "outbound",
		SampleJson:  `{"name":"Bob"}`,
	})
	require.NoError(t, err)
	assert.True(t, resp.GetValidationResult().GetPassed())
	assert.NotEmpty(t, resp.GetTransformedJson())
	assert.Len(t, resp.GetFieldMappingTrace(), 1)

	trace0 := resp.GetFieldMappingTrace()[0]
	assert.Equal(t, "name", trace0.GetSourcePath())
	assert.Equal(t, "customer_name", trace0.GetTargetPath())
}

func TestMappingService_DryRunMapping_MappingNotFound(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)

	_, err := svc.DryRunMapping(tenantCtx(), &pb.DryRunMappingRequest{
		MappingName: "nonexistent",
		Direction:   "inbound",
		SampleJson:  `{}`,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestMappingService_DryRunMapping_ValidationFailure(t *testing.T) {
	repo := newStubRepo()
	ctx := tenantCtx()

	svc, err := handlerpkg.NewMappingService(repo, nil, nil)
	require.NoError(t, err)

	createResp, err := svc.CreateMapping(ctx, &pb.CreateMappingRequest{
		Name:                 "validated-mapping",
		TargetService:        "svc",
		TargetRpc:            "Rpc",
		Version:              1,
		InboundValidationCel: "has(payload.required_field)",
	})
	require.NoError(t, err)

	id, _ := uuid.Parse(createResp.GetMapping().GetId())
	require.NoError(t, repo.UpdateStatus(ctx, id, mapping.StatusActive))

	resp, err := svc.DryRunMapping(ctx, &pb.DryRunMappingRequest{
		MappingName: "validated-mapping",
		Direction:   "inbound",
		SampleJson:  `{"other_field":"value"}`,
	})
	require.NoError(t, err)
	assert.False(t, resp.GetValidationResult().GetPassed())
	assert.NotEmpty(t, resp.GetValidationResult().GetErrors())
}

func TestMappingService_DryRunMapping_WithExplicitVersion(t *testing.T) {
	repo := newStubRepo()
	activeMappingWithFields(t, repo, "versioned-mapping", []*pb.FieldCorrespondence{
		{ExternalPath: "foo", InternalPath: "bar"},
	})

	svc := newMappingService(t, repo)

	resp, err := svc.DryRunMapping(tenantCtx(), &pb.DryRunMappingRequest{
		MappingName:    "versioned-mapping",
		MappingVersion: 1,
		Direction:      "inbound",
		SampleJson:     `{"foo":"baz"}`,
	})
	require.NoError(t, err)
	assert.True(t, resp.GetValidationResult().GetPassed())
}

func TestMappingService_DryRunMapping_FieldTrace_CELTransform(t *testing.T) {
	repo := newStubRepo()
	ctx := tenantCtx()

	svc, err := handlerpkg.NewMappingService(repo, nil, nil)
	require.NoError(t, err)

	createResp, err := svc.CreateMapping(ctx, &pb.CreateMappingRequest{
		Name:          "cel-mapping",
		TargetService: "svc",
		TargetRpc:     "Rpc",
		Version:       1,
		Fields: []*pb.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "status",
				Transform: &pb.FieldTransform{
					Transform: &pb.FieldTransform_Cel{
						Cel: &pb.CelTransform{InboundCel: "value.lowerAscii()"},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	id, _ := uuid.Parse(createResp.GetMapping().GetId())
	require.NoError(t, repo.UpdateStatus(ctx, id, mapping.StatusActive))

	resp, err := svc.DryRunMapping(ctx, &pb.DryRunMappingRequest{
		MappingName: "cel-mapping",
		Direction:   "inbound",
		SampleJson:  `{"status":"ACTIVE"}`,
	})
	require.NoError(t, err)
	assert.True(t, resp.GetValidationResult().GetPassed())

	require.Len(t, resp.GetFieldMappingTrace(), 1)
	trace := resp.GetFieldMappingTrace()[0]
	assert.Equal(t, "cel", trace.GetTransformType())
	assert.Equal(t, `"ACTIVE"`, trace.GetSourceValue())
	assert.Equal(t, `"active"`, trace.GetTransformedValue())
}

func TestMappingService_DryRunMapping_IdempotencyKey_Inbound(t *testing.T) {
	repo := newStubRepo()
	ctx := tenantCtx()

	svc, err := handlerpkg.NewMappingService(repo, nil, nil)
	require.NoError(t, err)

	createResp, err := svc.CreateMapping(ctx, &pb.CreateMappingRequest{
		Name:          "idempotent-mapping",
		TargetService: "svc",
		TargetRpc:     "Rpc",
		Version:       1,
		Fields: []*pb.FieldCorrespondence{
			{ExternalPath: "ref", InternalPath: "reference"},
		},
		Idempotency: &pb.IdempotencyConfig{
			SourceSelector: "ref",
		},
	})
	require.NoError(t, err)

	id, _ := uuid.Parse(createResp.GetMapping().GetId())
	require.NoError(t, repo.UpdateStatus(ctx, id, mapping.StatusActive))

	resp, err := svc.DryRunMapping(ctx, &pb.DryRunMappingRequest{
		MappingName: "idempotent-mapping",
		Direction:   "inbound",
		SampleJson:  `{"ref":"txn-001"}`,
	})
	require.NoError(t, err)
	assert.True(t, resp.GetValidationResult().GetPassed())
	assert.Equal(t, "txn-001", resp.GetIdempotencyKey())
}

func TestMappingService_DryRunMapping_InvalidDirection(t *testing.T) {
	repo := newStubRepo()
	activeMappingWithFields(t, repo, "dir-test", nil)

	svc := newMappingService(t, repo)

	_, err := svc.DryRunMapping(tenantCtx(), &pb.DryRunMappingRequest{
		MappingName: "dir-test",
		Direction:   "sideways",
		SampleJson:  `{}`,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
