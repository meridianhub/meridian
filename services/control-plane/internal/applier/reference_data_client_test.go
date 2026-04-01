package applier

import (
	"context"
	"net"
	"testing"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// Compile-time interface satisfaction check.
var _ ReferenceDataService = (*ReferenceDataClient)(nil)

// ─── Mock gRPC servers ─────────────────────────────────────────────────────

type fakeReferenceDataServer struct {
	referencedatav1.UnimplementedReferenceDataServiceServer
	activateInstrumentFn func(context.Context, *referencedatav1.ActivateInstrumentRequest) (*referencedatav1.ActivateInstrumentResponse, error)
	retrieveInstrumentFn func(context.Context, *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error)
}

func (f *fakeReferenceDataServer) RegisterInstrument(_ context.Context, req *referencedatav1.RegisterInstrumentRequest) (*referencedatav1.RegisterInstrumentResponse, error) {
	return &referencedatav1.RegisterInstrumentResponse{
		Instrument: &referencedatav1.InstrumentDefinition{
			Id:      "inst-uuid-1",
			Code:    req.Code,
			Version: 1,
			Status:  referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT,
		},
	}, nil
}

func (f *fakeReferenceDataServer) ActivateInstrument(ctx context.Context, req *referencedatav1.ActivateInstrumentRequest) (*referencedatav1.ActivateInstrumentResponse, error) {
	if f.activateInstrumentFn != nil {
		return f.activateInstrumentFn(ctx, req)
	}
	return &referencedatav1.ActivateInstrumentResponse{
		Instrument: &referencedatav1.InstrumentDefinition{
			Id:      "inst-uuid-1",
			Code:    req.Code,
			Version: req.Version,
			Status:  referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		},
	}, nil
}

func (f *fakeReferenceDataServer) RetrieveInstrument(ctx context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
	if f.retrieveInstrumentFn != nil {
		return f.retrieveInstrumentFn(ctx, req)
	}
	return &referencedatav1.RetrieveInstrumentResponse{
		Instrument: &referencedatav1.InstrumentDefinition{
			Id:      "inst-uuid-1",
			Code:    req.Code,
			Version: req.Version,
			Status:  referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		},
	}, nil
}

func (f *fakeReferenceDataServer) DeprecateInstrument(_ context.Context, req *referencedatav1.DeprecateInstrumentRequest) (*referencedatav1.DeprecateInstrumentResponse, error) {
	return &referencedatav1.DeprecateInstrumentResponse{
		Instrument: &referencedatav1.InstrumentDefinition{
			Id:      "inst-uuid-1",
			Code:    req.Code,
			Version: req.Version,
			Status:  referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED,
		},
	}, nil
}

type fakeAccountTypeServer struct {
	referencedatav1.UnimplementedAccountTypeRegistryServiceServer
	getActiveDefinitionFn func(context.Context, *referencedatav1.GetActiveDefinitionRequest) (*referencedatav1.GetActiveDefinitionResponse, error)
	createDraftFn         func(context.Context, *referencedatav1.CreateDraftRequest) (*referencedatav1.CreateDraftResponse, error)
}

func (f *fakeAccountTypeServer) GetActiveDefinition(ctx context.Context, req *referencedatav1.GetActiveDefinitionRequest) (*referencedatav1.GetActiveDefinitionResponse, error) {
	if f.getActiveDefinitionFn != nil {
		return f.getActiveDefinitionFn(ctx, req)
	}
	return nil, status.Error(codes.NotFound, "no active definition found")
}

func (f *fakeAccountTypeServer) CreateDraft(ctx context.Context, req *referencedatav1.CreateDraftRequest) (*referencedatav1.CreateDraftResponse, error) {
	if f.createDraftFn != nil {
		return f.createDraftFn(ctx, req)
	}
	return &referencedatav1.CreateDraftResponse{
		Definition: &referencedatav1.AccountTypeDefinition{
			Id:      "at-uuid-1",
			Code:    req.Code,
			Version: 1,
			Status:  referencedatav1.AccountTypeStatus_ACCOUNT_TYPE_STATUS_DRAFT,
		},
	}, nil
}

func (f *fakeAccountTypeServer) ActivateAccountType(_ context.Context, req *referencedatav1.ActivateAccountTypeRequest) (*referencedatav1.ActivateAccountTypeResponse, error) {
	return &referencedatav1.ActivateAccountTypeResponse{
		Definition: &referencedatav1.AccountTypeDefinition{
			Id:      req.Id,
			Code:    "TEST_ACCOUNT",
			Version: 1,
			Status:  referencedatav1.AccountTypeStatus_ACCOUNT_TYPE_STATUS_ACTIVE,
		},
	}, nil
}

func (f *fakeAccountTypeServer) DeprecateAccountType(_ context.Context, req *referencedatav1.DeprecateAccountTypeRequest) (*referencedatav1.DeprecateAccountTypeResponse, error) {
	return &referencedatav1.DeprecateAccountTypeResponse{
		Definition: &referencedatav1.AccountTypeDefinition{
			Id:      req.Id,
			Code:    "DEPRECATED",
			Version: 1,
			Status:  referencedatav1.AccountTypeStatus_ACCOUNT_TYPE_STATUS_DEPRECATED,
		},
	}, nil
}

type fakeSagaRegistryServer struct {
	sagav1.UnimplementedSagaRegistryServiceServer
	createDraftFn func(context.Context, *sagav1.CreateSagaDraftRequest) (*sagav1.CreateSagaDraftResponse, error)
	getActiveFn   func(context.Context, *sagav1.GetActiveSagaRequest) (*sagav1.GetActiveSagaResponse, error)
}

func (f *fakeSagaRegistryServer) CreateSagaDraft(ctx context.Context, req *sagav1.CreateSagaDraftRequest) (*sagav1.CreateSagaDraftResponse, error) {
	if f.createDraftFn != nil {
		return f.createDraftFn(ctx, req)
	}
	return &sagav1.CreateSagaDraftResponse{
		Saga: &sagav1.SagaDefinition{
			Id:     "saga-uuid-1",
			Name:   req.Name,
			Status: sagav1.SagaStatus_SAGA_STATUS_DRAFT,
		},
	}, nil
}

func (f *fakeSagaRegistryServer) ActivateSaga(_ context.Context, req *sagav1.ActivateSagaRequest) (*sagav1.ActivateSagaResponse, error) {
	return &sagav1.ActivateSagaResponse{
		Saga: &sagav1.SagaDefinition{
			Id:     req.Id,
			Name:   "test-saga",
			Status: sagav1.SagaStatus_SAGA_STATUS_ACTIVE,
		},
	}, nil
}

func (f *fakeSagaRegistryServer) GetActiveSaga(ctx context.Context, req *sagav1.GetActiveSagaRequest) (*sagav1.GetActiveSagaResponse, error) {
	if f.getActiveFn != nil {
		return f.getActiveFn(ctx, req)
	}
	// Default: no active saga found (proactive check falls through to create).
	return nil, status.Error(codes.NotFound, "no active saga found")
}

// ─── Test setup ────────────────────────────────────────────────────────────

func newRefDataTestServer(t *testing.T) *grpc.ClientConn {
	return newRefDataTestServerWith(t, &fakeSagaRegistryServer{})
}

func newRefDataTestServerWithRefData(t *testing.T, refDataSrv *fakeReferenceDataServer) *grpc.ClientConn {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	referencedatav1.RegisterReferenceDataServiceServer(srv, refDataSrv)
	referencedatav1.RegisterAccountTypeRegistryServiceServer(srv, &fakeAccountTypeServer{})
	sagav1.RegisterSagaRegistryServiceServer(srv, &fakeSagaRegistryServer{})

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

func newRefDataTestServerWith(t *testing.T, sagaSrv *fakeSagaRegistryServer) *grpc.ClientConn {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	referencedatav1.RegisterReferenceDataServiceServer(srv, &fakeReferenceDataServer{})
	referencedatav1.RegisterAccountTypeRegistryServiceServer(srv, &fakeAccountTypeServer{})
	sagav1.RegisterSagaRegistryServiceServer(srv, sagaSrv)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

func newRefDataTestServerWithAccountType(t *testing.T, atSrv *fakeAccountTypeServer) *grpc.ClientConn {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	referencedatav1.RegisterReferenceDataServiceServer(srv, &fakeReferenceDataServer{})
	referencedatav1.RegisterAccountTypeRegistryServiceServer(srv, atSrv)
	sagav1.RegisterSagaRegistryServiceServer(srv, &fakeSagaRegistryServer{})

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

func testStarlarkCtx() *saga.StarlarkContext {
	return &saga.StarlarkContext{Context: context.Background()}
}

// ─── Tests ─────────────────────────────────────────────────────────────────

func TestReferenceDataClient_RegisterInstrument(t *testing.T) {
	conn := newRefDataTestServer(t)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.RegisterInstrument(testStarlarkCtx(), map[string]any{
		"instrument_code": "GBP",
		"display_name":    "British Pound",
		"dimension":       "CURRENCY",
		"decimal_places":  2,
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "GBP", m["instrument_code"])
	assert.Equal(t, int32(1), m["version"])
}

func TestReferenceDataClient_ActivateInstrument(t *testing.T) {
	conn := newRefDataTestServer(t)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.ActivateInstrument(testStarlarkCtx(), map[string]any{
		"instrument_code": "GBP",
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "GBP", m["instrument_code"])
	assert.Contains(t, m["status"], "ACTIVE")
}

func TestReferenceDataClient_DeleteInstrument(t *testing.T) {
	conn := newRefDataTestServer(t)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.DeleteInstrument(testStarlarkCtx(), map[string]any{
		"instrument_code": "OLD",
		"version":         int32(1),
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "OLD", m["instrument_code"])
	assert.Contains(t, m["status"], "DEPRECATED")
}

func TestReferenceDataClient_RegisterAccountType(t *testing.T) {
	conn := newRefDataTestServer(t)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.RegisterAccountType(testStarlarkCtx(), map[string]any{
		"code":            "ENERGY_TRADING",
		"display_name":    "Energy Trading Account",
		"behavior_class":  "CLEARING",
		"normal_balance":  "DEBIT",
		"instrument_code": "GBP",
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "TEST_ACCOUNT", m["code"])
	assert.Contains(t, m["status"], "ACTIVE")
}

func TestReferenceDataClient_DeleteAccountType(t *testing.T) {
	conn := newRefDataTestServer(t)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.DeleteAccountType(testStarlarkCtx(), map[string]any{
		"id": "at-uuid-1",
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Contains(t, m["status"], "DEPRECATED")
}

func TestReferenceDataClient_RegisterValuationRule(t *testing.T) {
	conn := newRefDataTestServer(t)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.RegisterValuationRule(testStarlarkCtx(), map[string]any{
		"from_instrument": "KWH",
		"to_instrument":   "GBP",
		"rule_type":       "FIXED_RATE",
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "KWH", m["from_instrument"])
	assert.Equal(t, "GBP", m["to_instrument"])
	assert.Equal(t, "REGISTERED", m["status"])
}

func TestReferenceDataClient_RegisterSagaDefinition(t *testing.T) {
	conn := newRefDataTestServer(t)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.RegisterSagaDefinition(testStarlarkCtx(), map[string]any{
		"saga_name":    "test-saga",
		"display_name": "Test Saga",
		"script":       "def execute(): pass",
	})
	require.NoError(t, err)

	m := result.(map[string]any)
	assert.Equal(t, "test-saga", m["saga_name"])
	assert.Equal(t, "saga-uuid-1", m["saga_id"])
	assert.Contains(t, m["status"], "ACTIVE")
}

func TestParseDimension(t *testing.T) {
	tests := []struct {
		input    string
		expected referencedatav1.Dimension
	}{
		{"DIMENSION_CURRENCY", referencedatav1.Dimension_DIMENSION_CURRENCY},
		{"CURRENCY", referencedatav1.Dimension_DIMENSION_CURRENCY},
		{"DIMENSION_ENERGY", referencedatav1.Dimension_DIMENSION_ENERGY},
		{"ENERGY", referencedatav1.Dimension_DIMENSION_ENERGY},
		{"unknown", referencedatav1.Dimension_DIMENSION_UNSPECIFIED},
		{"", referencedatav1.Dimension_DIMENSION_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseDimension(tt.input))
		})
	}
}

func TestParseBehaviorClass(t *testing.T) {
	tests := []struct {
		input    string
		expected referencedatav1.BehaviorClass
	}{
		{"BEHAVIOR_CLASS_CLEARING", referencedatav1.BehaviorClass_BEHAVIOR_CLASS_CLEARING},
		{"CLEARING", referencedatav1.BehaviorClass_BEHAVIOR_CLASS_CLEARING},
		{"unknown", referencedatav1.BehaviorClass_BEHAVIOR_CLASS_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseBehaviorClass(tt.input))
		})
	}
}

func TestParseNormalBalance(t *testing.T) {
	tests := []struct {
		input    string
		expected referencedatav1.NormalBalance
	}{
		{"NORMAL_BALANCE_DEBIT", referencedatav1.NormalBalance_NORMAL_BALANCE_DEBIT},
		{"DEBIT", referencedatav1.NormalBalance_NORMAL_BALANCE_DEBIT},
		{"NORMAL_BALANCE_CREDIT", referencedatav1.NormalBalance_NORMAL_BALANCE_CREDIT},
		{"CREDIT", referencedatav1.NormalBalance_NORMAL_BALANCE_CREDIT},
		{"unknown", referencedatav1.NormalBalance_NORMAL_BALANCE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseNormalBalance(tt.input))
		})
	}
}

// ─── Idempotency tests ──────────────────────────────────────────────────────

func TestReferenceDataClient_RegisterSagaDefinition_AlreadyExists_TreatedAsSuccess(t *testing.T) {
	// Proactive check returns NotFound (first call), but CreateSagaDraft returns AlreadyExists.
	// The reactive fallback calls GetActiveSaga again (second call) and finds the saga.
	callCount := 0
	sagaSrv := &fakeSagaRegistryServer{
		getActiveFn: func(_ context.Context, req *sagav1.GetActiveSagaRequest) (*sagav1.GetActiveSagaResponse, error) {
			callCount++
			if callCount == 1 {
				return nil, status.Error(codes.NotFound, "no active saga found")
			}
			return &sagav1.GetActiveSagaResponse{
				Saga: &sagav1.SagaDefinition{
					Id:     "existing-saga-uuid",
					Name:   req.Name,
					Status: sagav1.SagaStatus_SAGA_STATUS_ACTIVE,
				},
			}, nil
		},
		createDraftFn: func(_ context.Context, _ *sagav1.CreateSagaDraftRequest) (*sagav1.CreateSagaDraftResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "saga already exists: test-saga")
		},
	}
	conn := newRefDataTestServerWith(t, sagaSrv)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.RegisterSagaDefinition(testStarlarkCtx(), map[string]any{
		"saga_name":    "test-saga",
		"display_name": "Test Saga",
		"script":       "def execute(): pass",
	})
	require.NoError(t, err, "AlreadyExists should be treated as idempotent success")
	m := result.(map[string]any)
	assert.Equal(t, "existing-saga-uuid", m["saga_id"])
	assert.Equal(t, "test-saga", m["saga_name"])
	assert.Contains(t, m["status"].(string), "ACTIVE")
}

func TestReferenceDataClient_RegisterSagaDefinition_AlreadyExists_LookupFails(t *testing.T) {
	// Both proactive and reactive GetActiveSaga calls return NotFound.
	// CreateSagaDraft returns AlreadyExists. The reactive fallback lookup also fails.
	sagaSrv := &fakeSagaRegistryServer{
		createDraftFn: func(_ context.Context, _ *sagav1.CreateSagaDraftRequest) (*sagav1.CreateSagaDraftResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "saga already exists")
		},
		getActiveFn: func(_ context.Context, _ *sagav1.GetActiveSagaRequest) (*sagav1.GetActiveSagaResponse, error) {
			// Both calls (proactive + reactive fallback) return NotFound.
			return nil, status.Error(codes.NotFound, "no active saga found")
		},
	}
	conn := newRefDataTestServerWith(t, sagaSrv)
	client := NewReferenceDataClient(conn, conn)

	_, err := client.RegisterSagaDefinition(testStarlarkCtx(), map[string]any{
		"saga_name": "test-saga",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lookup failed")
}

func TestReferenceDataClient_RegisterSagaDefinition_OtherError_Propagated(t *testing.T) {
	sagaSrv := &fakeSagaRegistryServer{
		// Proactive check returns NotFound, so we proceed to CreateSagaDraft.
		getActiveFn: func(_ context.Context, _ *sagav1.GetActiveSagaRequest) (*sagav1.GetActiveSagaResponse, error) {
			return nil, status.Error(codes.NotFound, "no active saga found")
		},
		createDraftFn: func(_ context.Context, _ *sagav1.CreateSagaDraftRequest) (*sagav1.CreateSagaDraftResponse, error) {
			return nil, status.Error(codes.Internal, "database unavailable")
		},
	}
	conn := newRefDataTestServerWith(t, sagaSrv)
	client := NewReferenceDataClient(conn, conn)

	_, err := client.RegisterSagaDefinition(testStarlarkCtx(), map[string]any{
		"saga_name": "test-saga",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create saga draft")
}

func TestReferenceDataClient_ActivateInstrument_AlreadyActive_TreatedAsSuccess(t *testing.T) {
	refDataSrv := &fakeReferenceDataServer{
		activateInstrumentFn: func(_ context.Context, _ *referencedatav1.ActivateInstrumentRequest) (*referencedatav1.ActivateInstrumentResponse, error) {
			return nil, status.Error(codes.FailedPrecondition, "instrument must be in DRAFT status: GBP")
		},
		retrieveInstrumentFn: func(_ context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:      "inst-uuid-1",
					Code:    req.Code,
					Version: req.Version,
					Status:  referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				},
			}, nil
		},
	}
	conn := newRefDataTestServerWithRefData(t, refDataSrv)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.ActivateInstrument(testStarlarkCtx(), map[string]any{
		"instrument_code": "GBP",
	})
	require.NoError(t, err, "FailedPrecondition with already-ACTIVE instrument should be treated as idempotent success")
	m := result.(map[string]any)
	assert.Equal(t, "GBP", m["instrument_code"])
	assert.Contains(t, m["status"], "ACTIVE")
}

func TestReferenceDataClient_ActivateInstrument_NotActive_ErrorPropagated(t *testing.T) {
	refDataSrv := &fakeReferenceDataServer{
		activateInstrumentFn: func(_ context.Context, _ *referencedatav1.ActivateInstrumentRequest) (*referencedatav1.ActivateInstrumentResponse, error) {
			return nil, status.Error(codes.FailedPrecondition, "instrument must be in DRAFT status: GBP")
		},
		retrieveInstrumentFn: func(_ context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:      "inst-uuid-1",
					Code:    req.Code,
					Version: req.Version,
					Status:  referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED,
				},
			}, nil
		},
	}
	conn := newRefDataTestServerWithRefData(t, refDataSrv)
	client := NewReferenceDataClient(conn, conn)

	_, err := client.ActivateInstrument(testStarlarkCtx(), map[string]any{
		"instrument_code": "GBP",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "activate instrument")
}

// ─── Task 1: Proactive ActivateInstrument idempotency ─────────────────────

func TestReferenceDataClient_ActivateInstrument_ProactiveCheck_AlreadyActive(t *testing.T) {
	// RetrieveInstrument returns ACTIVE on the proactive check,
	// so ActivateInstrument should never be called.
	activateCalled := false
	refDataSrv := &fakeReferenceDataServer{
		retrieveInstrumentFn: func(_ context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:      "inst-uuid-1",
					Code:    req.Code,
					Version: req.Version,
					Status:  referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
				},
			}, nil
		},
		activateInstrumentFn: func(_ context.Context, _ *referencedatav1.ActivateInstrumentRequest) (*referencedatav1.ActivateInstrumentResponse, error) {
			activateCalled = true
			return nil, status.Error(codes.FailedPrecondition, "should not be called")
		},
	}
	conn := newRefDataTestServerWithRefData(t, refDataSrv)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.ActivateInstrument(testStarlarkCtx(), map[string]any{
		"instrument_code": "GBP",
		"version":         int32(1),
	})
	require.NoError(t, err, "proactive check should return success for already-ACTIVE instrument")
	assert.False(t, activateCalled, "ActivateInstrument gRPC should not be called when proactive check finds ACTIVE")

	m := result.(map[string]any)
	assert.Equal(t, "GBP", m["instrument_code"])
	assert.Contains(t, m["status"], "ACTIVE")
}

func TestReferenceDataClient_ActivateInstrument_ProactiveCheck_LookupFails_ProceedsToActivate(t *testing.T) {
	// RetrieveInstrument fails (e.g., NotFound), so the handler proceeds to activate normally.
	refDataSrv := &fakeReferenceDataServer{
		retrieveInstrumentFn: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return nil, status.Error(codes.NotFound, "instrument not found")
		},
	}
	conn := newRefDataTestServerWithRefData(t, refDataSrv)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.ActivateInstrument(testStarlarkCtx(), map[string]any{
		"instrument_code": "GBP",
	})
	require.NoError(t, err, "should proceed to activate when proactive lookup fails")

	m := result.(map[string]any)
	assert.Equal(t, "GBP", m["instrument_code"])
	assert.Contains(t, m["status"], "ACTIVE")
}

func TestReferenceDataClient_ActivateInstrument_ProactiveCheck_DraftState_ProceedsToActivate(t *testing.T) {
	// RetrieveInstrument returns DRAFT, so the handler proceeds to activate.
	refDataSrv := &fakeReferenceDataServer{
		retrieveInstrumentFn: func(_ context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:      "inst-uuid-1",
					Code:    req.Code,
					Version: req.Version,
					Status:  referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT,
				},
			}, nil
		},
	}
	conn := newRefDataTestServerWithRefData(t, refDataSrv)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.ActivateInstrument(testStarlarkCtx(), map[string]any{
		"instrument_code": "GBP",
	})
	require.NoError(t, err, "should proceed to activate when instrument is DRAFT")

	m := result.(map[string]any)
	assert.Contains(t, m["status"], "ACTIVE")
}

// ─── Task 2: Proactive RegisterAccountType idempotency ────────────────────

func TestReferenceDataClient_RegisterAccountType_AlreadyActive_TreatedAsSuccess(t *testing.T) {
	atSrv := &fakeAccountTypeServer{
		getActiveDefinitionFn: func(_ context.Context, req *referencedatav1.GetActiveDefinitionRequest) (*referencedatav1.GetActiveDefinitionResponse, error) {
			return &referencedatav1.GetActiveDefinitionResponse{
				Definition: &referencedatav1.AccountTypeDefinition{
					Id:      "at-uuid-existing",
					Code:    req.Code,
					Version: 1,
					Status:  referencedatav1.AccountTypeStatus_ACCOUNT_TYPE_STATUS_ACTIVE,
				},
			}, nil
		},
		createDraftFn: func(_ context.Context, _ *referencedatav1.CreateDraftRequest) (*referencedatav1.CreateDraftResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "should not be called")
		},
	}
	conn := newRefDataTestServerWithAccountType(t, atSrv)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.RegisterAccountType(testStarlarkCtx(), map[string]any{
		"code":            "ENERGY_TRADING",
		"display_name":    "Energy Trading Account",
		"instrument_code": "GBP",
	})
	require.NoError(t, err, "already-ACTIVE account type should be treated as idempotent success")

	m := result.(map[string]any)
	assert.Equal(t, "at-uuid-existing", m["id"])
	assert.Equal(t, "ENERGY_TRADING", m["code"])
	assert.Contains(t, m["status"], "ACTIVE")
}

func TestReferenceDataClient_RegisterAccountType_LookupNotFound_ProceedsToCreate(t *testing.T) {
	// GetActiveDefinition returns NotFound, so the handler proceeds to create + activate.
	conn := newRefDataTestServerWithAccountType(t, &fakeAccountTypeServer{})
	client := NewReferenceDataClient(conn, conn)

	result, err := client.RegisterAccountType(testStarlarkCtx(), map[string]any{
		"code":            "NEW_TYPE",
		"display_name":    "New Account Type",
		"instrument_code": "GBP",
	})
	require.NoError(t, err, "should proceed to create when GetActiveDefinition returns NotFound")

	m := result.(map[string]any)
	assert.Equal(t, "at-uuid-1", m["id"])
	assert.Contains(t, m["status"], "ACTIVE")
}

func TestReferenceDataClient_RegisterAccountType_LookupError_ProceedsToCreate(t *testing.T) {
	// GetActiveDefinition returns an unexpected error, handler should still proceed.
	atSrv := &fakeAccountTypeServer{
		getActiveDefinitionFn: func(_ context.Context, _ *referencedatav1.GetActiveDefinitionRequest) (*referencedatav1.GetActiveDefinitionResponse, error) {
			return nil, status.Error(codes.Internal, "database unavailable")
		},
	}
	conn := newRefDataTestServerWithAccountType(t, atSrv)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.RegisterAccountType(testStarlarkCtx(), map[string]any{
		"code":            "NEW_TYPE",
		"display_name":    "New Account Type",
		"instrument_code": "GBP",
	})
	require.NoError(t, err, "should proceed to create when GetActiveDefinition returns error")

	m := result.(map[string]any)
	assert.Contains(t, m["status"], "ACTIVE")
}

// ─── Task 3: Proactive RegisterSagaDefinition idempotency ─────────────────

func TestReferenceDataClient_RegisterSagaDefinition_ProactiveCheck_AlreadyActive(t *testing.T) {
	createDraftCalled := false
	sagaSrv := &fakeSagaRegistryServer{
		getActiveFn: func(_ context.Context, req *sagav1.GetActiveSagaRequest) (*sagav1.GetActiveSagaResponse, error) {
			return &sagav1.GetActiveSagaResponse{
				Saga: &sagav1.SagaDefinition{
					Id:     "existing-saga-uuid",
					Name:   req.Name,
					Status: sagav1.SagaStatus_SAGA_STATUS_ACTIVE,
				},
			}, nil
		},
		createDraftFn: func(_ context.Context, _ *sagav1.CreateSagaDraftRequest) (*sagav1.CreateSagaDraftResponse, error) {
			createDraftCalled = true
			return nil, status.Error(codes.AlreadyExists, "should not be called")
		},
	}
	conn := newRefDataTestServerWith(t, sagaSrv)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.RegisterSagaDefinition(testStarlarkCtx(), map[string]any{
		"saga_name":    "test-saga",
		"display_name": "Test Saga",
		"script":       "def execute(): pass",
	})
	require.NoError(t, err, "proactive check should return success for already-ACTIVE saga")
	assert.False(t, createDraftCalled, "CreateSagaDraft should not be called when proactive check finds ACTIVE")

	m := result.(map[string]any)
	assert.Equal(t, "existing-saga-uuid", m["saga_id"])
	assert.Equal(t, "test-saga", m["saga_name"])
	assert.Contains(t, m["status"], "ACTIVE")
}

func TestReferenceDataClient_RegisterSagaDefinition_ProactiveCheck_NotFound_ProceedsToCreate(t *testing.T) {
	sagaSrv := &fakeSagaRegistryServer{
		getActiveFn: func(_ context.Context, _ *sagav1.GetActiveSagaRequest) (*sagav1.GetActiveSagaResponse, error) {
			return nil, status.Error(codes.NotFound, "no active saga found")
		},
	}
	conn := newRefDataTestServerWith(t, sagaSrv)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.RegisterSagaDefinition(testStarlarkCtx(), map[string]any{
		"saga_name":    "new-saga",
		"display_name": "New Saga",
		"script":       "def execute(): pass",
	})
	require.NoError(t, err, "should proceed to create when proactive check returns NotFound")

	m := result.(map[string]any)
	assert.Equal(t, "saga-uuid-1", m["saga_id"])
	assert.Contains(t, m["status"], "ACTIVE")
}

func TestReferenceDataClient_RegisterSagaDefinition_ReactiveFallback_AlreadyExists(t *testing.T) {
	// Proactive check returns NotFound, but CreateSagaDraft returns AlreadyExists (race condition).
	// The reactive fallback should handle this.
	callCount := 0
	sagaSrv := &fakeSagaRegistryServer{
		getActiveFn: func(_ context.Context, req *sagav1.GetActiveSagaRequest) (*sagav1.GetActiveSagaResponse, error) {
			callCount++
			if callCount == 1 {
				// First call (proactive check): not found
				return nil, status.Error(codes.NotFound, "no active saga found")
			}
			// Second call (reactive fallback): return the saga
			return &sagav1.GetActiveSagaResponse{
				Saga: &sagav1.SagaDefinition{
					Id:     "race-condition-saga-uuid",
					Name:   req.Name,
					Status: sagav1.SagaStatus_SAGA_STATUS_ACTIVE,
				},
			}, nil
		},
		createDraftFn: func(_ context.Context, _ *sagav1.CreateSagaDraftRequest) (*sagav1.CreateSagaDraftResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "saga already exists")
		},
	}
	conn := newRefDataTestServerWith(t, sagaSrv)
	client := NewReferenceDataClient(conn, conn)

	result, err := client.RegisterSagaDefinition(testStarlarkCtx(), map[string]any{
		"saga_name":    "race-saga",
		"display_name": "Race Condition Saga",
		"script":       "def execute(): pass",
	})
	require.NoError(t, err, "reactive AlreadyExists fallback should handle race condition")

	m := result.(map[string]any)
	assert.Equal(t, "race-condition-saga-uuid", m["saga_id"])
	assert.Contains(t, m["status"], "ACTIVE")
}
