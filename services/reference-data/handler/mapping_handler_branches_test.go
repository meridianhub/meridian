package handler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	handlerpkg "github.com/meridianhub/meridian/services/reference-data/handler"
	"github.com/meridianhub/meridian/services/reference-data/mapping"
	sharedcel "github.com/meridianhub/meridian/shared/pkg/cel"
)

// newValidatingService builds a MappingService with a real validator so the
// validator-error branches of CreateMapping/UpdateMapping are exercised.
func newValidatingService(t *testing.T, repo mapping.Repository) *handlerpkg.MappingService {
	t.Helper()
	compiler, err := sharedcel.NewCompiler()
	require.NoError(t, err)
	validator, err := mapping.NewValidator(compiler)
	require.NoError(t, err)
	svc, err := handlerpkg.NewMappingService(repo, validator, nil)
	require.NoError(t, err)
	return svc
}

// --- CreateMapping validator branch ---

func TestMappingService_CreateMapping_ValidationError(t *testing.T) {
	repo := newStubRepo()
	svc := newValidatingService(t, repo)

	// is_batch=true with no batch_target_path fails validation.
	_, err := svc.CreateMapping(tenantCtx(), &pb.CreateMappingRequest{
		Name:          "bad-batch",
		TargetService: "svc",
		TargetRpc:     "Rpc",
		Version:       1,
		IsBatch:       true,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// --- UpdateMapping branches ---

func TestMappingService_UpdateMapping_ValidationError(t *testing.T) {
	repo := newStubRepo()
	svc := newValidatingService(t, repo)
	ctx := tenantCtx()

	createResp, err := svc.CreateMapping(ctx, &pb.CreateMappingRequest{
		Name:          "to-update",
		TargetService: "svc",
		TargetRpc:     "Rpc",
		Version:       1,
	})
	require.NoError(t, err)

	// Update introduces an invalid external JSON schema, tripping the validator.
	_, err = svc.UpdateMapping(ctx, &pb.UpdateMappingRequest{
		Id:             createResp.GetMapping().GetId(),
		ExternalSchema: `{not valid json`,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestMappingService_UpdateMapping_InvalidUUID(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)

	_, err := svc.UpdateMapping(tenantCtx(), &pb.UpdateMappingRequest{Id: "not-a-uuid"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestMappingService_UpdateMapping_RepoError(t *testing.T) {
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

	repo.failOn = "update"

	_, err = svc.UpdateMapping(ctx, &pb.UpdateMappingRequest{
		Id:   createResp.GetMapping().GetId(),
		Name: "x",
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestMappingService_UpdateMapping_FieldMask(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)
	ctx := tenantCtx()

	createResp, err := svc.CreateMapping(ctx, &pb.CreateMappingRequest{
		Name:          "mask-base",
		TargetService: "svc",
		TargetRpc:     "Rpc",
		Version:       1,
	})
	require.NoError(t, err)

	// Exercise every FieldMask path branch in applyUpdateMask.
	updateResp, err := svc.UpdateMapping(ctx, &pb.UpdateMappingRequest{
		Id:                    createResp.GetMapping().GetId(),
		Name:                  "renamed",
		ExternalSchema:        `{"type":"object"}`,
		Fields:                []*pb.FieldCorrespondence{{ExternalPath: "a", InternalPath: "b"}},
		InboundComputedFields: []*pb.ComputedField{{TargetPath: "c", CelExpression: "1"}},
		OutboundComputedFields: []*pb.ComputedField{
			{TargetPath: "d", CelExpression: "2"},
		},
		InboundValidationCel:  "has(payload.x)",
		OutboundValidationCel: "has(payload.y)",
		IsBatch:               true,
		BatchTargetPath:       "items",
		Idempotency:           &pb.IdempotencyConfig{SourceSelector: "a"},
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{
				"name", "external_schema", "fields",
				"inbound_computed_fields", "outbound_computed_fields",
				"inbound_validation_cel", "outbound_validation_cel",
				"is_batch", "batch_target_path", "idempotency",
				"unknown_path_ignored",
			},
		},
	})
	require.NoError(t, err)
	// Update stub only persists name/external_schema/fields; assert the mask
	// applied name (others are validated by applyUpdateMask coverage itself).
	assert.Equal(t, "renamed", updateResp.GetMapping().GetName())
}

func TestMappingService_DeleteMapping_InvalidUUID(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)

	_, err := svc.DeleteMapping(tenantCtx(), &pb.DeleteMappingRequest{Id: "not-a-uuid"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// --- ListMappings branches ---

func TestMappingService_ListMappings_PageSizeClamp(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)

	// page_size above MaxPageSize is clamped; request must still succeed.
	resp, err := svc.ListMappings(tenantCtx(), &pb.ListMappingsRequest{PageSize: 5000})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

// listRepo lets us drive ListByTenant error and full-page (next token) branches.
type listRepo struct {
	*stubMappingRepo
	listErr  error
	listDefs []*mapping.Definition
}

func (r *listRepo) ListByTenant(_ context.Context, _ mapping.Status, _ string, _ int, _ string) ([]*mapping.Definition, int, error) {
	if r.listErr != nil {
		return nil, 0, r.listErr
	}
	return r.listDefs, len(r.listDefs), nil
}

func TestMappingService_ListMappings_RepoError(t *testing.T) {
	repo := &listRepo{stubMappingRepo: newStubRepo(), listErr: errors.New("db down")}
	svc := newMappingService(t, repo)

	_, err := svc.ListMappings(tenantCtx(), &pb.ListMappingsRequest{})
	require.Error(t, err)
	// Unmapped error falls through to Internal.
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestMappingService_ListMappings_NextPageToken(t *testing.T) {
	lastID := uuid.New()
	defs := make([]*mapping.Definition, 0, handlerpkg.DefaultPageSize)
	for i := 0; i < handlerpkg.DefaultPageSize; i++ {
		id := uuid.New()
		if i == handlerpkg.DefaultPageSize-1 {
			id = lastID
		}
		defs = append(defs, &mapping.Definition{ID: id, CreatedAt: time.Now()})
	}
	repo := &listRepo{stubMappingRepo: newStubRepo(), listDefs: defs}
	svc := newMappingService(t, repo)

	// A full page (len == pageSize) sets NextPageToken to the last def's ID.
	resp, err := svc.ListMappings(tenantCtx(), &pb.ListMappingsRequest{})
	require.NoError(t, err)
	assert.Equal(t, lastID.String(), resp.GetNextPageToken())
}

// --- DryRunMapping input-validation branches ---

func TestMappingService_DryRunMapping_EmptyName(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)

	_, err := svc.DryRunMapping(tenantCtx(), &pb.DryRunMappingRequest{
		MappingName: "",
		Direction:   "inbound",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestMappingService_DryRunMapping_NegativeVersion(t *testing.T) {
	repo := newStubRepo()
	svc := newMappingService(t, repo)

	_, err := svc.DryRunMapping(tenantCtx(), &pb.DryRunMappingRequest{
		MappingName:    "some-name",
		MappingVersion: -1,
		Direction:      "inbound",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// --- mapDomainError fallback ---

// failGetRepo returns an arbitrary (unmapped) error from GetByID so the
// Internal fallback path of mapDomainError is exercised.
type failGetRepo struct {
	*stubMappingRepo
}

func (r *failGetRepo) GetByID(context.Context, uuid.UUID) (*mapping.Definition, error) {
	return nil, errors.New("unexpected boom")
}

func TestMappingService_MapDomainError_InternalFallback(t *testing.T) {
	repo := &failGetRepo{stubMappingRepo: newStubRepo()}
	svc := newMappingService(t, repo)

	_, err := svc.GetMapping(tenantCtx(), &pb.GetMappingRequest{Id: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}
