package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func newTestGRPCServiceWithPartyType(t *testing.T) (*Service, *mockPartyTypeDefinitionRepository) {
	t.Helper()
	repo := newMockRepository()
	svc := newTestService(repo)

	ptRepo := newMockPartyTypeRepo()
	ptSvc, err := NewPartyTypeDefinitionService(ptRepo)
	require.NoError(t, err)
	svc.WithPartyTypeDefinitionService(ptSvc)

	return svc, ptRepo
}

func seedPartyType(repo *mockPartyTypeDefinitionRepository, id uuid.UUID, tenantID, partyType string) *persistence.PartyTypeDefinitionEntity {
	now := time.Now()
	e := &persistence.PartyTypeDefinitionEntity{
		ID:              id,
		TenantID:        tenantID,
		PartyType:       partyType,
		AttributeSchema: validAttributeSchema,
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	repo.entities[id] = e
	return e
}

// --- RegisterPartyType ---

func TestRegisterPartyType_Success(t *testing.T) {
	svc, _ := newTestGRPCServiceWithPartyType(t)

	resp, err := svc.RegisterPartyType(testCtx(), &pb.RegisterPartyTypeRequest{
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "PERSON", resp.PartyTypeDefinition.PartyType)
	assert.Equal(t, testTenantID, resp.PartyTypeDefinition.TenantId)
}

func TestRegisterPartyType_Unimplemented(t *testing.T) {
	repo := newMockRepository()
	svc := newTestService(repo) // no party type service attached

	_, err := svc.RegisterPartyType(testCtx(), &pb.RegisterPartyTypeRequest{
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestRegisterPartyType_NoTenant(t *testing.T) {
	svc, _ := newTestGRPCServiceWithPartyType(t)

	_, err := svc.RegisterPartyType(context.Background(), &pb.RegisterPartyTypeRequest{
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
}

func TestRegisterPartyType_AlreadyExists(t *testing.T) {
	svc, _ := newTestGRPCServiceWithPartyType(t)
	ctx := testCtx()

	req := &pb.RegisterPartyTypeRequest{
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
	}
	_, err := svc.RegisterPartyType(ctx, req)
	require.NoError(t, err)

	_, err = svc.RegisterPartyType(ctx, req)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.AlreadyExists, st.Code())
}

func TestRegisterPartyType_InvalidArgument(t *testing.T) {
	svc, _ := newTestGRPCServiceWithPartyType(t)

	_, err := svc.RegisterPartyType(testCtx(), &pb.RegisterPartyTypeRequest{
		PartyType:       "PERSON",
		AttributeSchema: "", // empty schema
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRegisterPartyType_InternalError(t *testing.T) {
	svc, ptRepo := newTestGRPCServiceWithPartyType(t)
	ptRepo.createErr = errors.New("unexpected db error")

	_, err := svc.RegisterPartyType(testCtx(), &pb.RegisterPartyTypeRequest{
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// --- GetPartyType ---

func TestGetPartyType_Success(t *testing.T) {
	svc, ptRepo := newTestGRPCServiceWithPartyType(t)
	id := uuid.New()
	seedPartyType(ptRepo, id, testTenantID, "PERSON")

	resp, err := svc.GetPartyType(testCtx(), &pb.GetPartyTypeRequest{Id: id.String()})

	require.NoError(t, err)
	assert.Equal(t, id.String(), resp.PartyTypeDefinition.Id)
}

func TestGetPartyType_Unimplemented(t *testing.T) {
	repo := newMockRepository()
	svc := newTestService(repo)

	_, err := svc.GetPartyType(testCtx(), &pb.GetPartyTypeRequest{Id: uuid.New().String()})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestGetPartyType_NoTenant(t *testing.T) {
	svc, _ := newTestGRPCServiceWithPartyType(t)

	_, err := svc.GetPartyType(context.Background(), &pb.GetPartyTypeRequest{Id: uuid.New().String()})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
}

func TestGetPartyType_InvalidID(t *testing.T) {
	svc, _ := newTestGRPCServiceWithPartyType(t)

	_, err := svc.GetPartyType(testCtx(), &pb.GetPartyTypeRequest{Id: "not-a-uuid"})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGetPartyType_NotFound(t *testing.T) {
	svc, _ := newTestGRPCServiceWithPartyType(t)

	_, err := svc.GetPartyType(testCtx(), &pb.GetPartyTypeRequest{Id: uuid.New().String()})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetPartyType_InternalError(t *testing.T) {
	svc, ptRepo := newTestGRPCServiceWithPartyType(t)
	ptRepo.getErr = errors.New("unexpected db error")

	_, err := svc.GetPartyType(testCtx(), &pb.GetPartyTypeRequest{Id: uuid.New().String()})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// --- ListPartyTypes ---

func TestListPartyTypes_Success(t *testing.T) {
	svc, ptRepo := newTestGRPCServiceWithPartyType(t)
	seedPartyType(ptRepo, uuid.New(), testTenantID, "PERSON")
	seedPartyType(ptRepo, uuid.New(), testTenantID, "ORGANIZATION")

	resp, err := svc.ListPartyTypes(testCtx(), &pb.ListPartyTypesRequest{})

	require.NoError(t, err)
	assert.Len(t, resp.PartyTypeDefinitions, 2)
}

func TestListPartyTypes_WithFilter(t *testing.T) {
	svc, ptRepo := newTestGRPCServiceWithPartyType(t)
	seedPartyType(ptRepo, uuid.New(), testTenantID, "PERSON")
	seedPartyType(ptRepo, uuid.New(), testTenantID, "ORGANIZATION")

	resp, err := svc.ListPartyTypes(testCtx(), &pb.ListPartyTypesRequest{PartyType: "PERSON"})

	require.NoError(t, err)
	assert.Len(t, resp.PartyTypeDefinitions, 1)
	assert.Equal(t, "PERSON", resp.PartyTypeDefinitions[0].PartyType)
}

func TestListPartyTypes_Unimplemented(t *testing.T) {
	repo := newMockRepository()
	svc := newTestService(repo)

	_, err := svc.ListPartyTypes(testCtx(), &pb.ListPartyTypesRequest{})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestListPartyTypes_InternalError(t *testing.T) {
	svc, ptRepo := newTestGRPCServiceWithPartyType(t)
	ptRepo.listErr = errors.New("unexpected db error")

	_, err := svc.ListPartyTypes(testCtx(), &pb.ListPartyTypesRequest{})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestListPartyTypes_NoTenant(t *testing.T) {
	svc, _ := newTestGRPCServiceWithPartyType(t)

	_, err := svc.ListPartyTypes(context.Background(), &pb.ListPartyTypesRequest{})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
}

// --- UpdatePartyType ---

func TestUpdatePartyType_Success(t *testing.T) {
	svc, ptRepo := newTestGRPCServiceWithPartyType(t)
	id := uuid.New()
	seedPartyType(ptRepo, id, testTenantID, "PERSON")

	newSchema := `{"type":"object","properties":{"income":{"type":"number"}}}`
	resp, err := svc.UpdatePartyType(testCtx(), &pb.UpdatePartyTypeRequest{
		Id:              id.String(),
		Version:         1,
		AttributeSchema: newSchema,
	})

	require.NoError(t, err)
	assert.Equal(t, int32(2), resp.PartyTypeDefinition.Version)
	assert.Equal(t, newSchema, resp.PartyTypeDefinition.AttributeSchema)
}

func TestUpdatePartyType_WithFieldMask(t *testing.T) {
	svc, ptRepo := newTestGRPCServiceWithPartyType(t)
	id := uuid.New()
	seedPartyType(ptRepo, id, testTenantID, "PERSON")

	newSchema := `{"type":"object","properties":{"income":{"type":"number"}}}`
	resp, err := svc.UpdatePartyType(testCtx(), &pb.UpdatePartyTypeRequest{
		Id:              id.String(),
		Version:         1,
		AttributeSchema: newSchema,
		UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"attribute_schema"}},
	})

	require.NoError(t, err)
	assert.Equal(t, newSchema, resp.PartyTypeDefinition.AttributeSchema)
}

func TestUpdatePartyType_UnsupportedMaskPath(t *testing.T) {
	svc, ptRepo := newTestGRPCServiceWithPartyType(t)
	id := uuid.New()
	seedPartyType(ptRepo, id, testTenantID, "PERSON")

	_, err := svc.UpdatePartyType(testCtx(), &pb.UpdatePartyTypeRequest{
		Id:         id.String(),
		Version:    1,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"unknown_field"}},
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdatePartyType_Unimplemented(t *testing.T) {
	repo := newMockRepository()
	svc := newTestService(repo)

	_, err := svc.UpdatePartyType(testCtx(), &pb.UpdatePartyTypeRequest{
		Id:      uuid.New().String(),
		Version: 1,
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
}

func TestUpdatePartyType_NoTenant(t *testing.T) {
	svc, _ := newTestGRPCServiceWithPartyType(t)

	_, err := svc.UpdatePartyType(context.Background(), &pb.UpdatePartyTypeRequest{
		Id:      uuid.New().String(),
		Version: 1,
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
}

func TestUpdatePartyType_InvalidID(t *testing.T) {
	svc, _ := newTestGRPCServiceWithPartyType(t)

	_, err := svc.UpdatePartyType(testCtx(), &pb.UpdatePartyTypeRequest{
		Id:      "not-a-uuid",
		Version: 1,
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdatePartyType_NotFound(t *testing.T) {
	svc, _ := newTestGRPCServiceWithPartyType(t)

	_, err := svc.UpdatePartyType(testCtx(), &pb.UpdatePartyTypeRequest{
		Id:              uuid.New().String(),
		Version:         1,
		AttributeSchema: validAttributeSchema,
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestUpdatePartyType_VersionConflict(t *testing.T) {
	svc, ptRepo := newTestGRPCServiceWithPartyType(t)
	id := uuid.New()
	e := seedPartyType(ptRepo, id, testTenantID, "PERSON")
	e.Version = 2 // DB at version 2

	_, err := svc.UpdatePartyType(testCtx(), &pb.UpdatePartyTypeRequest{
		Id:              id.String(),
		Version:         1, // client thinks version 1
		AttributeSchema: validAttributeSchema,
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, st.Code())
}

func TestUpdatePartyType_InvalidSchema(t *testing.T) {
	svc, ptRepo := newTestGRPCServiceWithPartyType(t)
	id := uuid.New()
	seedPartyType(ptRepo, id, testTenantID, "PERSON")

	_, err := svc.UpdatePartyType(testCtx(), &pb.UpdatePartyTypeRequest{
		Id:              id.String(),
		Version:         1,
		AttributeSchema: "not valid json",
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdatePartyType_InternalError(t *testing.T) {
	svc, ptRepo := newTestGRPCServiceWithPartyType(t)
	id := uuid.New()
	seedPartyType(ptRepo, id, testTenantID, "PERSON")
	ptRepo.updateErr = errors.New("unexpected db error")

	_, err := svc.UpdatePartyType(testCtx(), &pb.UpdatePartyTypeRequest{
		Id:              id.String(),
		Version:         1,
		AttributeSchema: validAttributeSchema,
	})

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// --- partyTypeDefinitionToProto ---

func TestPartyTypeDefinitionToProto(t *testing.T) {
	now := time.Now()
	id := uuid.New()
	entity := &persistence.PartyTypeDefinitionEntity{
		ID:              id,
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
		ValidationCEL:   "true",
		EligibilityCEL:  "true",
		ErrorMessageCEL: `"error"`,
		Version:         3,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	proto := partyTypeDefinitionToProto(entity)

	assert.Equal(t, id.String(), proto.Id)
	assert.Equal(t, testTenantID, proto.TenantId)
	assert.Equal(t, "PERSON", proto.PartyType)
	assert.Equal(t, validAttributeSchema, proto.AttributeSchema)
	assert.Equal(t, "true", proto.ValidationCel)
	assert.Equal(t, "true", proto.EligibilityCel)
	assert.Equal(t, `"error"`, proto.ErrorMessageCel)
	assert.Equal(t, int32(3), proto.Version)
	assert.NotNil(t, proto.CreatedAt)
	assert.NotNil(t, proto.UpdatedAt)
}
