package applier

import (
	"context"
	"net"
	"testing"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// Compile-time interface satisfaction check.
var _ MarketInformationService = (*MarketInformationClient)(nil)

// ─── Mock gRPC server ──────────────────────────────────────────────────────

type fakeMarketInformationServer struct {
	marketinformationv1.UnimplementedMarketInformationServiceServer
	registerDataSourceErr error
	registerDataSetErr    error
	activateDataSetErr    error
	retrieveDataSetFn     func(context.Context, *marketinformationv1.RetrieveDataSetRequest) (*marketinformationv1.RetrieveDataSetResponse, error)
}

func (f *fakeMarketInformationServer) RegisterDataSource(_ context.Context, req *marketinformationv1.RegisterDataSourceRequest) (*marketinformationv1.RegisterDataSourceResponse, error) {
	if f.registerDataSourceErr != nil {
		return nil, f.registerDataSourceErr
	}
	return &marketinformationv1.RegisterDataSourceResponse{
		Source: &marketinformationv1.DataSource{
			Id:   "src-uuid-1",
			Code: req.Code,
		},
	}, nil
}

func (f *fakeMarketInformationServer) RegisterDataSet(_ context.Context, req *marketinformationv1.RegisterDataSetRequest) (*marketinformationv1.RegisterDataSetResponse, error) {
	if f.registerDataSetErr != nil {
		return nil, f.registerDataSetErr
	}
	return &marketinformationv1.RegisterDataSetResponse{
		Dataset: &marketinformationv1.DataSetDefinition{
			Id:      "ds-uuid-1",
			Code:    req.Code,
			Version: 1,
			Status:  marketinformationv1.DataSetStatus_DATA_SET_STATUS_DRAFT,
		},
	}, nil
}

func (f *fakeMarketInformationServer) ActivateDataSet(_ context.Context, req *marketinformationv1.ActivateDataSetRequest) (*marketinformationv1.ActivateDataSetResponse, error) {
	if f.activateDataSetErr != nil {
		return nil, f.activateDataSetErr
	}
	return &marketinformationv1.ActivateDataSetResponse{
		Dataset: &marketinformationv1.DataSetDefinition{
			Id:      "ds-uuid-1",
			Code:    req.Code,
			Version: req.Version,
			Status:  marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
		},
	}, nil
}

func (f *fakeMarketInformationServer) RetrieveDataSet(ctx context.Context, req *marketinformationv1.RetrieveDataSetRequest) (*marketinformationv1.RetrieveDataSetResponse, error) {
	if f.retrieveDataSetFn != nil {
		return f.retrieveDataSetFn(ctx, req)
	}
	return &marketinformationv1.RetrieveDataSetResponse{
		Dataset: &marketinformationv1.DataSetDefinition{
			Id:      "ds-existing-uuid",
			Code:    req.Code,
			Version: 1,
			Status:  marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
		},
	}, nil
}

// ─── Test setup ────────────────────────────────────────────────────────────

func newMarketInformationTestServer(t *testing.T, srv *fakeMarketInformationServer) *grpc.ClientConn {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	grpcSrv := grpc.NewServer()
	marketinformationv1.RegisterMarketInformationServiceServer(grpcSrv, srv)

	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.GracefulStop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// ─── parseDataCategory tests ───────────────────────────────────────────────

func TestParseDataCategory_Empty(t *testing.T) {
	cat, err := parseDataCategory("")
	require.NoError(t, err)
	assert.Equal(t, marketinformationv1.DataCategory_DATA_CATEGORY_UNSPECIFIED, cat)
}

func TestParseDataCategory_Prefixed(t *testing.T) {
	cat, err := parseDataCategory("DATA_CATEGORY_FX_RATE")
	require.NoError(t, err)
	assert.Equal(t, marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE, cat)
}

func TestParseDataCategory_Stripped(t *testing.T) {
	cat, err := parseDataCategory("FX_RATE")
	require.NoError(t, err)
	assert.Equal(t, marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE, cat)
}

func TestParseDataCategory_Unknown(t *testing.T) {
	_, err := parseDataCategory("NONSENSE")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownDataCategory)
	assert.Contains(t, err.Error(), "NONSENSE")
}

func TestNewMarketInformationClient(t *testing.T) {
	c := NewMarketInformationClient(nil)
	assert.NotNil(t, c)
}

// ─── RegisterDataSource tests ──────────────────────────────────────────────

func TestMarketInformationClient_RegisterDataSource(t *testing.T) {
	conn := newMarketInformationTestServer(t, &fakeMarketInformationServer{})
	client := NewMarketInformationClient(conn)

	result, err := client.RegisterDataSource(testStarlarkCtx(), map[string]any{
		"code":        "BLOOMBERG",
		"name":        "Bloomberg Data",
		"description": "FX and commodity data",
		"trust_level": int32(90),
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "src-uuid-1", m["source_id"])
	assert.Equal(t, "BLOOMBERG", m["code"])
	assert.Equal(t, "REGISTERED", m["status"])
}

func TestMarketInformationClient_RegisterDataSource_MinimalParams(t *testing.T) {
	conn := newMarketInformationTestServer(t, &fakeMarketInformationServer{})
	client := NewMarketInformationClient(conn)

	result, err := client.RegisterDataSource(testStarlarkCtx(), map[string]any{
		"code": "MINIMAL",
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "src-uuid-1", m["source_id"])
	assert.Equal(t, "MINIMAL", m["code"])
}

func TestMarketInformationClient_RegisterDataSource_AlreadyExists_TreatedAsSuccess(t *testing.T) {
	srv := &fakeMarketInformationServer{
		registerDataSourceErr: status.Error(codes.AlreadyExists, "data source already exists"),
	}
	conn := newMarketInformationTestServer(t, srv)
	client := NewMarketInformationClient(conn)

	result, err := client.RegisterDataSource(testStarlarkCtx(), map[string]any{
		"code": "DUPLICATE",
	})
	require.NoError(t, err, "AlreadyExists should be treated as idempotent success")
	m := result.(map[string]any)
	assert.Equal(t, "DUPLICATE", m["code"])
	assert.Equal(t, "REGISTERED", m["status"])
}

func TestMarketInformationClient_RegisterDataSource_OtherError_Propagated(t *testing.T) {
	srv := &fakeMarketInformationServer{
		registerDataSourceErr: status.Error(codes.Internal, "database unavailable"),
	}
	conn := newMarketInformationTestServer(t, srv)
	client := NewMarketInformationClient(conn)

	_, err := client.RegisterDataSource(testStarlarkCtx(), map[string]any{
		"code": "WILL_FAIL",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "register data source")
}

// ─── RegisterDataSet tests ─────────────────────────────────────────────────

func TestMarketInformationClient_RegisterDataSet(t *testing.T) {
	conn := newMarketInformationTestServer(t, &fakeMarketInformationServer{})
	client := NewMarketInformationClient(conn)

	result, err := client.RegisterDataSet(testStarlarkCtx(), map[string]any{
		"code":                      "USD_EUR_FX",
		"unit":                      "USD/EUR",
		"display_name":              "USD/EUR Spot Rate",
		"description":               "Spot FX rate",
		"category":                  "FX_RATE",
		"resolution_key_expression": "observed_at",
		"validation_expression":     "value > 0",
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "ds-uuid-1", m["dataset_id"])
	assert.Equal(t, "USD_EUR_FX", m["code"])
	assert.Equal(t, int32(1), m["version"])
	assert.Contains(t, m["status"].(string), "DRAFT")
}

func TestMarketInformationClient_RegisterDataSet_MinimalParams(t *testing.T) {
	conn := newMarketInformationTestServer(t, &fakeMarketInformationServer{})
	client := NewMarketInformationClient(conn)

	result, err := client.RegisterDataSet(testStarlarkCtx(), map[string]any{
		"code": "MINIMAL_DS",
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "ds-uuid-1", m["dataset_id"])
	assert.Equal(t, "MINIMAL_DS", m["code"])
}

func TestMarketInformationClient_RegisterDataSet_WithEffectiveFrom(t *testing.T) {
	conn := newMarketInformationTestServer(t, &fakeMarketInformationServer{})
	client := NewMarketInformationClient(conn)

	result, err := client.RegisterDataSet(testStarlarkCtx(), map[string]any{
		"code":           "TIMED_DS",
		"effective_from": "2025-01-01T00:00:00Z",
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "TIMED_DS", m["code"])
}

func TestMarketInformationClient_RegisterDataSet_InvalidEffectiveFrom(t *testing.T) {
	conn := newMarketInformationTestServer(t, &fakeMarketInformationServer{})
	client := NewMarketInformationClient(conn)

	_, err := client.RegisterDataSet(testStarlarkCtx(), map[string]any{
		"code":           "BAD_DATE",
		"effective_from": "not-a-date",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid effective_from")
}

func TestMarketInformationClient_RegisterDataSet_InvalidCategory(t *testing.T) {
	conn := newMarketInformationTestServer(t, &fakeMarketInformationServer{})
	client := NewMarketInformationClient(conn)

	_, err := client.RegisterDataSet(testStarlarkCtx(), map[string]any{
		"code":     "BAD_CAT",
		"category": "NONEXISTENT_CATEGORY",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownDataCategory)
}

func TestMarketInformationClient_RegisterDataSet_GRPCError(t *testing.T) {
	srv := &fakeMarketInformationServer{
		registerDataSetErr: status.Error(codes.Internal, "internal error"),
	}
	conn := newMarketInformationTestServer(t, srv)
	client := NewMarketInformationClient(conn)

	_, err := client.RegisterDataSet(testStarlarkCtx(), map[string]any{
		"code": "WILL_FAIL",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "register data set")
}

// ─── ActivateDataSet tests ─────────────────────────────────────────────────

func TestMarketInformationClient_ActivateDataSet(t *testing.T) {
	conn := newMarketInformationTestServer(t, &fakeMarketInformationServer{})
	client := NewMarketInformationClient(conn)

	result, err := client.ActivateDataSet(testStarlarkCtx(), map[string]any{
		"code":    "USD_EUR_FX",
		"version": int32(1),
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "ds-uuid-1", m["dataset_id"])
	assert.Equal(t, "USD_EUR_FX", m["code"])
	assert.Equal(t, int32(1), m["version"])
	assert.Contains(t, m["status"].(string), "ACTIVE")
}

func TestMarketInformationClient_ActivateDataSet_MinimalParams(t *testing.T) {
	conn := newMarketInformationTestServer(t, &fakeMarketInformationServer{})
	client := NewMarketInformationClient(conn)

	result, err := client.ActivateDataSet(testStarlarkCtx(), map[string]any{
		"code": "MINIMAL",
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "MINIMAL", m["code"])
}

func TestMarketInformationClient_ActivateDataSet_GRPCError(t *testing.T) {
	srv := &fakeMarketInformationServer{
		activateDataSetErr: status.Error(codes.NotFound, "data set not found"),
	}
	conn := newMarketInformationTestServer(t, srv)
	client := NewMarketInformationClient(conn)

	_, err := client.ActivateDataSet(testStarlarkCtx(), map[string]any{
		"code": "MISSING",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "activate data set")
}

// ─── Idempotency tests ──────────────────────────────────────────────────────

func TestMarketInformationClient_RegisterDataSet_AlreadyExists_TreatedAsSuccess(t *testing.T) {
	srv := &fakeMarketInformationServer{
		registerDataSetErr: status.Error(codes.AlreadyExists, "dataset already exists"),
	}
	conn := newMarketInformationTestServer(t, srv)
	client := NewMarketInformationClient(conn)

	result, err := client.RegisterDataSet(testStarlarkCtx(), map[string]any{
		"code": "EXISTING_DS",
	})
	require.NoError(t, err, "AlreadyExists should be treated as idempotent success")
	m := result.(map[string]any)
	assert.Equal(t, "ds-existing-uuid", m["dataset_id"])
	assert.Equal(t, "EXISTING_DS", m["code"])
	assert.Equal(t, int32(1), m["version"])
}

func TestMarketInformationClient_RegisterDataSet_AlreadyExists_LookupFails(t *testing.T) {
	srv := &fakeMarketInformationServer{
		registerDataSetErr: status.Error(codes.AlreadyExists, "dataset already exists"),
		retrieveDataSetFn: func(_ context.Context, _ *marketinformationv1.RetrieveDataSetRequest) (*marketinformationv1.RetrieveDataSetResponse, error) {
			return nil, status.Error(codes.NotFound, "dataset not found")
		},
	}
	conn := newMarketInformationTestServer(t, srv)
	client := NewMarketInformationClient(conn)

	_, err := client.RegisterDataSet(testStarlarkCtx(), map[string]any{
		"code": "GHOST_DS",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lookup failed")
}

func TestMarketInformationClient_ActivateDataSet_AlreadyActive_TreatedAsSuccess(t *testing.T) {
	srv := &fakeMarketInformationServer{
		activateDataSetErr: status.Error(codes.FailedPrecondition, "invalid status transition: ACTIVE to ACTIVE"),
		// RetrieveDataSet returns ACTIVE status, confirming idempotent completion
	}
	conn := newMarketInformationTestServer(t, srv)
	client := NewMarketInformationClient(conn)

	result, err := client.ActivateDataSet(testStarlarkCtx(), map[string]any{
		"code":    "ALREADY_ACTIVE",
		"version": int32(1),
	})
	require.NoError(t, err, "FailedPrecondition for already-active should be treated as idempotent success")
	m := result.(map[string]any)
	assert.Equal(t, "ds-existing-uuid", m["dataset_id"])
	assert.Equal(t, "ALREADY_ACTIVE", m["code"])
}

func TestMarketInformationClient_ActivateDataSet_NotActive_ErrorPropagated(t *testing.T) {
	srv := &fakeMarketInformationServer{
		activateDataSetErr: status.Error(codes.FailedPrecondition, "invalid status transition: DEPRECATED to ACTIVE"),
		retrieveDataSetFn: func(_ context.Context, req *marketinformationv1.RetrieveDataSetRequest) (*marketinformationv1.RetrieveDataSetResponse, error) {
			return &marketinformationv1.RetrieveDataSetResponse{
				Dataset: &marketinformationv1.DataSetDefinition{
					Id:      "ds-uuid-1",
					Code:    req.Code,
					Version: 1,
					Status:  marketinformationv1.DataSetStatus_DATA_SET_STATUS_DEPRECATED,
				},
			}, nil
		},
	}
	conn := newMarketInformationTestServer(t, srv)
	client := NewMarketInformationClient(conn)

	_, err := client.ActivateDataSet(testStarlarkCtx(), map[string]any{
		"code":    "DEPRECATED_DS",
		"version": int32(1),
	})
	require.Error(t, err, "FailedPrecondition for non-ACTIVE should propagate")
	assert.Contains(t, err.Error(), "activate data set")
}
