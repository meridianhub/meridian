package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	cadomain "github.com/meridianhub/meridian/internal/current-account/domain"
	"github.com/meridianhub/meridian/internal/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/internal/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/internal/payment-order/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// testLogger returns a logger that discards output for cleaner test output
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Test errors for mock responses
var (
	errInsufficientFunds  = errors.New("insufficient funds")
	errGatewayUnavailable = errors.New("gateway unavailable")
)

// MockRepository implements persistence.Repository for testing.
// Thread-safe for use with async saga tests.
type MockRepository struct {
	mu                   sync.RWMutex
	paymentOrders        map[uuid.UUID]*domain.PaymentOrder
	idempotencyKeyIndex  map[string]*domain.PaymentOrder
	gatewayRefIndex      map[string]*domain.PaymentOrder
	debtorAccountIndex   map[string][]*domain.PaymentOrder
	createErr            error
	findByIDErr          error
	findByIdempotencyErr error
	updateErr            error
}

func NewMockRepository() *MockRepository {
	return &MockRepository{
		paymentOrders:       make(map[uuid.UUID]*domain.PaymentOrder),
		idempotencyKeyIndex: make(map[string]*domain.PaymentOrder),
		gatewayRefIndex:     make(map[string]*domain.PaymentOrder),
		debtorAccountIndex:  make(map[string][]*domain.PaymentOrder),
	}
}

// copyPaymentOrder creates a deep copy of a PaymentOrder to simulate database behavior
// where each query returns a fresh object rather than a shared pointer.
func copyPaymentOrder(po *domain.PaymentOrder) *domain.PaymentOrder {
	if po == nil {
		return nil
	}
	poCopy := *po // shallow copy of the struct
	// Deep copy any pointer fields
	if po.ReservedAt != nil {
		t := *po.ReservedAt
		poCopy.ReservedAt = &t
	}
	if po.ExecutingAt != nil {
		t := *po.ExecutingAt
		poCopy.ExecutingAt = &t
	}
	if po.CompletedAt != nil {
		t := *po.CompletedAt
		poCopy.CompletedAt = &t
	}
	if po.FailedAt != nil {
		t := *po.FailedAt
		poCopy.FailedAt = &t
	}
	if po.CancelledAt != nil {
		t := *po.CancelledAt
		poCopy.CancelledAt = &t
	}
	if po.ReversedAt != nil {
		t := *po.ReversedAt
		poCopy.ReversedAt = &t
	}
	return &poCopy
}

func (m *MockRepository) Create(po *domain.PaymentOrder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	// Store a copy to simulate database behavior where the stored object is separate
	stored := copyPaymentOrder(po)
	m.paymentOrders[po.ID] = stored
	m.idempotencyKeyIndex[po.IdempotencyKey] = stored
	m.debtorAccountIndex[po.DebtorAccountID] = append(m.debtorAccountIndex[po.DebtorAccountID], stored)
	return nil
}

func (m *MockRepository) FindByID(id uuid.UUID) (*domain.PaymentOrder, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.findByIDErr != nil {
		return nil, m.findByIDErr
	}
	po, ok := m.paymentOrders[id]
	if !ok {
		return nil, persistence.ErrPaymentOrderNotFound
	}
	// Return a copy to simulate database behavior
	return copyPaymentOrder(po), nil
}

func (m *MockRepository) FindByIdempotencyKey(key string) (*domain.PaymentOrder, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.findByIdempotencyErr != nil {
		return nil, m.findByIdempotencyErr
	}
	po, ok := m.idempotencyKeyIndex[key]
	if !ok {
		return nil, persistence.ErrPaymentOrderNotFound
	}
	// Return a copy to simulate database behavior
	return copyPaymentOrder(po), nil
}

func (m *MockRepository) FindByGatewayReferenceID(gatewayRefID string) (*domain.PaymentOrder, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	po, ok := m.gatewayRefIndex[gatewayRefID]
	if !ok {
		return nil, persistence.ErrPaymentOrderNotFound
	}
	// Return a copy to simulate database behavior
	return copyPaymentOrder(po), nil
}

func (m *MockRepository) FindByDebtorAccountID(accountID string) ([]*domain.PaymentOrder, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pos := m.debtorAccountIndex[accountID]
	// Return copies to simulate database behavior
	result := make([]*domain.PaymentOrder, len(pos))
	for i, po := range pos {
		result[i] = copyPaymentOrder(po)
	}
	return result, nil
}

func (m *MockRepository) FindByDebtorAccountIDPaginated(accountID string, limit, offset int) (*persistence.PaginatedResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pos := m.debtorAccountIndex[accountID]
	totalCount := len(pos)

	// Apply pagination bounds
	if offset >= totalCount {
		return &persistence.PaginatedResult{
			PaymentOrders: []*domain.PaymentOrder{},
			TotalCount:    int64(totalCount),
			HasMore:       false,
		}, nil
	}

	end := offset + limit
	if end > totalCount {
		end = totalCount
	}

	// Return copies to simulate database behavior
	result := make([]*domain.PaymentOrder, 0, end-offset)
	for i := offset; i < end; i++ {
		result = append(result, copyPaymentOrder(pos[i]))
	}

	return &persistence.PaginatedResult{
		PaymentOrders: result,
		TotalCount:    int64(totalCount),
		HasMore:       end < totalCount,
	}, nil
}

func (m *MockRepository) Update(po *domain.PaymentOrder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateErr != nil {
		return m.updateErr
	}
	// Store a copy to simulate database behavior
	stored := copyPaymentOrder(po)
	m.paymentOrders[po.ID] = stored
	if po.GatewayReferenceID != "" {
		m.gatewayRefIndex[po.GatewayReferenceID] = stored
	}
	return nil
}

// MockCurrentAccountClient implements CurrentAccountClient for testing
type MockCurrentAccountClient struct {
	initiateLienResp   *currentaccountv1.InitiateLienResponse
	initiateLienErr    error
	terminateLienResp  *currentaccountv1.TerminateLienResponse
	terminateLienErr   error
	executeLienResp    *currentaccountv1.ExecuteLienResponse
	executeLienErr     error
	initiateLienCalled bool
	terminateLienCalls int
	executeLienCalled  bool
}

func (m *MockCurrentAccountClient) InitiateLien(_ context.Context, _ *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error) {
	m.initiateLienCalled = true
	if m.initiateLienErr != nil {
		return nil, m.initiateLienErr
	}
	return m.initiateLienResp, nil
}

func (m *MockCurrentAccountClient) TerminateLien(_ context.Context, _ *currentaccountv1.TerminateLienRequest) (*currentaccountv1.TerminateLienResponse, error) {
	m.terminateLienCalls++
	if m.terminateLienErr != nil {
		return nil, m.terminateLienErr
	}
	return m.terminateLienResp, nil
}

func (m *MockCurrentAccountClient) ExecuteLien(_ context.Context, _ *currentaccountv1.ExecuteLienRequest) (*currentaccountv1.ExecuteLienResponse, error) {
	m.executeLienCalled = true
	if m.executeLienErr != nil {
		return nil, m.executeLienErr
	}
	return m.executeLienResp, nil
}

func (m *MockCurrentAccountClient) Close() error {
	return nil
}

// MockPaymentGateway implements gateway.PaymentGateway for testing
type MockPaymentGateway struct {
	response   gateway.PaymentResponse
	err        error
	callCount  int
	lastReqKey string
}

func (m *MockPaymentGateway) SendPayment(_ context.Context, req gateway.PaymentRequest) (gateway.PaymentResponse, error) {
	m.callCount++
	m.lastReqKey = req.IdempotencyKey
	if m.err != nil {
		return gateway.PaymentResponse{}, m.err
	}
	return m.response, nil
}

// Helper to create a valid InitiatePaymentOrderRequest
// nolint:unparam // debtorAccountID is parameterized for test clarity even if currently constant
func newInitiateRequest(idempotencyKey, debtorAccountID, creditorRef string, amountCents int64) *pb.InitiatePaymentOrderRequest {
	return &pb.InitiatePaymentOrderRequest{
		DebtorAccountId:   debtorAccountID,
		CreditorReference: creditorRef,
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        amountCents / 100,
				Nanos:        int32((amountCents % 100) * 10000000),
			},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: idempotencyKey},
	}
}

// Test NewService constructor
func TestNewService(t *testing.T) {
	repo := NewMockRepository()
	svc, err := NewService(repo)

	require.NoError(t, err)
	assert.NotNil(t, svc)
	assert.NotNil(t, svc.repo)
	assert.NotNil(t, svc.logger)
}

func TestNewService_NilRepository_ReturnsError(t *testing.T) {
	svc, err := NewService(nil)

	assert.Nil(t, svc)
	assert.ErrorIs(t, err, ErrRepositoryNil)
}

// Test NewServiceWithConfig
func TestNewServiceWithConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr error
	}{
		{
			name: "nil repository returns error",
			config: Config{
				Repository: nil,
			},
			wantErr: ErrRepositoryNil,
		},
		{
			name: "nil current account client returns error",
			config: Config{
				Repository:           NewMockRepository(),
				CurrentAccountClient: nil,
			},
			wantErr: ErrCurrentAccountClientNil,
		},
		{
			name: "nil payment gateway returns error",
			config: Config{
				Repository:           NewMockRepository(),
				CurrentAccountClient: &MockCurrentAccountClient{},
				PaymentGateway:       nil,
			},
			wantErr: ErrPaymentGatewayNil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewServiceWithConfig(tt.config)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// Test InitiatePaymentOrder
func TestInitiatePaymentOrder_Success(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	req := newInitiateRequest("test-key-1", "ACC-12345678", "GB82WEST12345698765432", 10000)

	resp, err := svc.InitiatePaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.PaymentOrder.PaymentOrderId)
	assert.Equal(t, "ACC-12345678", resp.PaymentOrder.DebtorAccountId)
	assert.Equal(t, "GB82WEST12345698765432", resp.PaymentOrder.CreditorReference)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_INITIATED, resp.PaymentOrder.Status)
	assert.NotEmpty(t, resp.PaymentOrder.CorrelationId)
}

func TestInitiatePaymentOrder_Idempotent(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	req := newInitiateRequest("idempotent-key", "ACC-12345678", "GB82WEST12345698765432", 10000)

	// First call
	resp1, err := svc.InitiatePaymentOrder(context.Background(), req)
	require.NoError(t, err)

	// Second call with same idempotency key
	resp2, err := svc.InitiatePaymentOrder(context.Background(), req)
	require.NoError(t, err)

	// Should return the same payment order
	assert.Equal(t, resp1.PaymentOrder.PaymentOrderId, resp2.PaymentOrder.PaymentOrderId)
}

func TestInitiatePaymentOrder_InvalidAmount(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	// Zero amount
	req := newInitiateRequest("test-key", "ACC-12345678", "GB82WEST12345698765432", 0)

	_, err := svc.InitiatePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "positive")
}

func TestInitiatePaymentOrder_NegativeAmount(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	// Negative amount
	req := &pb.InitiatePaymentOrderRequest{
		DebtorAccountId:   "ACC-12345678",
		CreditorReference: "GB82WEST12345698765432",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        -100,
				Nanos:        0,
			},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "test-key"},
	}

	_, err := svc.InitiatePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ErrDatabaseError is a test error for database failures
var ErrDatabaseError = errors.New("database error")

func TestInitiatePaymentOrder_RepositoryError(t *testing.T) {
	repo := NewMockRepository()
	repo.createErr = ErrDatabaseError
	svc, _ := NewService(repo)

	req := newInitiateRequest("test-key", "ACC-12345678", "GB82WEST12345698765432", 10000)

	_, err := svc.InitiatePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// Test RetrievePaymentOrder
func TestRetrievePaymentOrder_Success(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	// Create a payment order first
	amount, _ := cadomain.NewMoney("GBP", 10000)
	po, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "test-key", uuid.New().String())
	_ = repo.Create(po)

	req := &pb.RetrievePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
	}

	resp, err := svc.RetrievePaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, po.ID.String(), resp.PaymentOrder.PaymentOrderId)
	assert.Equal(t, "ACC-12345678", resp.PaymentOrder.DebtorAccountId)
}

func TestRetrievePaymentOrder_NotFound(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	req := &pb.RetrievePaymentOrderRequest{
		PaymentOrderId: uuid.New().String(),
	}

	_, err := svc.RetrievePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestRetrievePaymentOrder_InvalidID(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	req := &pb.RetrievePaymentOrderRequest{
		PaymentOrderId: "not-a-uuid",
	}

	_, err := svc.RetrievePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// Test CancelPaymentOrder
func TestCancelPaymentOrder_Success(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	// Create a payment order in INITIATED state
	amount, _ := cadomain.NewMoney("GBP", 10000)
	po, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "test-key", uuid.New().String())
	_ = repo.Create(po)

	req := &pb.CancelPaymentOrderRequest{
		PaymentOrderId:     po.ID.String(),
		CancellationReason: "User requested cancellation",
		CancelledBy:        "user@example.com",
		IdempotencyKey:     &commonpb.IdempotencyKey{Key: "cancel-key"},
	}

	resp, err := svc.CancelPaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_CANCELLED, resp.PaymentOrder.Status)
}

func TestCancelPaymentOrder_AlreadyCancelled_Idempotent(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	// Create and cancel a payment order
	amount, _ := cadomain.NewMoney("GBP", 10000)
	po, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "test-key", uuid.New().String())
	_ = po.Cancel("Already cancelled")
	_ = repo.Create(po)

	req := &pb.CancelPaymentOrderRequest{
		PaymentOrderId:     po.ID.String(),
		CancellationReason: "Second cancellation attempt",
		CancelledBy:        "user@example.com",
		IdempotencyKey:     &commonpb.IdempotencyKey{Key: "cancel-key"},
	}

	resp, err := svc.CancelPaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_CANCELLED, resp.PaymentOrder.Status)
}

func TestCancelPaymentOrder_NotCancellable(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	// Create a payment order and move it to EXECUTING state
	amount, _ := cadomain.NewMoney("GBP", 10000)
	po, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "test-key", uuid.New().String())
	_ = po.Reserve("lien-123")
	_ = po.Execute("gateway-ref-123")
	_ = repo.Create(po)

	req := &pb.CancelPaymentOrderRequest{
		PaymentOrderId:     po.ID.String(),
		CancellationReason: "User requested cancellation",
		CancelledBy:        "user@example.com",
		IdempotencyKey:     &commonpb.IdempotencyKey{Key: "cancel-key"},
	}

	_, err := svc.CancelPaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestCancelPaymentOrder_ReleasesLien(t *testing.T) {
	repo := NewMockRepository()
	caClient := &MockCurrentAccountClient{
		terminateLienResp: &currentaccountv1.TerminateLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId: "lien-123",
				Status: currentaccountv1.LienStatus_LIEN_STATUS_TERMINATED,
			},
		},
	}

	svc := &Service{
		repo:                 repo,
		currentAccountClient: caClient,
		logger:               testLogger(),
	}

	// Create a payment order in RESERVED state with a lien
	amount, _ := cadomain.NewMoney("GBP", 10000)
	po, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "test-key", uuid.New().String())
	_ = po.Reserve("lien-123")
	_ = repo.Create(po)

	req := &pb.CancelPaymentOrderRequest{
		PaymentOrderId:     po.ID.String(),
		CancellationReason: "User requested cancellation",
		CancelledBy:        "user@example.com",
		IdempotencyKey:     &commonpb.IdempotencyKey{Key: "cancel-key"},
	}

	resp, err := svc.CancelPaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_CANCELLED, resp.PaymentOrder.Status)
	assert.Equal(t, 1, caClient.terminateLienCalls)
}

// Test UpdatePaymentOrder
func TestUpdatePaymentOrder_Settled(t *testing.T) {
	repo := NewMockRepository()
	caClient := &MockCurrentAccountClient{
		executeLienResp: &currentaccountv1.ExecuteLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId: "lien-123",
				Status: currentaccountv1.LienStatus_LIEN_STATUS_EXECUTED,
			},
		},
	}

	svc := &Service{
		repo:                 repo,
		currentAccountClient: caClient,
		logger:               testLogger(),
	}

	// Create a payment order in EXECUTING state
	amount, _ := cadomain.NewMoney("GBP", 10000)
	po, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "test-key", uuid.New().String())
	_ = po.Reserve("lien-123")
	_ = po.Execute("gateway-ref-123")
	_ = repo.Create(po)

	req := &pb.UpdatePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		GatewayStatus:  pb.GatewayStatus_GATEWAY_STATUS_SETTLED,
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "update-key"},
	}

	resp, err := svc.UpdatePaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED, resp.PaymentOrder.Status)
	assert.True(t, caClient.executeLienCalled)
}

func TestUpdatePaymentOrder_Rejected(t *testing.T) {
	repo := NewMockRepository()
	caClient := &MockCurrentAccountClient{
		terminateLienResp: &currentaccountv1.TerminateLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId: "lien-123",
				Status: currentaccountv1.LienStatus_LIEN_STATUS_TERMINATED,
			},
		},
	}

	svc := &Service{
		repo:                 repo,
		currentAccountClient: caClient,
		logger:               testLogger(),
	}

	// Create a payment order in EXECUTING state
	amount, _ := cadomain.NewMoney("GBP", 10000)
	po, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "test-key", uuid.New().String())
	_ = po.Reserve("lien-123")
	_ = po.Execute("gateway-ref-123")
	_ = repo.Create(po)

	req := &pb.UpdatePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		GatewayStatus:  pb.GatewayStatus_GATEWAY_STATUS_REJECTED,
		GatewayMessage: "Insufficient funds at destination",
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "update-key"},
	}

	resp, err := svc.UpdatePaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_FAILED, resp.PaymentOrder.Status)
	assert.Equal(t, 1, caClient.terminateLienCalls)
}

func TestUpdatePaymentOrder_ByGatewayReferenceID(t *testing.T) {
	repo := NewMockRepository()
	caClient := &MockCurrentAccountClient{
		executeLienResp: &currentaccountv1.ExecuteLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId: "lien-123",
				Status: currentaccountv1.LienStatus_LIEN_STATUS_EXECUTED,
			},
		},
	}

	svc := &Service{
		repo:                 repo,
		currentAccountClient: caClient,
		logger:               testLogger(),
	}

	// Create a payment order in EXECUTING state
	amount, _ := cadomain.NewMoney("GBP", 10000)
	po, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "test-key", uuid.New().String())
	_ = po.Reserve("lien-123")
	_ = po.Execute("gateway-ref-123")
	_ = repo.Create(po)
	_ = repo.Update(po) // This adds gateway ref to index

	req := &pb.UpdatePaymentOrderRequest{
		GatewayReferenceId: "gateway-ref-123",
		GatewayStatus:      pb.GatewayStatus_GATEWAY_STATUS_SETTLED,
		IdempotencyKey:     &commonpb.IdempotencyKey{Key: "update-key"},
	}

	resp, err := svc.UpdatePaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED, resp.PaymentOrder.Status)
}

func TestUpdatePaymentOrder_NotFound(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	req := &pb.UpdatePaymentOrderRequest{
		PaymentOrderId: uuid.New().String(),
		GatewayStatus:  pb.GatewayStatus_GATEWAY_STATUS_SETTLED,
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "update-key"},
	}

	_, err := svc.UpdatePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestUpdatePaymentOrder_MissingIdentifier(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	req := &pb.UpdatePaymentOrderRequest{
		GatewayStatus:  pb.GatewayStatus_GATEWAY_STATUS_SETTLED,
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "update-key"},
	}

	_, err := svc.UpdatePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdatePaymentOrder_Idempotent_Settled(t *testing.T) {
	repo := NewMockRepository()
	caClient := &MockCurrentAccountClient{
		executeLienResp: &currentaccountv1.ExecuteLienResponse{},
	}
	svc := &Service{
		repo:                 repo,
		currentAccountClient: caClient,
		logger:               testLogger(),
	}

	// Create a payment order in EXECUTING state
	amount, _ := cadomain.NewMoney("GBP", 10000)
	po, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "test-key", "correlation-123")
	_ = po.Reserve("lien-123")
	_ = po.Execute("gateway-ref-123")
	_ = repo.Create(po)

	req := &pb.UpdatePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		GatewayStatus:  pb.GatewayStatus_GATEWAY_STATUS_SETTLED,
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "update-key"},
	}

	// First call - should succeed
	resp1, err := svc.UpdatePaymentOrder(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED, resp1.PaymentOrder.Status)

	// Second call with same request - should be idempotent
	resp2, err := svc.UpdatePaymentOrder(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED, resp2.PaymentOrder.Status)
	assert.Equal(t, resp1.PaymentOrder.PaymentOrderId, resp2.PaymentOrder.PaymentOrderId)
}

func TestUpdatePaymentOrder_Idempotent_Rejected(t *testing.T) {
	repo := NewMockRepository()
	caClient := &MockCurrentAccountClient{
		terminateLienResp: &currentaccountv1.TerminateLienResponse{},
	}
	svc := &Service{
		repo:                 repo,
		currentAccountClient: caClient,
		logger:               testLogger(),
	}

	// Create a payment order in EXECUTING state
	amount, _ := cadomain.NewMoney("GBP", 10000)
	po, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "test-key", "correlation-123")
	_ = po.Reserve("lien-123")
	_ = po.Execute("gateway-ref-123")
	_ = repo.Create(po)

	req := &pb.UpdatePaymentOrderRequest{
		PaymentOrderId: po.ID.String(),
		GatewayStatus:  pb.GatewayStatus_GATEWAY_STATUS_REJECTED,
		GatewayMessage: "Insufficient funds",
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "update-key"},
	}

	// First call - should succeed
	resp1, err := svc.UpdatePaymentOrder(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_FAILED, resp1.PaymentOrder.Status)

	// Second call with same request - should be idempotent
	resp2, err := svc.UpdatePaymentOrder(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_FAILED, resp2.PaymentOrder.Status)
	assert.Equal(t, resp1.PaymentOrder.PaymentOrderId, resp2.PaymentOrder.PaymentOrderId)
}

// Test ListPaymentOrders
func TestListPaymentOrders_Success(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	// Create some payment orders
	amount, _ := cadomain.NewMoney("GBP", 10000)
	po1, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "key-1", uuid.New().String())
	po2, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765433", amount, "key-2", uuid.New().String())
	_ = repo.Create(po1)
	_ = repo.Create(po2)

	req := &pb.ListPaymentOrdersRequest{
		DebtorAccountId: "ACC-12345678",
	}

	resp, err := svc.ListPaymentOrders(context.Background(), req)

	require.NoError(t, err)
	assert.Len(t, resp.PaymentOrders, 2)
}

func TestListPaymentOrders_EmptyDebtorAccountID(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	req := &pb.ListPaymentOrdersRequest{}

	_, err := svc.ListPaymentOrders(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListPaymentOrders_Pagination(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	// Create 5 payment orders
	amount, _ := cadomain.NewMoney("GBP", 10000)
	for i := 0; i < 5; i++ {
		po, _ := domain.NewPaymentOrder(
			"ACC-12345678",
			"GB82WEST12345698765432",
			amount,
			fmt.Sprintf("key-%d", i),
			uuid.New().String(),
		)
		_ = repo.Create(po)
	}

	// Test first page with page size 2
	req := &pb.ListPaymentOrdersRequest{
		DebtorAccountId: "ACC-12345678",
		Pagination: &commonpb.Pagination{
			PageSize: 2,
		},
	}

	resp, err := svc.ListPaymentOrders(context.Background(), req)

	require.NoError(t, err)
	assert.Len(t, resp.PaymentOrders, 2)
	assert.Equal(t, int64(5), resp.Pagination.TotalCount)
	assert.NotEmpty(t, resp.Pagination.NextPageToken)

	// Test second page
	req.Pagination.PageToken = resp.Pagination.NextPageToken
	resp2, err := svc.ListPaymentOrders(context.Background(), req)

	require.NoError(t, err)
	assert.Len(t, resp2.PaymentOrders, 2)
	assert.NotEmpty(t, resp2.Pagination.NextPageToken)

	// Test last page
	req.Pagination.PageToken = resp2.Pagination.NextPageToken
	resp3, err := svc.ListPaymentOrders(context.Background(), req)

	require.NoError(t, err)
	assert.Len(t, resp3.PaymentOrders, 1) // Only 1 remaining
	assert.Empty(t, resp3.Pagination.NextPageToken)
}

func TestListPaymentOrders_InvalidPageToken(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	req := &pb.ListPaymentOrdersRequest{
		DebtorAccountId: "ACC-12345678",
		Pagination: &commonpb.Pagination{
			PageToken: "not-a-number",
		},
	}

	_, err := svc.ListPaymentOrders(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "page_token")
}

func TestListPaymentOrders_NegativePageToken(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	req := &pb.ListPaymentOrdersRequest{
		DebtorAccountId: "ACC-12345678",
		Pagination: &commonpb.Pagination{
			PageToken: "-5",
		},
	}

	_, err := svc.ListPaymentOrders(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListPaymentOrders_PageSizeExceedsMax(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	// Create 3 payment orders
	amount, _ := cadomain.NewMoney("GBP", 10000)
	for i := 0; i < 3; i++ {
		po, _ := domain.NewPaymentOrder(
			"ACC-12345678",
			"GB82WEST12345698765432",
			amount,
			fmt.Sprintf("key-%d", i),
			uuid.New().String(),
		)
		_ = repo.Create(po)
	}

	req := &pb.ListPaymentOrdersRequest{
		DebtorAccountId: "ACC-12345678",
		Pagination: &commonpb.Pagination{
			PageSize: 5000, // Exceeds max of 1000
		},
	}

	resp, err := svc.ListPaymentOrders(context.Background(), req)

	require.NoError(t, err)
	// Should return all 3 (page size capped but we only have 3)
	assert.Len(t, resp.PaymentOrders, 3)
}

func TestListPaymentOrders_OffsetBeyondResults(t *testing.T) {
	repo := NewMockRepository()
	svc, _ := NewService(repo)

	// Create 2 payment orders
	amount, _ := cadomain.NewMoney("GBP", 10000)
	for i := 0; i < 2; i++ {
		po, _ := domain.NewPaymentOrder(
			"ACC-12345678",
			"GB82WEST12345698765432",
			amount,
			fmt.Sprintf("key-%d", i),
			uuid.New().String(),
		)
		_ = repo.Create(po)
	}

	req := &pb.ListPaymentOrdersRequest{
		DebtorAccountId: "ACC-12345678",
		Pagination: &commonpb.Pagination{
			PageToken: "100", // Offset beyond the 2 results
		},
	}

	resp, err := svc.ListPaymentOrders(context.Background(), req)

	require.NoError(t, err)
	assert.Len(t, resp.PaymentOrders, 0)
	assert.Empty(t, resp.Pagination.NextPageToken)
	assert.Equal(t, int64(2), resp.Pagination.TotalCount)
}

// Test proto conversion helpers
func TestToProto(t *testing.T) {
	amount, _ := cadomain.NewMoney("GBP", 10000)
	po, _ := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "test-key", "correlation-123")

	// Add some state
	_ = po.Reserve("lien-123")
	now := time.Now()
	po.ExecutingAt = &now

	proto := toProto(po)

	assert.Equal(t, po.ID.String(), proto.PaymentOrderId)
	assert.Equal(t, po.DebtorAccountID, proto.DebtorAccountId)
	assert.Equal(t, po.CreditorReference, proto.CreditorReference)
	assert.Equal(t, po.IdempotencyKey, proto.IdempotencyKey)
	assert.Equal(t, po.CorrelationID, proto.CorrelationId)
	assert.Equal(t, "lien-123", proto.LienId)
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_RESERVED, proto.Status)
	assert.NotNil(t, proto.ReservedAt)
}

func TestMapStatusToProto(t *testing.T) {
	tests := []struct {
		domain domain.PaymentOrderStatus
		want   pb.PaymentOrderStatus
	}{
		{domain.PaymentOrderStatusInitiated, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_INITIATED},
		{domain.PaymentOrderStatusReserved, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_RESERVED},
		{domain.PaymentOrderStatusExecuting, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_EXECUTING},
		{domain.PaymentOrderStatusCompleted, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED},
		{domain.PaymentOrderStatusFailed, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_FAILED},
		{domain.PaymentOrderStatusCancelled, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_CANCELLED},
		{domain.PaymentOrderStatusReversed, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_REVERSED},
		{"UNKNOWN", pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.domain), func(t *testing.T) {
			got := mapStatusToProto(tt.domain)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProtoToMoney(t *testing.T) {
	tests := []struct {
		name      string
		amount    *commonpb.MoneyAmount
		wantCents int64
		wantErr   bool
	}{
		{
			name: "basic amount",
			amount: &commonpb.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        500000000, // 0.50
				},
			},
			wantCents: 10050,
			wantErr:   false,
		},
		{
			name: "whole units",
			amount: &commonpb.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "USD",
					Units:        50,
					Nanos:        0,
				},
			},
			wantCents: 5000,
			wantErr:   false,
		},
		{
			name:      "nil amount",
			amount:    nil,
			wantCents: 0,
			wantErr:   true,
		},
		{
			name: "negative amount with nanos",
			amount: &commonpb.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        -10,
					Nanos:        -123456789, // -10.123456789 should round to -1012 cents
				},
			},
			wantCents: -1012, // -10.12 (rounded from -10.123456789)
			wantErr:   false,
		},
		{
			name: "negative amount rounds correctly",
			amount: &commonpb.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "USD",
					Units:        -5,
					Nanos:        -555000000, // -5.555 should round to -556 cents
				},
			},
			wantCents: -556, // -5.56 (rounded from -5.555)
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := protoToMoney(tt.amount)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantCents, got.AmountCents())
			}
		})
	}
}

func TestToMoneyAmount(t *testing.T) {
	amount, _ := cadomain.NewMoney("GBP", 10050) // £100.50

	proto := toMoneyAmount(amount)

	assert.Equal(t, "GBP", proto.Amount.CurrencyCode)
	assert.Equal(t, int64(100), proto.Amount.Units)
	assert.Equal(t, int32(500000000), proto.Amount.Nanos)
}

// TestSagaOrchestration_HappyPath tests the full saga flow from InitiatePaymentOrder
// through to EXECUTING state, exercising the async saga orchestration.
func TestSagaOrchestration_HappyPath(t *testing.T) {
	repo := NewMockRepository()

	// Set up mock CurrentAccountClient with successful lien response
	caClient := &MockCurrentAccountClient{
		initiateLienResp: &currentaccountv1.InitiateLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId: "test-lien-123",
			},
		},
	}

	// Set up mock PaymentGateway with successful acceptance response
	gwMock := &MockPaymentGateway{
		response: gateway.PaymentResponse{
			Status:             gateway.StatusAccepted,
			GatewayReferenceID: "gw-ref-456",
		},
	}

	// Create service with all dependencies configured
	svc := &Service{
		repo:                 repo,
		logger:               slog.New(slog.NewJSONHandler(io.Discard, nil)),
		currentAccountClient: caClient,
		paymentGateway:       gwMock,
		// kafkaProducer is nil - events won't be published but saga still runs
	}

	// Initiate payment order
	ctx := context.Background()
	req := newInitiateRequest("saga-test-key", "debtor-123", "creditor-ref", 5000)

	resp, err := svc.InitiatePaymentOrder(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.PaymentOrder)

	paymentOrderID := resp.PaymentOrder.PaymentOrderId

	// Initial response should show INITIATED status
	assert.Equal(t, pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_INITIATED, resp.PaymentOrder.Status)

	// Wait for async saga to complete
	// The saga runs in a goroutine and should reach EXECUTING state
	// Use polling with timeout to verify final state
	require.Eventually(t, func() bool {
		po, err := repo.FindByID(uuid.MustParse(paymentOrderID))
		if err != nil {
			return false
		}
		return po.Status == domain.PaymentOrderStatusExecuting
	}, 2*time.Second, 50*time.Millisecond, "payment order should reach EXECUTING state")

	// Verify final state
	po, err := repo.FindByID(uuid.MustParse(paymentOrderID))
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentOrderStatusExecuting, po.Status)
	assert.Equal(t, "test-lien-123", po.LienID)
	assert.Equal(t, "gw-ref-456", po.GatewayReferenceID)
	assert.NotNil(t, po.ReservedAt)
	assert.NotNil(t, po.ExecutingAt)
}

// TestSagaOrchestration_LienFailure tests saga compensation when lien creation fails.
func TestSagaOrchestration_LienFailure(t *testing.T) {
	repo := NewMockRepository()

	// Set up mock CurrentAccountClient to fail lien creation
	caClient := &MockCurrentAccountClient{
		initiateLienErr: errInsufficientFunds,
	}

	// Gateway mock won't be called since lien fails first
	gwMock := &MockPaymentGateway{}

	svc := &Service{
		repo:                 repo,
		logger:               slog.New(slog.NewJSONHandler(io.Discard, nil)),
		currentAccountClient: caClient,
		paymentGateway:       gwMock,
	}

	ctx := context.Background()
	req := newInitiateRequest("saga-fail-key", "debtor-456", "creditor-ref", 5000)

	resp, err := svc.InitiatePaymentOrder(ctx, req)

	require.NoError(t, err) // InitiatePaymentOrder succeeds, saga fails async
	require.NotNil(t, resp)

	paymentOrderID := resp.PaymentOrder.PaymentOrderId

	// Wait for async saga to fail and mark payment as FAILED
	require.Eventually(t, func() bool {
		po, err := repo.FindByID(uuid.MustParse(paymentOrderID))
		if err != nil {
			return false
		}
		return po.Status == domain.PaymentOrderStatusFailed
	}, 2*time.Second, 50*time.Millisecond, "payment order should reach FAILED state")

	// Verify failure state
	po, err := repo.FindByID(uuid.MustParse(paymentOrderID))
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentOrderStatusFailed, po.Status)
	assert.Contains(t, po.FailureReason, "insufficient funds")
	assert.Equal(t, "SAGA_FAILED", po.ErrorCode)
	assert.Empty(t, po.LienID) // Lien was never created
	assert.NotNil(t, po.FailedAt)
}

// TestSagaOrchestration_GatewayFailure tests saga compensation when gateway submission fails.
func TestSagaOrchestration_GatewayFailure(t *testing.T) {
	repo := NewMockRepository()

	// Set up mock CurrentAccountClient with successful lien response
	caClient := &MockCurrentAccountClient{
		initiateLienResp: &currentaccountv1.InitiateLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId: "test-lien-789",
			},
		},
	}

	// Set up mock PaymentGateway to fail
	gwMock := &MockPaymentGateway{
		err: errGatewayUnavailable,
	}

	svc := &Service{
		repo:                 repo,
		logger:               slog.New(slog.NewJSONHandler(io.Discard, nil)),
		currentAccountClient: caClient,
		paymentGateway:       gwMock,
	}

	ctx := context.Background()
	req := newInitiateRequest("saga-gw-fail-key", "debtor-789", "creditor-ref", 5000)

	resp, err := svc.InitiatePaymentOrder(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)

	paymentOrderID := resp.PaymentOrder.PaymentOrderId

	// Wait for async saga to fail
	require.Eventually(t, func() bool {
		po, err := repo.FindByID(uuid.MustParse(paymentOrderID))
		if err != nil {
			return false
		}
		return po.Status == domain.PaymentOrderStatusFailed
	}, 2*time.Second, 50*time.Millisecond, "payment order should reach FAILED state")

	// Verify failure state
	po, err := repo.FindByID(uuid.MustParse(paymentOrderID))
	require.NoError(t, err)

	assert.Equal(t, domain.PaymentOrderStatusFailed, po.Status)
	assert.Contains(t, po.FailureReason, "gateway unavailable")
	assert.Equal(t, "SAGA_FAILED", po.ErrorCode)
	// Lien was created but should be released by compensation
	assert.Equal(t, "test-lien-789", po.LienID)
	assert.NotNil(t, po.FailedAt)
}
