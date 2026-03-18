package client

import (
	"context"
	"testing"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// extendedMockClient extends mockFinancialAccountingClient with controllable
// implementations for methods not covered by the existing mock.
type extendedMockClient struct {
	mockFinancialAccountingClient
	ListFinancialBookingLogsFunc func(ctx context.Context, req *financialaccountingv1.ListFinancialBookingLogsRequest, opts ...grpc.CallOption) (*financialaccountingv1.ListFinancialBookingLogsResponse, error)
	RetrieveLedgerPostingFunc    func(ctx context.Context, req *financialaccountingv1.RetrieveLedgerPostingRequest, opts ...grpc.CallOption) (*financialaccountingv1.RetrieveLedgerPostingResponse, error)
	ListLedgerPostingsFunc       func(ctx context.Context, req *financialaccountingv1.ListLedgerPostingsRequest, opts ...grpc.CallOption) (*financialaccountingv1.ListLedgerPostingsResponse, error)
}

func (m *extendedMockClient) ListFinancialBookingLogs(ctx context.Context, req *financialaccountingv1.ListFinancialBookingLogsRequest, opts ...grpc.CallOption) (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
	if m.ListFinancialBookingLogsFunc != nil {
		return m.ListFinancialBookingLogsFunc(ctx, req, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *extendedMockClient) RetrieveLedgerPosting(ctx context.Context, req *financialaccountingv1.RetrieveLedgerPostingRequest, opts ...grpc.CallOption) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
	if m.RetrieveLedgerPostingFunc != nil {
		return m.RetrieveLedgerPostingFunc(ctx, req, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *extendedMockClient) ListLedgerPostings(ctx context.Context, req *financialaccountingv1.ListLedgerPostingsRequest, opts ...grpc.CallOption) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
	if m.ListLedgerPostingsFunc != nil {
		return m.ListLedgerPostingsFunc(ctx, req, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

// newClientWithMock creates a Client struct with an injected mock for unit testing.
func newClientWithExtendedMock(mock financialaccountingv1.FinancialAccountingServiceClient) *Client {
	return &Client{
		financialAccounting: mock,
		timeout:             DefaultTimeout,
	}
}

func TestListFinancialBookingLogs_Success(t *testing.T) {
	expected := &financialaccountingv1.ListFinancialBookingLogsResponse{}
	mock := &extendedMockClient{
		ListFinancialBookingLogsFunc: func(_ context.Context, _ *financialaccountingv1.ListFinancialBookingLogsRequest, _ ...grpc.CallOption) (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
			return expected, nil
		},
	}
	c := newClientWithExtendedMock(mock)

	resp, err := c.ListFinancialBookingLogs(context.Background(), &financialaccountingv1.ListFinancialBookingLogsRequest{})
	require.NoError(t, err)
	assert.Equal(t, expected, resp)
}

func TestListFinancialBookingLogs_Error(t *testing.T) {
	mock := &extendedMockClient{
		ListFinancialBookingLogsFunc: func(_ context.Context, _ *financialaccountingv1.ListFinancialBookingLogsRequest, _ ...grpc.CallOption) (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
			return nil, status.Error(codes.Internal, "db error")
		},
	}
	c := newClientWithExtendedMock(mock)

	_, err := c.ListFinancialBookingLogs(context.Background(), &financialaccountingv1.ListFinancialBookingLogsRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list financial booking logs")
}

func TestRetrieveLedgerPosting_Success(t *testing.T) {
	postingID := "posting-123"
	expected := &financialaccountingv1.RetrieveLedgerPostingResponse{}
	mock := &extendedMockClient{
		RetrieveLedgerPostingFunc: func(_ context.Context, req *financialaccountingv1.RetrieveLedgerPostingRequest, _ ...grpc.CallOption) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
			assert.Equal(t, postingID, req.GetId())
			return expected, nil
		},
	}
	c := newClientWithExtendedMock(mock)

	resp, err := c.RetrieveLedgerPosting(context.Background(), &financialaccountingv1.RetrieveLedgerPostingRequest{
		Id: postingID,
	})
	require.NoError(t, err)
	assert.Equal(t, expected, resp)
}

func TestRetrieveLedgerPosting_Error(t *testing.T) {
	mock := &extendedMockClient{
		RetrieveLedgerPostingFunc: func(_ context.Context, _ *financialaccountingv1.RetrieveLedgerPostingRequest, _ ...grpc.CallOption) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	c := newClientWithExtendedMock(mock)

	_, err := c.RetrieveLedgerPosting(context.Background(), &financialaccountingv1.RetrieveLedgerPostingRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retrieve ledger posting")
}

func TestListLedgerPostings_Success(t *testing.T) {
	expected := &financialaccountingv1.ListLedgerPostingsResponse{}
	mock := &extendedMockClient{
		ListLedgerPostingsFunc: func(_ context.Context, _ *financialaccountingv1.ListLedgerPostingsRequest, _ ...grpc.CallOption) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
			return expected, nil
		},
	}
	c := newClientWithExtendedMock(mock)

	resp, err := c.ListLedgerPostings(context.Background(), &financialaccountingv1.ListLedgerPostingsRequest{})
	require.NoError(t, err)
	assert.Equal(t, expected, resp)
}

func TestListLedgerPostings_Error(t *testing.T) {
	mock := &extendedMockClient{
		ListLedgerPostingsFunc: func(_ context.Context, _ *financialaccountingv1.ListLedgerPostingsRequest, _ ...grpc.CallOption) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
			return nil, status.Error(codes.Unavailable, "service unavailable")
		},
	}
	c := newClientWithExtendedMock(mock)

	_, err := c.ListLedgerPostings(context.Background(), &financialaccountingv1.ListLedgerPostingsRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list ledger postings")
}

func TestRetrieveFinancialBookingLog_Success(t *testing.T) {
	expected := &financialaccountingv1.RetrieveFinancialBookingLogResponse{}
	c := &Client{
		financialAccounting: &retrieveFinancialBookingLogMock{
			fn: func(_ context.Context, _ *financialaccountingv1.RetrieveFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
				return expected, nil
			},
		},
		timeout: DefaultTimeout,
	}

	resp, err := c.RetrieveFinancialBookingLog(context.Background(), &financialaccountingv1.RetrieveFinancialBookingLogRequest{
		Id: "booking-123",
	})
	require.NoError(t, err)
	assert.Equal(t, expected, resp)
}

func TestRetrieveFinancialBookingLog_Error(t *testing.T) {
	c := &Client{
		financialAccounting: &retrieveFinancialBookingLogMock{
			fn: func(_ context.Context, _ *financialaccountingv1.RetrieveFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
				return nil, status.Error(codes.NotFound, "not found")
			},
		},
		timeout: DefaultTimeout,
	}

	_, err := c.RetrieveFinancialBookingLog(context.Background(), &financialaccountingv1.RetrieveFinancialBookingLogRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retrieve financial booking log")
}

// retrieveFinancialBookingLogMock wraps extendedMockClient and overrides RetrieveFinancialBookingLog.
type retrieveFinancialBookingLogMock struct {
	extendedMockClient
	fn func(ctx context.Context, req *financialaccountingv1.RetrieveFinancialBookingLogRequest, opts ...grpc.CallOption) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error)
}

func (m *retrieveFinancialBookingLogMock) RetrieveFinancialBookingLog(ctx context.Context, req *financialaccountingv1.RetrieveFinancialBookingLogRequest, opts ...grpc.CallOption) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
	if m.fn != nil {
		return m.fn(ctx, req, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestConn_ReturnsConnection(t *testing.T) {
	c, cleanup, err := New(Config{Target: "localhost:50052"})
	require.NoError(t, err)
	defer cleanup()

	conn := c.Conn()
	assert.NotNil(t, conn)
	assert.Equal(t, c.conn, conn)
}

func TestConn_NilClient(t *testing.T) {
	c := &Client{}
	assert.Nil(t, c.Conn())
}

func TestUpdateFinancialBookingLog_Success(t *testing.T) {
	expected := &financialaccountingv1.UpdateFinancialBookingLogResponse{}
	mock := &extendedMockClient{
		mockFinancialAccountingClient: mockFinancialAccountingClient{
			UpdateFinancialBookingLogFunc: func(_ context.Context, _ *financialaccountingv1.UpdateFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
				return expected, nil
			},
		},
	}
	c := newClientWithExtendedMock(mock)

	resp, err := c.UpdateFinancialBookingLog(context.Background(), &financialaccountingv1.UpdateFinancialBookingLogRequest{})
	require.NoError(t, err)
	assert.Equal(t, expected, resp)
}

func TestUpdateFinancialBookingLog_Error(t *testing.T) {
	mock := &extendedMockClient{
		mockFinancialAccountingClient: mockFinancialAccountingClient{
			UpdateFinancialBookingLogFunc: func(_ context.Context, _ *financialaccountingv1.UpdateFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
				return nil, status.Error(codes.Internal, "update failed")
			},
		},
	}
	c := newClientWithExtendedMock(mock)

	_, err := c.UpdateFinancialBookingLog(context.Background(), &financialaccountingv1.UpdateFinancialBookingLogRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update financial booking log")
}
