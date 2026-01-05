package domain_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPosition(t *testing.T) {
	tests := []struct {
		name           string
		accountID      string
		instrumentCode string
		bucketKey      string
		amount         decimal.Decimal
		dimension      string
		attributes     map[string]string
		referenceID    uuid.UUID
		createdBy      string
		wantErr        error
	}{
		{
			name:           "valid monetary position",
			accountID:      "ACC-001",
			instrumentCode: "GBP",
			bucketKey:      "default",
			amount:         decimal.NewFromFloat(100.50),
			dimension:      "Monetary",
			attributes:     map[string]string{"source": "deposit"},
			referenceID:    uuid.New(),
			createdBy:      "system",
			wantErr:        nil,
		},
		{
			name:           "valid energy position",
			accountID:      "ACC-002",
			instrumentCode: "KWH",
			bucketKey:      "meter-001",
			amount:         decimal.NewFromFloat(1500.0),
			dimension:      "Energy",
			attributes:     nil,
			referenceID:    uuid.Nil,
			createdBy:      "system",
			wantErr:        nil,
		},
		{
			name:           "negative amount allowed (for debits)",
			accountID:      "ACC-003",
			instrumentCode: "USD",
			bucketKey:      "default",
			amount:         decimal.NewFromFloat(-50.00),
			dimension:      "Monetary",
			attributes:     nil,
			referenceID:    uuid.New(),
			createdBy:      "system",
			wantErr:        nil,
		},
		{
			name:           "empty account ID",
			accountID:      "",
			instrumentCode: "GBP",
			bucketKey:      "default",
			amount:         decimal.NewFromFloat(100.00),
			dimension:      "Monetary",
			attributes:     nil,
			referenceID:    uuid.New(),
			createdBy:      "system",
			wantErr:        domain.ErrEmptyAccountID,
		},
		{
			name:           "empty instrument code",
			accountID:      "ACC-001",
			instrumentCode: "",
			bucketKey:      "default",
			amount:         decimal.NewFromFloat(100.00),
			dimension:      "Monetary",
			attributes:     nil,
			referenceID:    uuid.New(),
			createdBy:      "system",
			wantErr:        domain.ErrEmptyInstrumentCode,
		},
		{
			name:           "empty bucket key",
			accountID:      "ACC-001",
			instrumentCode: "GBP",
			bucketKey:      "",
			amount:         decimal.NewFromFloat(100.00),
			dimension:      "Monetary",
			attributes:     nil,
			referenceID:    uuid.New(),
			createdBy:      "system",
			wantErr:        domain.ErrEmptyBucketKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos, err := domain.NewPosition(
				tt.accountID,
				tt.instrumentCode,
				tt.bucketKey,
				tt.amount,
				tt.dimension,
				tt.attributes,
				tt.referenceID,
				tt.createdBy,
			)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, pos)
			} else {
				require.NoError(t, err)
				require.NotNil(t, pos)

				// Verify all fields are set correctly
				assert.NotEqual(t, uuid.Nil, pos.ID)
				assert.Equal(t, tt.accountID, pos.AccountID)
				assert.Equal(t, tt.instrumentCode, pos.InstrumentCode)
				assert.Equal(t, tt.bucketKey, pos.BucketKey)
				assert.True(t, tt.amount.Equal(pos.Amount))
				assert.Equal(t, tt.dimension, pos.Dimension)
				assert.Equal(t, tt.attributes, pos.Attributes)
				assert.Equal(t, tt.referenceID, pos.ReferenceID)
				assert.Equal(t, tt.createdBy, pos.CreatedBy)
				assert.False(t, pos.CreatedAt.IsZero())
			}
		})
	}
}

func TestPosition_MultiplePositionsSameAccount(t *testing.T) {
	// Verify that creating 100 positions for the same account creates 100 distinct IDs
	const numPositions = 100
	positions := make([]*domain.Position, numPositions)
	idSet := make(map[uuid.UUID]struct{})

	for i := 0; i < numPositions; i++ {
		pos, err := domain.NewPosition(
			"ACC-001",
			"GBP",
			"default",
			decimal.NewFromFloat(1.00),
			"Monetary",
			nil,
			uuid.Nil,
			"system",
		)
		require.NoError(t, err)
		require.NotNil(t, pos)

		positions[i] = pos
		idSet[pos.ID] = struct{}{}
	}

	// All positions should have unique IDs
	assert.Len(t, idSet, numPositions, "expected %d unique position IDs", numPositions)
}

func TestAggregatedPosition(t *testing.T) {
	// Test that AggregatedPosition struct can be used correctly
	agg := domain.AggregatedPosition{
		AccountID:      "ACC-001",
		InstrumentCode: "GBP",
		BucketKey:      "default",
		TotalAmount:    decimal.NewFromFloat(1500.50),
		Dimension:      "Monetary",
		RecordCount:    50,
	}

	assert.Equal(t, "ACC-001", agg.AccountID)
	assert.Equal(t, "GBP", agg.InstrumentCode)
	assert.Equal(t, "default", agg.BucketKey)
	assert.True(t, decimal.NewFromFloat(1500.50).Equal(agg.TotalAmount))
	assert.Equal(t, int64(50), agg.RecordCount)
}
