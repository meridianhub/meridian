// Package client provides a gRPC client for the Market Information service
// with retry, circuit breaker, and context propagation support.
//
// The client provides high-level APIs for:
//   - Point-in-time rate queries (GetRate)
//   - Bi-temporal queries for audit replay (GetRateWithKnowledgeTime)
//   - Single observation ingestion (RecordObservation)
//   - Batch observation ingestion (RecordObservationBatch)
//   - Data set retrieval (GetDataSet)
//
// Usage with Kubernetes DNS-based discovery (recommended for production):
//
//	client, cleanup, err := client.New(ctx, client.Config{
//	    ServiceName: "market-information",
//	    Namespace:   "default",
//	    Port:        50051,
//	})
//	if err != nil {
//	    return err
//	}
//	defer cleanup()
//
//	// Get FX rate for USD/EUR as of a specific time
//	obs, err := client.GetRate(ctx, "USD_EUR_FX", "spot", asOfTime, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)
//
// Usage with direct address (development):
//
//	client, cleanup, err := client.New(ctx, client.Config{
//	    Target: "localhost:50051",
//	})
package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
)

const (
	// DefaultPort is the default gRPC port for the Market Information service.
	DefaultPort = ports.MarketInformation

	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// DefaultNamespace is the default Kubernetes namespace.
	DefaultNamespace = "default"

	// ServiceName is the Kubernetes service name for Market Information.
	ServiceName = "market-information"
)

// Config holds configuration for the Market Information client.
type Config struct {
	// Target is the gRPC server address (e.g., "localhost:50051").
	// If set, overrides Kubernetes DNS-based discovery.
	//
	// Deprecated: Use ServiceName, Namespace, and Port for DNS-based load balancing.
	Target string

	// ServiceName is the Kubernetes service name (e.g., "market-information").
	// When specified, enables DNS-based client-side load balancing.
	ServiceName string

	// Namespace is the Kubernetes namespace (e.g., "default", "production").
	// Defaults to "default" if not specified.
	Namespace string

	// Port is the service port number.
	// Defaults to 50051 if not specified.
	Port int

	// Timeout is the default timeout for RPC calls.
	// Defaults to 30 seconds if not specified.
	Timeout time.Duration

	// Tracer is an optional observability tracer for distributed tracing.
	Tracer *observability.Tracer

	// Resilience is an optional configuration for circuit breaker and retry.
	Resilience *clients.ResilientClientConfig

	// DialOptions allows custom gRPC dial options.
	// When using Target (direct connection), if DialOptions is nil, insecure credentials
	// are added by default. If DialOptions is provided, the caller must include
	// appropriate transport credentials (e.g., grpc.WithTransportCredentials).
	// When using ServiceName (Kubernetes DNS), credentials are handled by the platform factory.
	DialOptions []grpc.DialOption
}

// ErrTargetRequired is returned when neither Target nor ServiceName is provided.
var ErrTargetRequired = clients.ErrConnTargetRequired

// ErrObservationNotFound is returned when no observation matches the query criteria.
var ErrObservationNotFound = errors.New("observation not found")

// ErrNilRequest is returned when a nil request is passed to a method.
var ErrNilRequest = errors.New("request cannot be nil")

// ErrEmptyObservations is returned when an empty observations slice is passed to batch methods.
var ErrEmptyObservations = errors.New("observations cannot be empty")

// Client provides access to the Market Information service.
type Client struct {
	conn       *grpc.ClientConn
	grpcClient marketinformationv1.MarketInformationServiceClient
	tracer     *observability.Tracer
	resilient  *clients.ResilientClient
	timeout    time.Duration
}

// applyDefaults sets default values for unspecified configuration fields.
func (cfg *Config) applyDefaults() {
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Namespace == "" {
		cfg.Namespace = DefaultNamespace
	}
}

// New creates a new Market Information gRPC client.
//
// Returns the client, a cleanup function to close all resources, and any error.
// The cleanup function should be deferred immediately after checking the error.
func New(ctx context.Context, cfg Config) (*Client, func() error, error) {
	cfg.applyDefaults()

	conn, _, err := clients.NewConn(ctx, clients.ConnConfig{
		Target:      cfg.Target,
		ServiceName: cfg.ServiceName,
		Namespace:   cfg.Namespace,
		Port:        cfg.Port,
		Tracer:      cfg.Tracer,
		DialOptions: cfg.DialOptions,
	})
	if err != nil {
		return nil, nil, err
	}

	var resilient *clients.ResilientClient
	if cfg.Resilience != nil {
		resilient = clients.NewResilientClient(*cfg.Resilience)
	}

	client := &Client{
		conn:       conn,
		grpcClient: marketinformationv1.NewMarketInformationServiceClient(conn),
		tracer:     cfg.Tracer,
		resilient:  resilient,
		timeout:    cfg.Timeout,
	}

	return client, client.Close, nil
}

// GetRate retrieves a market rate observation for a specific dataset and resolution key
// at a point in time with minimum quality threshold.
//
// This is the primary method for point-in-time rate lookups such as FX rates,
// commodity prices, or interest rates.
//
// Example - Get USD/EUR FX rate:
//
//	obs, err := client.GetRate(ctx, "USD_EUR_FX", "spot", asOfTime,
//	    marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)
//	if err != nil {
//	    return err
//	}
//	fmt.Printf("Rate: %s\n", obs.Value)
//
// Example - Get energy tariff:
//
//	obs, err := client.GetRate(ctx, "ELEC_TARIFF", "peak",
//	    time.Now(), marketinformationv1.QualityLevel_QUALITY_LEVEL_PROVISIONAL)
func (c *Client) GetRate(
	ctx context.Context,
	datasetCode string,
	resolutionKey string,
	asOf time.Time,
	minQuality marketinformationv1.QualityLevel,
) (*marketinformationv1.MarketPriceObservation, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	// ListObservations returns observations ordered by valid_from DESC by default,
	// so PageSize=1 retrieves the most recent matching observation
	req := &marketinformationv1.ListObservationsRequest{
		DatasetCode:        datasetCode,
		ResolutionKeyValue: resolutionKey,
		ValidAt:            timestamppb.New(asOf),
		QualityFilter:      minQuality,
		PageSize:           1,
	}

	if c.resilient != nil {
		resp, err := clients.ExecuteWithResilience(ctx, c.resilient, "GetRate", func() (*marketinformationv1.ListObservationsResponse, error) {
			return c.grpcClient.ListObservations(ctx, req)
		})
		if err != nil {
			return nil, fmt.Errorf("get rate: %w", err)
		}
		if len(resp.Observations) == 0 {
			return nil, fmt.Errorf("get rate %s/%s at %v with quality >= %s: %w",
				datasetCode, resolutionKey, asOf, minQuality, ErrObservationNotFound)
		}
		return resp.Observations[0], nil
	}

	resp, err := c.grpcClient.ListObservations(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get rate: %w", err)
	}
	if len(resp.Observations) == 0 {
		return nil, fmt.Errorf("get rate %s/%s at %v with quality >= %s: %w",
			datasetCode, resolutionKey, asOf, minQuality, ErrObservationNotFound)
	}
	return resp.Observations[0], nil
}

// GetRateWithKnowledgeTime retrieves a market rate observation using bi-temporal query.
// This is used for audit replay scenarios where you need to know what value was known
// at a specific point in time (knowledge time) for a given valid time (asOf).
//
// Example - Audit replay: What FX rate did we know on Dec 1 for trades on Nov 15?
//
//	knowledgeTime := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)
//	asOf := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
//	obs, err := client.GetRateWithKnowledgeTime(ctx, "USD_EUR_FX", "spot",
//	    asOf, knowledgeTime, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL)
func (c *Client) GetRateWithKnowledgeTime(
	ctx context.Context,
	datasetCode string,
	resolutionKey string,
	asOf time.Time,
	knowledgeBaseTime time.Time,
	minQuality marketinformationv1.QualityLevel,
) (*marketinformationv1.MarketPriceObservation, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	req := &marketinformationv1.ListObservationsRequest{
		DatasetCode:        datasetCode,
		ResolutionKeyValue: resolutionKey,
		ValidAt:            timestamppb.New(asOf),
		KnowledgeBaseTime:  timestamppb.New(knowledgeBaseTime),
		QualityFilter:      minQuality,
		PageSize:           1,
	}

	if c.resilient != nil {
		resp, err := clients.ExecuteWithResilience(ctx, c.resilient, "GetRateWithKnowledgeTime", func() (*marketinformationv1.ListObservationsResponse, error) {
			return c.grpcClient.ListObservations(ctx, req)
		})
		if err != nil {
			return nil, fmt.Errorf("get rate with knowledge time: %w", err)
		}
		if len(resp.Observations) == 0 {
			return nil, fmt.Errorf("get rate with knowledge time %s/%s at %v (known at %v) with quality >= %s: %w",
				datasetCode, resolutionKey, asOf, knowledgeBaseTime, minQuality, ErrObservationNotFound)
		}
		return resp.Observations[0], nil
	}

	resp, err := c.grpcClient.ListObservations(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get rate with knowledge time: %w", err)
	}
	if len(resp.Observations) == 0 {
		return nil, fmt.Errorf("get rate with knowledge time %s/%s at %v (known at %v) with quality >= %s: %w",
			datasetCode, resolutionKey, asOf, knowledgeBaseTime, minQuality, ErrObservationNotFound)
	}
	return resp.Observations[0], nil
}

// RecordObservation records a single market data observation.
//
// Example - Record FX rate observation:
//
//	resp, err := client.RecordObservation(ctx, &marketinformationv1.RecordObservationRequest{
//	    DatasetCode: "USD_EUR_FX",
//	    ObservedAt:  timestamppb.Now(),
//	    ValidFrom:   timestamppb.Now(),
//	    Value:       "1.0856",
//	    Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
//	    SourceCode:  "BLOOMBERG",
//	})
func (c *Client) RecordObservation(
	ctx context.Context,
	req *marketinformationv1.RecordObservationRequest,
) (*marketinformationv1.RecordObservationResponse, error) {
	if req == nil {
		return nil, ErrNilRequest
	}

	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		// Use no-retry for mutations to avoid duplicates
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "RecordObservation", func() (*marketinformationv1.RecordObservationResponse, error) {
			return c.grpcClient.RecordObservation(ctx, req)
		})
	}

	return c.grpcClient.RecordObservation(ctx, req)
}

// RecordObservationBatch records multiple market data observations in a single call.
// Returns a BatchObservationResult with success/failure details for each observation.
//
// Example - Batch ingest energy tariffs:
//
//	entries := []*marketinformationv1.BatchObservationEntry{
//	    {
//	        DatasetCode:     "ELEC_TARIFF",
//	        ObservedAt:      timestamppb.Now(),
//	        ValidFrom:       timestamppb.New(startOfDay),
//	        Value:           "0.15",
//	        Quality:         marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
//	        SourceCode:      "GRID_OPERATOR",
//	        ClientReference: "tariff-001",
//	    },
//	    // ... more entries
//	}
//	resp, err := client.RecordObservationBatch(ctx, entries)
//	if err != nil {
//	    return err
//	}
//	fmt.Printf("Recorded %d/%d observations\n", resp.SuccessCount, resp.TotalCount)
func (c *Client) RecordObservationBatch(
	ctx context.Context,
	observations []*marketinformationv1.BatchObservationEntry,
) (*marketinformationv1.RecordObservationBatchResponse, error) {
	if len(observations) == 0 {
		return nil, ErrEmptyObservations
	}

	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	req := &marketinformationv1.RecordObservationBatchRequest{
		Observations: observations,
	}

	if c.resilient != nil {
		// Use no-retry for mutations to avoid duplicates
		return clients.ExecuteWithResilienceNoRetry(ctx, c.resilient, "RecordObservationBatch", func() (*marketinformationv1.RecordObservationBatchResponse, error) {
			return c.grpcClient.RecordObservationBatch(ctx, req)
		})
	}

	return c.grpcClient.RecordObservationBatch(ctx, req)
}

// GetDataSet retrieves a data set definition by code.
// If version is nil, returns the latest ACTIVE version.
//
// Example - Get FX rate dataset definition:
//
//	dataset, err := client.GetDataSet(ctx, "USD_EUR_FX", nil)
//	if err != nil {
//	    return err
//	}
//	fmt.Printf("Dataset: %s (version %d)\n", dataset.Code, dataset.Version)
func (c *Client) GetDataSet(
	ctx context.Context,
	code string,
	version *int32,
) (*marketinformationv1.DataSetDefinition, error) {
	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	req := &marketinformationv1.RetrieveDataSetRequest{
		Code: code,
	}
	if version != nil {
		req.Version = *version
	}

	if c.resilient != nil {
		resp, err := clients.ExecuteWithResilience(ctx, c.resilient, "GetDataSet", func() (*marketinformationv1.RetrieveDataSetResponse, error) {
			return c.grpcClient.RetrieveDataSet(ctx, req)
		})
		if err != nil {
			return nil, fmt.Errorf("get dataset: %w", err)
		}
		return resp.Dataset, nil
	}

	resp, err := c.grpcClient.RetrieveDataSet(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get dataset: %w", err)
	}
	return resp.Dataset, nil
}

// ListObservations returns observations matching the filter criteria.
// This is a lower-level method for complex queries. For simple rate lookups,
// prefer GetRate or GetRateWithKnowledgeTime.
func (c *Client) ListObservations(
	ctx context.Context,
	req *marketinformationv1.ListObservationsRequest,
) (*marketinformationv1.ListObservationsResponse, error) {
	if req == nil {
		return nil, ErrNilRequest
	}

	ctx, cancel := clients.WithTimeout(ctx, c.timeout)
	defer cancel()

	ctx = clients.PropagateCorrelationID(ctx)
	ctx = clients.PropagateOrganization(ctx)

	if c.resilient != nil {
		return clients.ExecuteWithResilience(ctx, c.resilient, "ListObservations", func() (*marketinformationv1.ListObservationsResponse, error) {
			return c.grpcClient.ListObservations(ctx, req)
		})
	}

	return c.grpcClient.ListObservations(ctx, req)
}

// Close releases the gRPC connection.
func (c *Client) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("close grpc: %w", err)
		}
	}
	return nil
}

// Conn returns the underlying gRPC connection.
func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}
