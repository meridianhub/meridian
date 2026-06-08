// Package infra provides infrastructure components for market data import.
package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
)

const (
	// DefaultTimeout is the default timeout for gRPC calls.
	DefaultTimeout = 30 * time.Second

	// MetadataTenantID is the gRPC metadata key for tenant ID.
	MetadataTenantID = "x-tenant-id"
)

// GRPCClientConfig holds configuration for the gRPC client.
type GRPCClientConfig struct {
	// Endpoint is the gRPC server address (e.g., "localhost:9090").
	Endpoint string

	// TenantID is propagated via gRPC metadata.
	TenantID string

	// Timeout is the default timeout for RPC calls.
	Timeout time.Duration

	// DialOptions allows custom gRPC dial options.
	DialOptions []grpc.DialOption
}

// GRPCClient provides access to the Market Information Service.
type GRPCClient struct {
	conn     *grpc.ClientConn
	client   marketinformationv1.MarketInformationServiceClient
	tenantID string
	timeout  time.Duration
}

// Errors for gRPC client operations.
var (
	// ErrEndpointRequired is returned when endpoint is not configured.
	ErrEndpointRequired = errors.New("gRPC endpoint is required")
	// ErrTenantRequired is returned when tenant ID is not configured.
	ErrTenantRequired = errors.New("tenant ID is required")
	// ErrDataSourceNotFound is returned when a data source cannot be found.
	ErrDataSourceNotFound = errors.New("data source not found")
	// ErrDataSetNotFound is returned when a dataset cannot be found.
	ErrDataSetNotFound = errors.New("dataset not found")
)

// NewGRPCClient creates a new gRPC client for the Market Information Service.
// Returns the client, a cleanup function, and any error.
func NewGRPCClient(_ context.Context, cfg GRPCClientConfig) (*GRPCClient, func(), error) {
	if cfg.Endpoint == "" {
		return nil, nil, ErrEndpointRequired
	}
	if cfg.TenantID == "" {
		return nil, nil, ErrTenantRequired
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	dialOpts := cfg.DialOptions
	if dialOpts == nil {
		dialOpts = []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}
	}

	conn, err := grpc.NewClient(cfg.Endpoint, dialOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create grpc connection to %s: %w", cfg.Endpoint, err)
	}

	client := &GRPCClient{
		conn:     conn,
		client:   marketinformationv1.NewMarketInformationServiceClient(conn),
		tenantID: cfg.TenantID,
		timeout:  timeout,
	}

	cleanup := func() {
		if client.conn != nil {
			_ = client.conn.Close()
		}
	}

	return client, cleanup, nil
}

// contextWithTenant adds tenant ID to the context via gRPC metadata.
func (c *GRPCClient) contextWithTenant(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, MetadataTenantID, c.tenantID)
}

// DataSetDefinition represents a dataset definition from the service.
type DataSetDefinition struct {
	ID                      string
	Code                    string
	Version                 int32
	Category                string
	Unit                    string
	Status                  string
	DisplayName             string
	Description             string
	ResolutionKeyExpression string
	ValidationExpression    string
	ErrorMessageExpression  string
	AttributeSchema         *structpb.Struct
	AttributeSchemaJSON     string
}

// GetDataSet retrieves a dataset definition by code.
// If version is nil, returns the latest ACTIVE version.
func (c *GRPCClient) GetDataSet(ctx context.Context, code string, version *int32) (*DataSetDefinition, error) {
	ctx, cancel := context.WithTimeout(c.contextWithTenant(ctx), c.timeout)
	defer cancel()

	req := &marketinformationv1.RetrieveDataSetRequest{
		Code: code,
	}
	if version != nil {
		req.Version = *version
	}

	resp, err := c.client.RetrieveDataSet(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("retrieve dataset %s: %w", code, err)
	}

	if resp.Dataset == nil {
		return nil, fmt.Errorf("%w: %s", ErrDataSetNotFound, code)
	}

	return protoToDataSetDefinition(resp.Dataset), nil
}

// ResolveSourceID resolves a data source code to its UUID.
func (c *GRPCClient) ResolveSourceID(ctx context.Context, sourceCode string) (string, error) {
	ctx, cancel := context.WithTimeout(c.contextWithTenant(ctx), c.timeout)
	defer cancel()

	req := &marketinformationv1.ListDataSourcesRequest{
		ActiveOnly: true,
		PageSize:   100,
	}

	resp, err := c.client.ListDataSources(ctx, req)
	if err != nil {
		return "", fmt.Errorf("list data sources: %w", err)
	}

	for _, source := range resp.Sources {
		if source.Code == sourceCode {
			return source.Id, nil
		}
	}

	return "", fmt.Errorf("%w: %s", ErrDataSourceNotFound, sourceCode)
}

// RecordObservationBatch sends a batch of observations to the service.
func (c *GRPCClient) RecordObservationBatch(ctx context.Context, observations []*marketinformationv1.BatchObservationEntry) (*marketinformationv1.RecordObservationBatchResponse, error) {
	ctx, cancel := context.WithTimeout(c.contextWithTenant(ctx), c.timeout)
	defer cancel()

	req := &marketinformationv1.RecordObservationBatchRequest{
		Observations: observations,
	}

	return c.client.RecordObservationBatch(ctx, req)
}

// Close closes the gRPC connection.
func (c *GRPCClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// protoToDataSetDefinition converts a protobuf DataSetDefinition to our internal type.
func protoToDataSetDefinition(def *marketinformationv1.DataSetDefinition) *DataSetDefinition {
	if def == nil {
		return nil
	}

	result := &DataSetDefinition{
		ID:                      def.Id,
		Code:                    def.Code,
		Version:                 def.Version,
		Category:                def.Category.String(),
		Unit:                    def.Unit,
		Status:                  def.Status.String(),
		DisplayName:             def.DisplayName,
		Description:             def.Description,
		ResolutionKeyExpression: def.ResolutionKeyExpression,
		ValidationExpression:    def.ValidationExpression,
		ErrorMessageExpression:  def.ErrorMessageExpression,
		AttributeSchema:         def.AttributeSchema,
	}

	// Convert attribute schema to JSON string for validation
	if def.AttributeSchema != nil {
		if jsonBytes, err := json.Marshal(def.AttributeSchema.AsMap()); err == nil {
			result.AttributeSchemaJSON = string(jsonBytes)
		}
	}

	return result
}

// ObservationEntry represents an observation to be recorded.
type ObservationEntry struct {
	DatasetCode     string
	DatasetVersion  int32
	ObservedAt      time.Time
	ValidFrom       *time.Time
	ValidTo         *time.Time
	Value           string
	QualityLevel    string
	SourceCode      string
	Attributes      map[string]string
	ClientReference string
}

// ToProto converts an ObservationEntry to a protobuf BatchObservationEntry.
func (e *ObservationEntry) ToProto() *marketinformationv1.BatchObservationEntry {
	entry := &marketinformationv1.BatchObservationEntry{
		DatasetCode:     e.DatasetCode,
		DatasetVersion:  e.DatasetVersion,
		ObservedAt:      timestamppb.New(e.ObservedAt),
		Value:           e.Value,
		Quality:         qualityStringToProto(e.QualityLevel),
		SourceCode:      e.SourceCode,
		ClientReference: e.ClientReference,
	}

	if e.ValidFrom != nil {
		entry.ValidFrom = timestamppb.New(*e.ValidFrom)
	} else {
		// Default valid_from to observed_at
		entry.ValidFrom = timestamppb.New(e.ObservedAt)
	}

	if e.ValidTo != nil {
		entry.ValidTo = timestamppb.New(*e.ValidTo)
	}

	// Convert attributes map to proto format
	if len(e.Attributes) > 0 {
		entry.Attributes = attributesToProto(e.Attributes)
	}

	return entry
}

// attributesToProto converts a map of attributes to proto format.
// Keys are sorted for deterministic ordering.
func attributesToProto(attrs map[string]string) []*quantityv1.AttributeEntry {
	if len(attrs) == 0 {
		return nil
	}

	// Sort keys for deterministic ordering
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	entries := make([]*quantityv1.AttributeEntry, 0, len(attrs))
	for _, k := range keys {
		entries = append(entries, &quantityv1.AttributeEntry{
			Key:   k,
			Value: attrs[k],
		})
	}
	return entries
}

// qualityStringToProto converts a quality level string to the proto enum on
// Axis A (confidence) of the two-axis quality model (ADR-0017).
//
// Proto slot 4 is still spelled QUALITY_LEVEL_REVISED but its doc comment
// redefines it to VERIFIED confidence semantics (the rename to a dedicated
// QUALITY_LEVEL_VERIFIED symbol is pending). Both the canonical VERIFIED grade
// and the legacy REVISED label therefore map onto slot 4.
//
// Axis B (the revision counter) is server-assigned and is not carried on the
// write request, so it is intentionally absent from this mapping.
func qualityStringToProto(quality string) marketinformationv1.QualityLevel {
	switch quality {
	case "ESTIMATE":
		return marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE
	case "PROVISIONAL":
		return marketinformationv1.QualityLevel_QUALITY_LEVEL_PROVISIONAL
	case "ACTUAL":
		return marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL
	case "VERIFIED", "REVISED":
		// Slot 4: VERIFIED confidence (legacy REVISED label maps here too).
		return marketinformationv1.QualityLevel_QUALITY_LEVEL_REVISED
	default:
		return marketinformationv1.QualityLevel_QUALITY_LEVEL_UNSPECIFIED
	}
}
