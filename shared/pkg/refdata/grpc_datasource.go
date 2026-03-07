package refdata

import (
	"context"
	"fmt"
	"strings"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GRPCDataSource implements DataSource by calling the Reference Data gRPC service.
type GRPCDataSource struct {
	client referencedatav1.ReferenceDataServiceClient
}

// NewGRPCDataSource creates a DataSource backed by the Reference Data gRPC service.
func NewGRPCDataSource(client referencedatav1.ReferenceDataServiceClient) *GRPCDataSource {
	return &GRPCDataSource{client: client}
}

// FetchInstrument retrieves properties for a single instrument code from Reference Data.
func (s *GRPCDataSource) FetchInstrument(ctx context.Context, code string) (InstrumentProperties, error) {
	resp, err := s.client.RetrieveInstrument(ctx, &referencedatav1.RetrieveInstrumentRequest{
		Code:    code,
		Version: 0, // Latest active version
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return InstrumentProperties{}, ErrUnknownInstrument
		}
		return InstrumentProperties{}, fmt.Errorf("retrieve instrument %q: %w", code, err)
	}

	return protoToProperties(resp.Instrument), nil
}

// FetchAllActive retrieves all active instrument properties from Reference Data.
func (s *GRPCDataSource) FetchAllActive(ctx context.Context) ([]InstrumentProperties, error) {
	var all []InstrumentProperties
	var pageToken string

	for {
		resp, err := s.client.ListInstruments(ctx, &referencedatav1.ListInstrumentsRequest{
			StatusFilter: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			PageSize:     100,
			PageToken:    pageToken,
		})
		if err != nil {
			return nil, fmt.Errorf("list active instruments: %w", err)
		}

		for _, inst := range resp.Instruments {
			all = append(all, protoToProperties(inst))
		}

		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	return all, nil
}

// protoToProperties converts a proto InstrumentDefinition to InstrumentProperties.
func protoToProperties(inst *referencedatav1.InstrumentDefinition) InstrumentProperties {
	return InstrumentProperties{
		Code:         inst.Code,
		Dimension:    dimensionToString(inst.Dimension),
		Precision:    int(inst.Precision),
		RoundingMode: DefaultRoundingMode,
	}
}

// dimensionToString converts a proto Dimension enum to its string representation.
// The string format strips the "DIMENSION_" prefix to match the domain convention
// (e.g., DIMENSION_CURRENCY -> "CURRENCY", DIMENSION_ENERGY -> "ENERGY").
func dimensionToString(d referencedatav1.Dimension) string {
	name := d.String()
	return strings.TrimPrefix(name, "DIMENSION_")
}
