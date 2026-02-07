// Package clients provides gRPC client adapters for external services.
package clients

import (
	"context"
	"errors"
	"fmt"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/meridianhub/meridian/services/payment-order/service"
	"google.golang.org/grpc"
)

var (
	// ErrSagaNotFound is returned when a saga definition is not found.
	ErrSagaNotFound = errors.New("saga not found")

	// ErrInstrumentNotFound is returned when an instrument is not found.
	ErrInstrumentNotFound = errors.New("instrument not found")
)

// ReferenceDataClientWrapper wraps the gRPC client for the reference-data service.
type ReferenceDataClientWrapper struct {
	conn             *grpc.ClientConn
	sagaClient       sagav1.SagaRegistryServiceClient
	instrumentClient referencedatav1.ReferenceDataServiceClient
}

// NewReferenceDataClient creates a new reference-data client wrapper.
func NewReferenceDataClient(conn *grpc.ClientConn) *ReferenceDataClientWrapper {
	return &ReferenceDataClientWrapper{
		conn:             conn,
		sagaClient:       sagav1.NewSagaRegistryServiceClient(conn),
		instrumentClient: referencedatav1.NewReferenceDataServiceClient(conn),
	}
}

// GetSaga fetches a saga definition by name and version from the reference-data service.
// If version is 0, returns the ACTIVE version for the current tenant.
func (c *ReferenceDataClientWrapper) GetSaga(ctx context.Context, name string, version int) (*service.SagaDefinition, error) {
	resp, err := c.sagaClient.GetSaga(ctx, &sagav1.GetSagaRequest{
		Name:    name,
		Version: int32(version),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get saga %s v%d: %w", name, version, err)
	}

	if resp.Saga == nil {
		return nil, fmt.Errorf("%w: %s v%d", ErrSagaNotFound, name, version)
	}

	// Use Script field directly - reference-data returns the full script content
	script := resp.Saga.Script

	return &service.SagaDefinition{
		ID:      resp.Saga.Id,
		Name:    resp.Saga.Name,
		Version: int(resp.Saga.Version),
		Script:  script,
		Status:  resp.Saga.Status.String(),
	}, nil
}

// RetrieveInstrument fetches an instrument definition by code from the reference-data service.
// Passes version=0 to retrieve the latest ACTIVE version.
func (c *ReferenceDataClientWrapper) RetrieveInstrument(ctx context.Context, code string) (*service.InstrumentInfo, error) {
	resp, err := c.instrumentClient.RetrieveInstrument(ctx, &referencedatav1.RetrieveInstrumentRequest{
		Code:    code,
		Version: 0, // 0 = latest ACTIVE version
	})
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve instrument %s: %w", code, err)
	}

	if resp.Instrument == nil {
		return nil, fmt.Errorf("%w: %s", ErrInstrumentNotFound, code)
	}

	return &service.InstrumentInfo{
		Code:                     resp.Instrument.Code,
		Version:                  resp.Instrument.Version,
		FungibilityKeyExpression: resp.Instrument.FungibilityKeyExpression,
	}, nil
}

// Close terminates the gRPC connection.
func (c *ReferenceDataClientWrapper) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
