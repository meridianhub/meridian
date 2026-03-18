package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPeriod(t *testing.T) {
	tests := []struct {
		name    string
		start   time.Time
		end     time.Time
		wantErr error
	}{
		{
			name:    "valid period with duration",
			start:   time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			end:     time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
			wantErr: nil,
		},
		{
			name:    "valid instant (start equals end)",
			start:   time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			end:     time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			wantErr: nil,
		},
		{
			name:    "invalid: end before start",
			start:   time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
			end:     time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			wantErr: ErrInvalidPeriod,
		},
		{
			name:    "invalid: start not in UTC",
			start:   time.Date(2025, 1, 1, 12, 0, 0, 0, time.FixedZone("EST", -5*3600)),
			end:     time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
			wantErr: ErrNonUTCTimestamp,
		},
		{
			name:    "invalid: end not in UTC",
			start:   time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			end:     time.Date(2025, 1, 1, 12, 30, 0, 0, time.FixedZone("PST", -8*3600)),
			wantErr: ErrNonUTCTimestamp,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			period, err := NewPeriod(tt.start, tt.end)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.start, period.Start)
			assert.Equal(t, tt.end, period.End)
		})
	}
}

func TestInstant(t *testing.T) {
	tests := []struct {
		name    string
		instant time.Time
		wantErr error
	}{
		{
			name:    "valid UTC instant",
			instant: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			wantErr: nil,
		},
		{
			name:    "invalid: non-UTC instant",
			instant: time.Date(2025, 1, 1, 12, 0, 0, 0, time.FixedZone("EST", -5*3600)),
			wantErr: ErrNonUTCTimestamp,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			period, err := Instant(tt.instant)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.instant, period.Start)
			assert.Equal(t, tt.instant, period.End)
			assert.True(t, period.IsInstant())
		})
	}
}

func TestPeriod_IsInstant(t *testing.T) {
	instant := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	later := time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC)

	tests := []struct {
		name   string
		period Period
		want   bool
	}{
		{
			name:   "instant period",
			period: Period{Start: instant, End: instant},
			want:   true,
		},
		{
			name:   "duration period",
			period: Period{Start: instant, End: later},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.period.IsInstant())
		})
	}
}

func TestPeriod_Duration(t *testing.T) {
	start := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC)

	period := Period{Start: start, End: end}
	assert.Equal(t, 30*time.Minute, period.Duration())
}

func TestMeasurement_IsCurrent(t *testing.T) {
	supersededID := uuid.New()

	tests := []struct {
		name        string
		measurement Measurement
		want        bool
	}{
		{
			name: "current measurement",
			measurement: Measurement{
				SupersededBy: nil,
			},
			want: true,
		},
		{
			name: "superseded measurement",
			measurement: Measurement{
				SupersededBy: &supersededID,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.measurement.IsCurrent())
		})
	}
}

func TestMeasurement_IsLocked(t *testing.T) {
	lockedAt := time.Now().UTC()

	tests := []struct {
		name        string
		measurement Measurement
		want        bool
	}{
		{
			name: "unlocked measurement",
			measurement: Measurement{
				LockedAt: nil,
			},
			want: false,
		},
		{
			name: "locked measurement",
			measurement: Measurement{
				LockedAt: &lockedAt,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.measurement.IsLocked())
		})
	}
}

func TestMustPeriod(t *testing.T) {
	t.Run("valid period does not panic", func(t *testing.T) {
		start := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
		end := time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC)
		p := MustPeriod(start, end)
		assert.Equal(t, start, p.Start)
		assert.Equal(t, end, p.End)
	})

	t.Run("valid instant does not panic", func(t *testing.T) {
		ts := time.Date(2025, 6, 15, 9, 0, 0, 0, time.UTC)
		p := MustPeriod(ts, ts)
		assert.True(t, p.IsInstant())
	})

	t.Run("invalid period panics", func(t *testing.T) {
		start := time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC)
		end := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
		assert.Panics(t, func() {
			MustPeriod(start, end)
		})
	})

	t.Run("non-UTC timestamp panics", func(t *testing.T) {
		start := time.Date(2025, 1, 1, 12, 0, 0, 0, time.FixedZone("EST", -5*3600))
		end := time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC)
		assert.Panics(t, func() {
			MustPeriod(start, end)
		})
	})
}

func TestPeriod_Validate(t *testing.T) {
	tests := []struct {
		name    string
		period  Period
		wantErr error
	}{
		{
			name: "valid period",
			period: Period{
				Start: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
				End:   time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
			},
			wantErr: nil,
		},
		{
			name: "valid instant",
			period: Period{
				Start: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
				End:   time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			},
			wantErr: nil,
		},
		{
			name: "start not UTC",
			period: Period{
				Start: time.Date(2025, 1, 1, 12, 0, 0, 0, time.FixedZone("EST", -5*3600)),
				End:   time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
			},
			wantErr: ErrNonUTCTimestamp,
		},
		{
			name: "end not UTC",
			period: Period{
				Start: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
				End:   time.Date(2025, 1, 1, 12, 30, 0, 0, time.FixedZone("PST", -8*3600)),
			},
			wantErr: ErrNonUTCTimestamp,
		},
		{
			name: "end before start",
			period: Period{
				Start: time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
				End:   time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			},
			wantErr: ErrInvalidPeriod,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.period.Validate()
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMeasurement_FullModel(t *testing.T) {
	// Test that we can create a complete measurement with all fields
	accountID := uuid.New()
	timestamp := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	period, err := Instant(timestamp)
	require.NoError(t, err)

	measurement := &Measurement{
		ID:        uuid.New(),
		AccountID: accountID,
		AssetCode: "MERIDIAN-CURRENT-ACCOUNT-OPS",
		Quantity:  decimal.NewFromInt(1),
		Period:    period,
		Attributes: map[string]string{
			"service":   "current_account",
			"operation": "INSERT",
			"table":     "accounts",
		},
		Source:        "AUDIT_STREAM",
		QualityScore:  60,
		ReceivedAt:    time.Now().UTC(),
		SupersededBy:  nil,
		SettlementRun: "",
		LockedAt:      nil,
	}

	assert.NotEqual(t, uuid.Nil, measurement.ID)
	assert.Equal(t, accountID, measurement.AccountID)
	assert.Equal(t, "MERIDIAN-CURRENT-ACCOUNT-OPS", measurement.AssetCode)
	assert.True(t, measurement.Quantity.Equal(decimal.NewFromInt(1)))
	assert.True(t, measurement.Period.IsInstant())
	assert.Equal(t, 3, len(measurement.Attributes))
	assert.Equal(t, "AUDIT_STREAM", measurement.Source)
	assert.Equal(t, 60, measurement.QualityScore)
	assert.True(t, measurement.IsCurrent())
	assert.False(t, measurement.IsLocked())
}
