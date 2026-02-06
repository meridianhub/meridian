package valuation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/valuation"
)

func TestRequest_Validation(t *testing.T) {
	tests := []struct {
		name    string
		req     *valuation.Request
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid request",
			req: &valuation.Request{
				RequestID:     uuid.New(),
				MethodID:      uuid.New(),
				MethodVersion: nil, // latest
				Quantity: valuation.Quantity{
					Amount:         decimal.NewFromFloat(100.0),
					InstrumentCode: "KWH",
					Attributes:     map[string]string{"gsp": "P"},
				},
				AccountID:   uuid.New(),
				PartyID:     uuid.New(),
				KnowledgeAt: time.Now(),
				Parameters:  map[string]interface{}{"tier": "Standard"},
			},
			wantErr: false,
		},
		{
			name: "missing request ID",
			req: &valuation.Request{
				MethodID: uuid.New(),
				Quantity: valuation.Quantity{
					Amount:         decimal.NewFromFloat(100.0),
					InstrumentCode: "KWH",
				},
				AccountID:   uuid.New(),
				PartyID:     uuid.New(),
				KnowledgeAt: time.Now(),
			},
			wantErr: true,
			errMsg:  "invalid request",
		},
		{
			name: "missing method ID",
			req: &valuation.Request{
				RequestID: uuid.New(),
				Quantity: valuation.Quantity{
					Amount:         decimal.NewFromFloat(100.0),
					InstrumentCode: "KWH",
				},
				AccountID:   uuid.New(),
				PartyID:     uuid.New(),
				KnowledgeAt: time.Now(),
			},
			wantErr: true,
			errMsg:  "invalid request",
		},
		{
			name: "invalid quantity - no instrument code",
			req: &valuation.Request{
				RequestID: uuid.New(),
				MethodID:  uuid.New(),
				Quantity: valuation.Quantity{
					Amount: decimal.NewFromFloat(100.0),
					// Missing InstrumentCode
				},
				AccountID:   uuid.New(),
				PartyID:     uuid.New(),
				KnowledgeAt: time.Now(),
			},
			wantErr: true,
			errMsg:  "invalid request",
		},
		{
			name: "zero knowledge time",
			req: &valuation.Request{
				RequestID: uuid.New(),
				MethodID:  uuid.New(),
				Quantity: valuation.Quantity{
					Amount:         decimal.NewFromFloat(100.0),
					InstrumentCode: "KWH",
				},
				AccountID:   uuid.New(),
				PartyID:     uuid.New(),
				KnowledgeAt: time.Time{}, // Zero value
			},
			wantErr: true,
			errMsg:  "invalid request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestQuantity_IsZero(t *testing.T) {
	tests := []struct {
		name     string
		quantity valuation.Quantity
		want     bool
	}{
		{
			name: "zero quantity",
			quantity: valuation.Quantity{
				Amount:         decimal.Zero,
				InstrumentCode: "KWH",
			},
			want: true,
		},
		{
			name: "non-zero positive",
			quantity: valuation.Quantity{
				Amount:         decimal.NewFromFloat(100.5),
				InstrumentCode: "KWH",
			},
			want: false,
		},
		{
			name: "non-zero negative",
			quantity: valuation.Quantity{
				Amount:         decimal.NewFromFloat(-50.0),
				InstrumentCode: "KWH",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.quantity.IsZero()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAnalysis_AddPathEntry(t *testing.T) {
	analysis := &valuation.Analysis{}

	// Add entries within limit
	for i := 0; i < 20; i++ {
		analysis.AddPathEntry("step", map[string]interface{}{"i": i})
	}
	assert.Len(t, analysis.CalculationPath, 20)
	assert.Empty(t, analysis.Warnings)

	// Exceed limit - should log warning and truncate
	analysis.AddPathEntry("extra step", map[string]interface{}{})
	assert.Len(t, analysis.CalculationPath, 20) // Still 20, not 21
	assert.Len(t, analysis.Warnings, 1)
	assert.Contains(t, analysis.Warnings[0], "calculation path truncated")
}

func TestAnalysis_RecordPolicyExecution(t *testing.T) {
	analysis := &valuation.Analysis{}

	analysis.RecordPolicyExecution("test_policy", 1, map[string]interface{}{"in": 100}, 42.0, 500)

	require.Len(t, analysis.PoliciesExecuted, 1)
	pe := analysis.PoliciesExecuted[0]
	assert.Equal(t, "test_policy", pe.PolicyName)
	assert.Equal(t, 1, pe.PolicyVersion)
	assert.Equal(t, uint64(500), pe.CostUnits)
}
