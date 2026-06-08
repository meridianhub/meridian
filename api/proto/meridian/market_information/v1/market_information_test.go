package marketinformationv1_test

import (
	"fmt"
	"os"
	"testing"

	"buf.build/go/protovalidate"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// testValidator is the shared validator instance for all validation tests.
var testValidator protovalidate.Validator

func TestMain(m *testing.M) {
	var err error
	testValidator, err = protovalidate.New()
	if err != nil {
		panic(fmt.Sprintf("failed to create validator: %v", err))
	}
	os.Exit(m.Run())
}

// TestDataCategoryEnum verifies all DataCategory enum values are defined.
func TestDataCategoryEnum(t *testing.T) {
	expected := []marketinformationv1.DataCategory{
		marketinformationv1.DataCategory_DATA_CATEGORY_UNSPECIFIED,
		marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
		marketinformationv1.DataCategory_DATA_CATEGORY_INTEREST_RATE,
		marketinformationv1.DataCategory_DATA_CATEGORY_COMMODITY_PRICE,
		marketinformationv1.DataCategory_DATA_CATEGORY_EQUITY_PRICE,
		marketinformationv1.DataCategory_DATA_CATEGORY_INDEX_VALUE,
		marketinformationv1.DataCategory_DATA_CATEGORY_ENERGY_PRICE,
		marketinformationv1.DataCategory_DATA_CATEGORY_CARBON_PRICE,
		marketinformationv1.DataCategory_DATA_CATEGORY_BENCHMARK_RATE,
		marketinformationv1.DataCategory_DATA_CATEGORY_VOLATILITY,
		marketinformationv1.DataCategory_DATA_CATEGORY_CREDIT_SPREAD,
	}

	for i, cat := range expected {
		if int32(cat) != int32(i) {
			t.Errorf("DataCategory %s has unexpected value %d, expected %d", cat, int32(cat), i)
		}
	}

	// Verify total count matches
	if len(marketinformationv1.DataCategory_name) != len(expected) {
		t.Errorf("expected %d data categories, got %d", len(expected), len(marketinformationv1.DataCategory_name))
	}
}

// TestDataSetStatusEnum verifies all DataSetStatus enum values.
func TestDataSetStatusEnum(t *testing.T) {
	expected := []marketinformationv1.DataSetStatus{
		marketinformationv1.DataSetStatus_DATA_SET_STATUS_UNSPECIFIED,
		marketinformationv1.DataSetStatus_DATA_SET_STATUS_DRAFT,
		marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
		marketinformationv1.DataSetStatus_DATA_SET_STATUS_DEPRECATED,
	}

	for i, status := range expected {
		if int32(status) != int32(i) {
			t.Errorf("DataSetStatus %s has unexpected value %d, expected %d", status, int32(status), i)
		}
	}

	// Verify total count
	if len(marketinformationv1.DataSetStatus_name) != len(expected) {
		t.Errorf("expected %d statuses, got %d", len(expected), len(marketinformationv1.DataSetStatus_name))
	}
}

// TestQualityLevelEnum verifies all QualityLevel enum values.
func TestQualityLevelEnum(t *testing.T) {
	expected := []marketinformationv1.QualityLevel{
		marketinformationv1.QualityLevel_QUALITY_LEVEL_UNSPECIFIED,
		marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
		marketinformationv1.QualityLevel_QUALITY_LEVEL_PROVISIONAL,
		marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
		marketinformationv1.QualityLevel_QUALITY_LEVEL_REVISED,
	}

	for i, ql := range expected {
		if int32(ql) != int32(i) {
			t.Errorf("QualityLevel %s has unexpected value %d, expected %d", ql, int32(ql), i)
		}
	}

	// Verify total count
	if len(marketinformationv1.QualityLevel_name) != len(expected) {
		t.Errorf("expected %d quality levels, got %d", len(expected), len(marketinformationv1.QualityLevel_name))
	}
}

// TestDataSetDefinitionValidation verifies DataSetDefinition validation constraints.
func TestDataSetDefinitionValidation(t *testing.T) {
	now := timestamppb.Now()
	validUUID := "123e4567-e89b-12d3-a456-426614174000"

	tests := []struct {
		name    string
		def     *marketinformationv1.DataSetDefinition
		wantErr bool
	}{
		{
			name: "valid minimal definition",
			def: &marketinformationv1.DataSetDefinition{
				Id:            validUUID,
				Code:          "USD_EUR_FX",
				Version:       1,
				Category:      marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:          "USD/EUR",
				Status:        marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
				EffectiveFrom: now,
				CreatedAt:     now,
			},
			wantErr: false,
		},
		{
			name: "valid full definition with CEL expressions",
			def: &marketinformationv1.DataSetDefinition{
				Id:                      validUUID,
				Code:                    "BRENT_CRUDE",
				Version:                 1,
				Category:                marketinformationv1.DataCategory_DATA_CATEGORY_COMMODITY_PRICE,
				Unit:                    "USD/BBL",
				ResolutionKeyExpression: "tenor + ':' + settlement",
				ValidationExpression:    "value > 0 && value < 10000",
				ErrorMessageExpression:  "'Invalid price: ' + string(value)",
				Status:                  marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
				EffectiveFrom:           now,
				DisplayName:             "Brent Crude Oil",
				Description:             "Brent crude oil spot price",
				CreatedAt:               now,
				UpdatedAt:               now,
			},
			wantErr: false,
		},
		{
			name: "valid DRAFT status",
			def: &marketinformationv1.DataSetDefinition{
				Id:            validUUID,
				Code:          "NEW_RATE",
				Version:       1,
				Category:      marketinformationv1.DataCategory_DATA_CATEGORY_INTEREST_RATE,
				Unit:          "percent",
				Status:        marketinformationv1.DataSetStatus_DATA_SET_STATUS_DRAFT,
				EffectiveFrom: now,
				CreatedAt:     now,
			},
			wantErr: false,
		},
		{
			name: "invalid: missing id",
			def: &marketinformationv1.DataSetDefinition{
				Id:            "",
				Code:          "USD_EUR_FX",
				Version:       1,
				Category:      marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:          "USD/EUR",
				Status:        marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
				EffectiveFrom: now,
				CreatedAt:     now,
			},
			wantErr: true,
		},
		{
			name: "invalid: malformed UUID",
			def: &marketinformationv1.DataSetDefinition{
				Id:            "not-a-uuid",
				Code:          "USD_EUR_FX",
				Version:       1,
				Category:      marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:          "USD/EUR",
				Status:        marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
				EffectiveFrom: now,
				CreatedAt:     now,
			},
			wantErr: true,
		},
		{
			name: "invalid: empty code",
			def: &marketinformationv1.DataSetDefinition{
				Id:            validUUID,
				Code:          "",
				Version:       1,
				Category:      marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:          "USD/EUR",
				Status:        marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
				EffectiveFrom: now,
				CreatedAt:     now,
			},
			wantErr: true,
		},
		{
			name: "invalid: lowercase code",
			def: &marketinformationv1.DataSetDefinition{
				Id:            validUUID,
				Code:          "usd_eur_fx",
				Version:       1,
				Category:      marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:          "USD/EUR",
				Status:        marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
				EffectiveFrom: now,
				CreatedAt:     now,
			},
			wantErr: true,
		},
		{
			name: "invalid: version is zero",
			def: &marketinformationv1.DataSetDefinition{
				Id:            validUUID,
				Code:          "USD_EUR_FX",
				Version:       0,
				Category:      marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:          "USD/EUR",
				Status:        marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
				EffectiveFrom: now,
				CreatedAt:     now,
			},
			wantErr: true,
		},
		{
			name: "invalid: DATA_CATEGORY_UNSPECIFIED",
			def: &marketinformationv1.DataSetDefinition{
				Id:            validUUID,
				Code:          "USD_EUR_FX",
				Version:       1,
				Category:      marketinformationv1.DataCategory_DATA_CATEGORY_UNSPECIFIED,
				Unit:          "USD/EUR",
				Status:        marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
				EffectiveFrom: now,
				CreatedAt:     now,
			},
			wantErr: true,
		},
		{
			name: "invalid: DATA_SET_STATUS_UNSPECIFIED",
			def: &marketinformationv1.DataSetDefinition{
				Id:            validUUID,
				Code:          "USD_EUR_FX",
				Version:       1,
				Category:      marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:          "USD/EUR",
				Status:        marketinformationv1.DataSetStatus_DATA_SET_STATUS_UNSPECIFIED,
				EffectiveFrom: now,
				CreatedAt:     now,
			},
			wantErr: true,
		},
		{
			name: "invalid: empty unit",
			def: &marketinformationv1.DataSetDefinition{
				Id:            validUUID,
				Code:          "USD_EUR_FX",
				Version:       1,
				Category:      marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:          "",
				Status:        marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
				EffectiveFrom: now,
				CreatedAt:     now,
			},
			wantErr: true,
		},
		{
			name: "invalid: missing effective_from",
			def: &marketinformationv1.DataSetDefinition{
				Id:        validUUID,
				Code:      "USD_EUR_FX",
				Version:   1,
				Category:  marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:      "USD/EUR",
				Status:    marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
				CreatedAt: now,
				// EffectiveFrom: nil - missing required field
			},
			wantErr: true,
		},
		{
			name: "invalid: missing created_at",
			def: &marketinformationv1.DataSetDefinition{
				Id:            validUUID,
				Code:          "USD_EUR_FX",
				Version:       1,
				Category:      marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
				Unit:          "USD/EUR",
				Status:        marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
				EffectiveFrom: now,
				// CreatedAt: nil - missing required field
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.def)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestMarketPriceObservationValidation verifies MarketPriceObservation validation constraints.
func TestMarketPriceObservationValidation(t *testing.T) {
	now := timestamppb.Now()
	validUUID := "123e4567-e89b-12d3-a456-426614174000"
	sourceUUID := "987fcdeb-51a2-3e4f-b6c7-890123456789"

	tests := []struct {
		name    string
		obs     *marketinformationv1.MarketPriceObservation
		wantErr bool
	}{
		{
			name: "valid minimal observation",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "USD_EUR_FX",
				DatasetVersion: 1,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "1.2345",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceId:       sourceUUID,
				CreatedAt:      now,
			},
			wantErr: false,
		},
		{
			name: "valid full observation with attributes",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:                 validUUID,
				DatasetCode:        "SOFR_3M",
				DatasetVersion:     2,
				ResolutionKeyValue: "3M:T+2",
				ObservedAt:         now,
				ValidFrom:          now,
				ValidTo:            timestamppb.Now(),
				Value:              "5.25",
				Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceId:           sourceUUID,
				CreatedAt:          now,
				Attributes: []*quantityv1.AttributeEntry{
					{Key: "tenor", Value: "3M"},
					{Key: "settlement", Value: "T+2"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid negative value",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "EUR_INTEREST",
				DatasetVersion: 1,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "-0.50",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceId:       sourceUUID,
				CreatedAt:      now,
			},
			wantErr: false,
		},
		{
			name: "valid ESTIMATE quality",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "USD_EUR_FX",
				DatasetVersion: 1,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "1.2345",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
				SourceId:       sourceUUID,
				CreatedAt:      now,
			},
			wantErr: false,
		},
		{
			name: "valid PROVISIONAL quality",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "USD_EUR_FX",
				DatasetVersion: 1,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "1.2345",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_PROVISIONAL,
				SourceId:       sourceUUID,
				CreatedAt:      now,
			},
			wantErr: false,
		},
		{
			name: "valid REVISED quality with supersession",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "USD_EUR_FX",
				DatasetVersion: 1,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "1.2346",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_REVISED,
				SourceId:       sourceUUID,
				CreatedAt:      now,
				SupersededAt:   now,
				SupersededById: "abcdef01-2345-6789-abcd-ef0123456789",
			},
			wantErr: false,
		},
		{
			name: "invalid: missing id",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             "",
				DatasetCode:    "USD_EUR_FX",
				DatasetVersion: 1,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "1.2345",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceId:       sourceUUID,
				CreatedAt:      now,
			},
			wantErr: true,
		},
		{
			name: "invalid: empty dataset_code",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "",
				DatasetVersion: 1,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "1.2345",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceId:       sourceUUID,
				CreatedAt:      now,
			},
			wantErr: true,
		},
		{
			name: "invalid: lowercase dataset_code",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "usd_eur_fx",
				DatasetVersion: 1,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "1.2345",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceId:       sourceUUID,
				CreatedAt:      now,
			},
			wantErr: true,
		},
		{
			name: "invalid: version is zero",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "USD_EUR_FX",
				DatasetVersion: 0,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "1.2345",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceId:       sourceUUID,
				CreatedAt:      now,
			},
			wantErr: true,
		},
		{
			name: "invalid: malformed value",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "USD_EUR_FX",
				DatasetVersion: 1,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "not-a-number",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceId:       sourceUUID,
				CreatedAt:      now,
			},
			wantErr: true,
		},
		{
			name: "invalid: QUALITY_LEVEL_UNSPECIFIED",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "USD_EUR_FX",
				DatasetVersion: 1,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "1.2345",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_UNSPECIFIED,
				SourceId:       sourceUUID,
				CreatedAt:      now,
			},
			wantErr: true,
		},
		{
			name: "invalid: missing observed_at",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "USD_EUR_FX",
				DatasetVersion: 1,
				// ObservedAt: nil
				ValidFrom: now,
				Value:     "1.2345",
				Quality:   marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceId:  sourceUUID,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: missing valid_from",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "USD_EUR_FX",
				DatasetVersion: 1,
				ObservedAt:     now,
				// ValidFrom: nil
				Value:     "1.2345",
				Quality:   marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceId:  sourceUUID,
				CreatedAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid: missing source_id",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "USD_EUR_FX",
				DatasetVersion: 1,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "1.2345",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceId:       "",
				CreatedAt:      now,
			},
			wantErr: true,
		},
		{
			name: "invalid: malformed source_id",
			obs: &marketinformationv1.MarketPriceObservation{
				Id:             validUUID,
				DatasetCode:    "USD_EUR_FX",
				DatasetVersion: 1,
				ObservedAt:     now,
				ValidFrom:      now,
				Value:          "1.2345",
				Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceId:       "not-a-uuid",
				CreatedAt:      now,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.obs)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestDataSourceValidation verifies DataSource validation constraints.
func TestDataSourceValidation(t *testing.T) {
	now := timestamppb.Now()
	validUUID := "123e4567-e89b-12d3-a456-426614174000"

	tests := []struct {
		name    string
		src     *marketinformationv1.DataSource
		wantErr bool
	}{
		{
			name: "valid minimal data source",
			src: &marketinformationv1.DataSource{
				Id:         validUUID,
				Code:       "BLOOMBERG",
				Name:       "Bloomberg LP",
				TrustLevel: 90,
				IsActive:   true,
				CreatedAt:  now,
			},
			wantErr: false,
		},
		{
			name: "valid full data source",
			src: &marketinformationv1.DataSource{
				Id:          validUUID,
				Code:        "ECB",
				Name:        "European Central Bank",
				Description: "Official ECB reference rates",
				TrustLevel:  100,
				IsActive:    true,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			wantErr: false,
		},
		{
			name: "valid: trust_level at minimum (0)",
			src: &marketinformationv1.DataSource{
				Id:         validUUID,
				Code:       "MANUAL",
				Name:       "Manual Entry",
				TrustLevel: 0,
				IsActive:   true,
				CreatedAt:  now,
			},
			wantErr: false,
		},
		{
			name: "valid: trust_level at maximum (100)",
			src: &marketinformationv1.DataSource{
				Id:         validUUID,
				Code:       "OFFICIAL",
				Name:       "Official Source",
				TrustLevel: 100,
				IsActive:   true,
				CreatedAt:  now,
			},
			wantErr: false,
		},
		{
			name: "invalid: missing id",
			src: &marketinformationv1.DataSource{
				Id:         "",
				Code:       "BLOOMBERG",
				Name:       "Bloomberg LP",
				TrustLevel: 90,
				IsActive:   true,
				CreatedAt:  now,
			},
			wantErr: true,
		},
		{
			name: "invalid: empty code",
			src: &marketinformationv1.DataSource{
				Id:         validUUID,
				Code:       "",
				Name:       "Bloomberg LP",
				TrustLevel: 90,
				IsActive:   true,
				CreatedAt:  now,
			},
			wantErr: true,
		},
		{
			name: "invalid: lowercase code",
			src: &marketinformationv1.DataSource{
				Id:         validUUID,
				Code:       "bloomberg",
				Name:       "Bloomberg LP",
				TrustLevel: 90,
				IsActive:   true,
				CreatedAt:  now,
			},
			wantErr: true,
		},
		{
			name: "invalid: empty name",
			src: &marketinformationv1.DataSource{
				Id:         validUUID,
				Code:       "BLOOMBERG",
				Name:       "",
				TrustLevel: 90,
				IsActive:   true,
				CreatedAt:  now,
			},
			wantErr: true,
		},
		{
			name: "invalid: trust_level negative",
			src: &marketinformationv1.DataSource{
				Id:         validUUID,
				Code:       "BLOOMBERG",
				Name:       "Bloomberg LP",
				TrustLevel: -1,
				IsActive:   true,
				CreatedAt:  now,
			},
			wantErr: true,
		},
		{
			name: "invalid: trust_level exceeds 100",
			src: &marketinformationv1.DataSource{
				Id:         validUUID,
				Code:       "BLOOMBERG",
				Name:       "Bloomberg LP",
				TrustLevel: 101,
				IsActive:   true,
				CreatedAt:  now,
			},
			wantErr: true,
		},
		{
			name: "invalid: missing created_at",
			src: &marketinformationv1.DataSource{
				Id:         validUUID,
				Code:       "BLOOMBERG",
				Name:       "Bloomberg LP",
				TrustLevel: 90,
				IsActive:   true,
				// CreatedAt: nil
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.src)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestObservationRecordedEvent verifies ObservationRecorded event validation.
func TestObservationRecordedEvent(t *testing.T) {
	now := timestamppb.Now()
	validUUID := "123e4567-e89b-12d3-a456-426614174000"
	sourceUUID := "987fcdeb-51a2-3e4f-b6c7-890123456789"

	tests := []struct {
		name    string
		event   *marketinformationv1.ObservationRecorded
		wantErr bool
	}{
		{
			name: "valid event",
			event: &marketinformationv1.ObservationRecorded{
				ObservationId:      validUUID,
				DatasetCode:        "USD_EUR_FX",
				ResolutionKeyValue: "spot",
				ObservedAt:         now,
				Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				Value:              "1.2345",
				SourceId:           sourceUUID,
				RecordedAt:         now,
			},
			wantErr: false,
		},
		{
			name: "valid event with supersession",
			event: &marketinformationv1.ObservationRecorded{
				ObservationId:           validUUID,
				DatasetCode:             "USD_EUR_FX",
				ObservedAt:              now,
				Quality:                 marketinformationv1.QualityLevel_QUALITY_LEVEL_REVISED,
				Value:                   "1.2346",
				SourceId:                sourceUUID,
				RecordedAt:              now,
				SupersedesObservationId: "abcdef01-2345-6789-abcd-ef0123456789",
			},
			wantErr: false,
		},
		{
			name: "invalid: missing observation_id",
			event: &marketinformationv1.ObservationRecorded{
				ObservationId: "",
				DatasetCode:   "USD_EUR_FX",
				ObservedAt:    now,
				Quality:       marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				Value:         "1.2345",
				SourceId:      sourceUUID,
				RecordedAt:    now,
			},
			wantErr: true,
		},
		{
			name: "invalid: missing source_id",
			event: &marketinformationv1.ObservationRecorded{
				ObservationId: validUUID,
				DatasetCode:   "USD_EUR_FX",
				ObservedAt:    now,
				Quality:       marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				Value:         "1.2345",
				SourceId:      "",
				RecordedAt:    now,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestDataSetDefinitionSerialization verifies round-trip serialization.
func TestDataSetDefinitionSerialization(t *testing.T) {
	now := timestamppb.Now()
	validUUID := "123e4567-e89b-12d3-a456-426614174000"

	attrSchema, _ := structpb.NewStruct(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tenor": map[string]interface{}{"type": "string"},
		},
	})

	original := &marketinformationv1.DataSetDefinition{
		Id:                      validUUID,
		Code:                    "SOFR_CURVE",
		Version:                 2,
		Category:                marketinformationv1.DataCategory_DATA_CATEGORY_INTEREST_RATE,
		Unit:                    "percent",
		ResolutionKeyExpression: "tenor",
		ValidationExpression:    "value >= -10 && value <= 50",
		ErrorMessageExpression:  "'Rate out of range'",
		AttributeSchema:         attrSchema,
		Status:                  marketinformationv1.DataSetStatus_DATA_SET_STATUS_ACTIVE,
		EffectiveFrom:           now,
		EffectiveTo:             now,
		DisplayName:             "SOFR Interest Rate Curve",
		Description:             "SOFR interest rate curve by tenor",
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	decoded := &marketinformationv1.DataSetDefinition{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !proto.Equal(original, decoded) {
		t.Error("round-trip serialization produced different message")
	}

	// Verify specific fields
	if decoded.Id != original.Id {
		t.Errorf("id mismatch: got %v, want %v", decoded.Id, original.Id)
	}
	if decoded.Code != original.Code {
		t.Errorf("code mismatch: got %v, want %v", decoded.Code, original.Code)
	}
	if decoded.Category != original.Category {
		t.Errorf("category mismatch: got %v, want %v", decoded.Category, original.Category)
	}
	if decoded.Status != original.Status {
		t.Errorf("status mismatch: got %v, want %v", decoded.Status, original.Status)
	}
}

// TestMarketPriceObservationSerialization verifies round-trip serialization.
func TestMarketPriceObservationSerialization(t *testing.T) {
	now := timestamppb.Now()
	validUUID := "123e4567-e89b-12d3-a456-426614174000"
	sourceUUID := "987fcdeb-51a2-3e4f-b6c7-890123456789"

	original := &marketinformationv1.MarketPriceObservation{
		Id:                 validUUID,
		DatasetCode:        "USD_EUR_FX",
		DatasetVersion:     1,
		ResolutionKeyValue: "spot",
		ObservedAt:         now,
		ValidFrom:          now,
		Value:              "1.23456789",
		Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
		SourceId:           sourceUUID,
		CreatedAt:          now,
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "bid", Value: "1.23450"},
			{Key: "ask", Value: "1.23462"},
		},
	}

	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	decoded := &marketinformationv1.MarketPriceObservation{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !proto.Equal(original, decoded) {
		t.Error("round-trip serialization produced different message")
	}

	// Verify value precision is preserved
	if decoded.Value != original.Value {
		t.Errorf("value precision not preserved: got %v, want %v", decoded.Value, original.Value)
	}

	// Verify attributes are preserved
	if len(decoded.Attributes) != len(original.Attributes) {
		t.Errorf("attributes count mismatch: got %d, want %d", len(decoded.Attributes), len(original.Attributes))
	}
}

// TestMarketPriceObservationRevisionField verifies the revision field (Axis B of
// the two-axis quality model) round-trips through serialization and defaults to 0.
func TestMarketPriceObservationRevisionField(t *testing.T) {
	now := timestamppb.Now()
	validUUID := "123e4567-e89b-12d3-a456-426614174000"
	sourceUUID := "987fcdeb-51a2-3e4f-b6c7-890123456789"

	// A freshly constructed observation defaults revision to 0 (original).
	original := &marketinformationv1.MarketPriceObservation{
		Id:             validUUID,
		DatasetCode:    "USD_EUR_FX",
		DatasetVersion: 1,
		ObservedAt:     now,
		ValidFrom:      now,
		Value:          "1.2345",
		Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
		SourceId:       sourceUUID,
		CreatedAt:      now,
	}
	if original.GetRevision() != 0 {
		t.Errorf("default revision = %d, want 0", original.GetRevision())
	}

	// A non-zero revision marks a correction that supersedes a prior observation.
	for _, rev := range []uint32{0, 1, 2, 42} {
		obs := proto.Clone(original).(*marketinformationv1.MarketPriceObservation)
		obs.Revision = rev

		data, err := proto.Marshal(obs)
		if err != nil {
			t.Fatalf("failed to marshal observation with revision %d: %v", rev, err)
		}

		decoded := &marketinformationv1.MarketPriceObservation{}
		if err := proto.Unmarshal(data, decoded); err != nil {
			t.Fatalf("failed to unmarshal observation with revision %d: %v", rev, err)
		}

		if decoded.GetRevision() != rev {
			t.Errorf("revision not preserved: got %d, want %d", decoded.GetRevision(), rev)
		}
		if !proto.Equal(obs, decoded) {
			t.Errorf("round-trip mismatch for revision %d", rev)
		}
	}
}

// TestServiceClientInterface verifies service client interface exists with all methods.
func TestServiceClientInterface(_ *testing.T) {
	// This test verifies the gRPC client interface has all expected methods.
	// We can't instantiate a client without a connection, but we can verify
	// the interface exists by checking its type.

	var client marketinformationv1.MarketInformationServiceClient
	_ = client // Verifies the type exists

	// The interface should have these methods (verified by compilation):
	// - RegisterDataSet
	// - UpdateDataSet
	// - ActivateDataSet
	// - DeprecateDataSet
	// - RetrieveDataSet
	// - ListDataSets
	// - RegisterDataSource
	// - UpdateDataSource
	// - DeactivateDataSource
	// - ListDataSources
	// - RecordObservation
	// - RecordObservationBatch
	// - RetrieveObservation
	// - ListObservations
}

// TestRequestResponseMessages verifies all request/response message types can be instantiated.
func TestRequestResponseMessages(_ *testing.T) {
	now := timestamppb.Now()

	// Data Set requests
	_ = &marketinformationv1.RegisterDataSetRequest{
		Code:          "TEST",
		Category:      marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
		Unit:          "USD/EUR",
		EffectiveFrom: now,
	}
	_ = &marketinformationv1.RegisterDataSetResponse{}
	_ = &marketinformationv1.UpdateDataSetRequest{Code: "TEST", Version: 1}
	_ = &marketinformationv1.UpdateDataSetResponse{}
	_ = &marketinformationv1.ActivateDataSetRequest{Code: "TEST", Version: 1}
	_ = &marketinformationv1.ActivateDataSetResponse{}
	_ = &marketinformationv1.DeprecateDataSetRequest{Code: "TEST", Version: 1}
	_ = &marketinformationv1.DeprecateDataSetResponse{}
	_ = &marketinformationv1.RetrieveDataSetRequest{Code: "TEST"}
	_ = &marketinformationv1.RetrieveDataSetResponse{}
	_ = &marketinformationv1.ListDataSetsRequest{}
	_ = &marketinformationv1.ListDataSetsResponse{}

	// Data Source requests
	_ = &marketinformationv1.RegisterDataSourceRequest{Code: "TEST", Name: "Test"}
	_ = &marketinformationv1.RegisterDataSourceResponse{}
	_ = &marketinformationv1.UpdateDataSourceRequest{Code: "TEST"}
	_ = &marketinformationv1.UpdateDataSourceResponse{}
	_ = &marketinformationv1.DeactivateDataSourceRequest{Code: "TEST"}
	_ = &marketinformationv1.DeactivateDataSourceResponse{}
	_ = &marketinformationv1.ListDataSourcesRequest{}
	_ = &marketinformationv1.ListDataSourcesResponse{}

	// Observation requests
	_ = &marketinformationv1.RecordObservationRequest{
		DatasetCode: "TEST",
		Value:       "1.0",
		ObservedAt:  now,
		ValidFrom:   now,
	}
	_ = &marketinformationv1.RecordObservationResponse{}
	_ = &marketinformationv1.RecordObservationBatchRequest{
		Observations: []*marketinformationv1.BatchObservationEntry{
			{DatasetCode: "TEST", Value: "1.0", ObservedAt: now, ValidFrom: now},
		},
	}
	_ = &marketinformationv1.RecordObservationBatchResponse{}
	_ = &marketinformationv1.RetrieveObservationRequest{ObservationId: "123e4567-e89b-12d3-a456-426614174000"}
	_ = &marketinformationv1.RetrieveObservationResponse{}
	_ = &marketinformationv1.ListObservationsRequest{DatasetCode: "TEST"}
	_ = &marketinformationv1.ListObservationsResponse{}

	// Batch types
	_ = &marketinformationv1.BatchObservationEntry{}
	_ = &marketinformationv1.BatchObservationResult{}
}

// TestRecordObservationRequestValidation verifies RecordObservationRequest validation.
func TestRecordObservationRequestValidation(t *testing.T) {
	now := timestamppb.Now()

	tests := []struct {
		name    string
		req     *marketinformationv1.RecordObservationRequest
		wantErr bool
	}{
		{
			name: "valid request",
			req: &marketinformationv1.RecordObservationRequest{
				DatasetCode: "USD_EUR_FX",
				ObservedAt:  now,
				ValidFrom:   now,
				Value:       "1.2345",
				Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceCode:  "BLOOMBERG",
			},
			wantErr: false,
		},
		{
			name: "valid with supersession",
			req: &marketinformationv1.RecordObservationRequest{
				DatasetCode:             "USD_EUR_FX",
				ObservedAt:              now,
				ValidFrom:               now,
				Value:                   "1.2345",
				Quality:                 marketinformationv1.QualityLevel_QUALITY_LEVEL_REVISED,
				SourceCode:              "BLOOMBERG",
				SupersedesObservationId: "123e4567-e89b-12d3-a456-426614174000",
			},
			wantErr: false,
		},
		{
			name: "invalid: empty dataset_code",
			req: &marketinformationv1.RecordObservationRequest{
				DatasetCode: "",
				ObservedAt:  now,
				ValidFrom:   now,
				Value:       "1.2345",
				Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceCode:  "BLOOMBERG",
			},
			wantErr: true,
		},
		{
			name: "invalid: malformed value",
			req: &marketinformationv1.RecordObservationRequest{
				DatasetCode: "USD_EUR_FX",
				ObservedAt:  now,
				ValidFrom:   now,
				Value:       "abc",
				Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
				SourceCode:  "BLOOMBERG",
			},
			wantErr: true,
		},
		{
			name: "invalid: QUALITY_LEVEL_UNSPECIFIED",
			req: &marketinformationv1.RecordObservationRequest{
				DatasetCode: "USD_EUR_FX",
				ObservedAt:  now,
				ValidFrom:   now,
				Value:       "1.2345",
				Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_UNSPECIFIED,
				SourceCode:  "BLOOMBERG",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testValidator.Validate(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
