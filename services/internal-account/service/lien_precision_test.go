package service

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// instrumentMap allows per-code instrument responses in tests.
type instrumentMap struct {
	instruments map[string]*referencedatav1.InstrumentDefinition
	defaultErr  error
}

func (m *instrumentMap) RetrieveInstrument(_ context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
	if m.defaultErr != nil {
		return nil, m.defaultErr
	}
	inst, ok := m.instruments[req.Code]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "instrument not found: %s", req.Code)
	}
	return &referencedatav1.RetrieveInstrumentResponse{Instrument: inst}, nil
}

func (m *instrumentMap) Close() error { return nil }

func newInstrumentMap(entries map[string]int32) *instrumentMap {
	instruments := make(map[string]*referencedatav1.InstrumentDefinition, len(entries))
	for code, precision := range entries {
		instruments[code] = &referencedatav1.InstrumentDefinition{
			Code:      code,
			Precision: precision,
			Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		}
	}
	return &instrumentMap{instruments: instruments}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

// --- getInstrumentPrecision tests ---

func TestGetInstrumentPrecision_WithoutClient_FailsClosed(t *testing.T) {
	svc := &Service{logger: testLogger()}

	_, err := svc.getInstrumentPrecision(context.Background(), "GBP")

	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestGetInstrumentPrecision_StandardCurrency(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{"GBP": 2})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	precision, err := svc.getInstrumentPrecision(context.Background(), "GBP")

	require.NoError(t, err)
	assert.Equal(t, int32(2), precision)
}

func TestGetInstrumentPrecision_ZeroPrecision_JPY(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{"JPY": 0})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	precision, err := svc.getInstrumentPrecision(context.Background(), "JPY")

	require.NoError(t, err)
	assert.Equal(t, int32(0), precision)
}

func TestGetInstrumentPrecision_ThreeDecimalPlaces_BHD(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{"BHD": 3})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	precision, err := svc.getInstrumentPrecision(context.Background(), "BHD")

	require.NoError(t, err)
	assert.Equal(t, int32(3), precision)
}

func TestGetInstrumentPrecision_HighPrecision_Crypto(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{"BTC": 8})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	precision, err := svc.getInstrumentPrecision(context.Background(), "BTC")

	require.NoError(t, err)
	assert.Equal(t, int32(8), precision)
}

func TestGetInstrumentPrecision_NotFound(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	_, err := svc.getInstrumentPrecision(context.Background(), "UNKNOWN")

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestGetInstrumentPrecision_ClientError(t *testing.T) {
	refClient := &instrumentMap{
		defaultErr: status.Error(codes.Unavailable, "service down"),
	}
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	_, err := svc.getInstrumentPrecision(context.Background(), "GBP")

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestGetInstrumentPrecision_NilInstrumentInResponse(t *testing.T) {
	// Mock that returns a response with nil instrument
	refClient := &instrumentMap{
		instruments: map[string]*referencedatav1.InstrumentDefinition{
			"BAD": nil,
		},
	}
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	_, err := svc.getInstrumentPrecision(context.Background(), "BAD")

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// --- toMinorUnits / toMajorUnits tests ---

func TestToMinorUnits(t *testing.T) {
	tests := []struct {
		name      string
		amount    string
		precision int32
		expected  int64
	}{
		{"GBP 100.50 precision 2", "100.50", 2, 10050},
		{"JPY 1000 precision 0", "1000", 0, 1000},
		{"BHD 100.125 precision 3", "100.125", 3, 100125},
		{"BTC 0.00000001 precision 8", "0.00000001", 8, 1},
		{"GBP 0.01 precision 2", "0.01", 2, 1},
		{"large GBP precision 2", "999999.99", 2, 99999999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, _ := decimal.NewFromString(tt.amount)
			result := toMinorUnits(amount, tt.precision)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToMajorUnits(t *testing.T) {
	tests := []struct {
		name        string
		amountCents int64
		precision   int32
		expected    string
	}{
		{"GBP 10050 precision 2", 10050, 2, "100.5"},
		{"JPY 1000 precision 0", 1000, 0, "1000"},
		{"BHD 100125 precision 3", 100125, 3, "100.125"},
		{"BTC 1 satoshi precision 8", 1, 8, "0.00000001"},
		{"GBP 1 penny precision 2", 1, 2, "0.01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toMajorUnits(tt.amountCents, tt.precision)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// --- domainToProtoLien precision tests ---

func TestDomainToProtoLien_StandardPrecision(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{"GBP": 2})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10050,
		InstrumentCode:        "GBP",
		Status:                domain.LienStatusActive,
		PaymentOrderReference: "PO-001",
		Version:               1,
	}

	protoLien, err := svc.domainToProtoLien(context.Background(), lien)

	require.NoError(t, err)
	assert.Equal(t, "100.5", protoLien.Amount.Amount)
	assert.Equal(t, "GBP", protoLien.Amount.InstrumentCode)
}

func TestDomainToProtoLien_ZeroPrecision_JPY(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{"JPY": 0})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10000,
		InstrumentCode:        "JPY",
		Status:                domain.LienStatusActive,
		PaymentOrderReference: "PO-002",
		Version:               1,
	}

	protoLien, err := svc.domainToProtoLien(context.Background(), lien)

	require.NoError(t, err)
	// With precision 0, 10000 minor units = 10000 major units (no shift)
	assert.Equal(t, "10000", protoLien.Amount.Amount)
}

func TestDomainToProtoLien_ThreeDecimalPlaces_BHD(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{"BHD": 3})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           100125,
		InstrumentCode:        "BHD",
		Status:                domain.LienStatusActive,
		PaymentOrderReference: "PO-003",
		Version:               1,
	}

	protoLien, err := svc.domainToProtoLien(context.Background(), lien)

	require.NoError(t, err)
	assert.Equal(t, "100.125", protoLien.Amount.Amount)
}

func TestDomainToProtoLien_HighPrecision_BTC(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{"BTC": 8})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           100000000, // 1 BTC in satoshis
		InstrumentCode:        "BTC",
		Status:                domain.LienStatusActive,
		PaymentOrderReference: "PO-004",
		Version:               1,
	}

	protoLien, err := svc.domainToProtoLien(context.Background(), lien)

	require.NoError(t, err)
	assert.Equal(t, "1", protoLien.Amount.Amount)
}

func TestDomainToProtoLien_ErrorOnClientFailure(t *testing.T) {
	refClient := &instrumentMap{
		defaultErr: status.Error(codes.Unavailable, "service down"),
	}
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10050,
		InstrumentCode:        "GBP",
		Status:                domain.LienStatusActive,
		PaymentOrderReference: "PO-005",
		Version:               1,
	}

	_, err := svc.domainToProtoLien(context.Background(), lien)

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestDomainToProtoLien_ErrorWithoutClient(t *testing.T) {
	svc := &Service{logger: testLogger()}

	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10050,
		InstrumentCode:        "GBP",
		Status:                domain.LienStatusActive,
		PaymentOrderReference: "PO-006",
		Version:               1,
	}

	_, err := svc.domainToProtoLien(context.Background(), lien)

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// --- Precision truncation validation tests ---

func TestPrecisionValidation_ExcessDecimalPlaces(t *testing.T) {
	tests := []struct {
		name      string
		amount    string
		precision int32
		valid     bool
	}{
		{"GBP exact 2dp", "100.50", 2, true},
		{"GBP whole amount", "100", 2, true},
		{"GBP excess 3dp", "100.555", 2, false},
		{"JPY exact 0dp", "1000", 0, true},
		{"JPY has decimals", "1000.5", 0, false},
		{"BHD exact 3dp", "100.125", 3, true},
		{"BHD excess 4dp", "100.1234", 3, false},
		{"BTC exact 8dp", "1.23456789", 8, true},
		{"BTC excess 9dp", "1.234567891", 8, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, _ := decimal.NewFromString(tt.amount)
			isValid := amount.Equal(amount.Truncate(tt.precision))
			assert.Equal(t, tt.valid, isValid)
		})
	}
}

// --- Roundtrip tests: major -> minor -> major ---

func TestPrecisionRoundtrip(t *testing.T) {
	tests := []struct {
		name      string
		amount    string
		precision int32
	}{
		{"GBP standard", "100.50", 2},
		{"JPY zero precision", "1000", 0},
		{"BHD three decimals", "100.125", 3},
		{"BTC eight decimals", "1.23456789", 8},
		{"small amount precision 2", "0.01", 2},
		{"large amount precision 2", "999999.99", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original, _ := decimal.NewFromString(tt.amount)
			minor := toMinorUnits(original, tt.precision)
			major := toMajorUnits(minor, tt.precision)

			// Parse back and compare
			roundtripped, _ := decimal.NewFromString(major)
			assert.True(t, original.Equal(roundtripped),
				"roundtrip failed: %s -> %d -> %s (expected %s)", tt.amount, minor, major, tt.amount)
		})
	}
}

// --- Non-currency instrument precision tests (Task 8.3) ---

func TestGetInstrumentPrecision_NonCurrencyInstrument_KWH(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{"KWH": 6})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	precision, err := svc.getInstrumentPrecision(context.Background(), "KWH")

	require.NoError(t, err)
	assert.Equal(t, int32(6), precision)
}

func TestGetInstrumentPrecision_NonCurrencyInstrument_TONNE_CO2E(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{"TONNE_CO2E": 4})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	precision, err := svc.getInstrumentPrecision(context.Background(), "TONNE_CO2E")

	require.NoError(t, err)
	assert.Equal(t, int32(4), precision)
}

func TestGetInstrumentPrecision_NonCurrencyInstrument_GPU_HOUR(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{"GPU_HOUR": 3})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	precision, err := svc.getInstrumentPrecision(context.Background(), "GPU_HOUR")

	require.NoError(t, err)
	assert.Equal(t, int32(3), precision)
}

func TestDomainToProtoLien_NonCurrencyInstrument_KWH(t *testing.T) {
	refClient := newInstrumentMap(map[string]int32{"KWH": 6})
	svc := &Service{referenceDataClient: refClient, logger: testLogger()}

	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           123456789, // 123.456789 KWH
		InstrumentCode:        "KWH",
		Status:                domain.LienStatusActive,
		PaymentOrderReference: "PO-KWH-001",
		Version:               1,
	}

	protoLien, err := svc.domainToProtoLien(context.Background(), lien)

	require.NoError(t, err)
	assert.Equal(t, "123.456789", protoLien.Amount.Amount)
	assert.Equal(t, "KWH", protoLien.Amount.InstrumentCode)
}

func TestPrecisionRoundtrip_NonCurrencyInstruments(t *testing.T) {
	tests := []struct {
		name      string
		amount    string
		precision int32
	}{
		{"KWH six decimals", "123.456789", 6},
		{"TONNE_CO2E four decimals", "50.1234", 4},
		{"GPU_HOUR three decimals", "1000.125", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original, _ := decimal.NewFromString(tt.amount)
			minor := toMinorUnits(original, tt.precision)
			major := toMajorUnits(minor, tt.precision)

			roundtripped, _ := decimal.NewFromString(major)
			assert.True(t, original.Equal(roundtripped),
				"roundtrip failed: %s -> %d -> %s (expected %s)", tt.amount, minor, major, tt.amount)
		})
	}
}

func TestFailClosed_NilReferenceDataClient_BlocksLienCreation(t *testing.T) {
	// Without a reference data client, getInstrumentPrecision should fail closed.
	// This verifies that lien operations cannot silently use a default precision.
	svc := &Service{logger: testLogger()}

	_, err := svc.getInstrumentPrecision(context.Background(), "KWH")
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Contains(t, status.Convert(err).Message(), "reference data client is required")

	_, err = svc.getInstrumentPrecision(context.Background(), "GBP")
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}
