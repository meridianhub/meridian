package quantity

import (
	"errors"
	"testing"
)

func TestNewInstrument_ValidCases(t *testing.T) {
	tests := []struct {
		name      string
		code      string
		version   uint32
		dimension string
		precision int
	}{
		{
			name:      "USD currency",
			code:      "USD",
			version:   1,
			dimension: "CURRENCY",
			precision: 2,
		},
		{
			name:      "EUR currency",
			code:      "EUR",
			version:   1,
			dimension: "CURRENCY",
			precision: 2,
		},
		{
			name:      "KWH energy",
			code:      "KWH",
			version:   1,
			dimension: "ENERGY",
			precision: 6,
		},
		{
			name:      "GPU_HOUR compute",
			code:      "GPU_HOUR",
			version:   2,
			dimension: "COMPUTE",
			precision: 4,
		},
		{
			name:      "carbon credit",
			code:      "TONNE_CO2E",
			version:   1,
			dimension: "CARBON",
			precision: 3,
		},
		{
			name:      "data dimension",
			code:      "GB",
			version:   1,
			dimension: "DATA",
			precision: 0,
		},
		{
			name:      "count dimension",
			code:      "VOUCHER",
			version:   1,
			dimension: "COUNT",
			precision: 0,
		},
		{
			name:      "version zero allowed",
			code:      "LEGACY",
			version:   0,
			dimension: "CURRENCY",
			precision: 2,
		},
		{
			name:      "precision zero allowed",
			code:      "JPY",
			version:   1,
			dimension: "CURRENCY",
			precision: 0,
		},
		{
			name:      "max precision allowed",
			code:      "SATOSHI",
			version:   1,
			dimension: "CURRENCY",
			precision: 18,
		},
		{
			name:      "lowercase dimension normalized",
			code:      "USD",
			version:   1,
			dimension: "currency",
			precision: 2,
		},
		{
			name:      "code with numbers",
			code:      "ISO4217",
			version:   1,
			dimension: "CURRENCY",
			precision: 2,
		},
		{
			name:      "single letter code",
			code:      "X",
			version:   1,
			dimension: "COUNT",
			precision: 0,
		},
		{
			name:      "32 character code at limit",
			code:      "ABCDEFGHIJKLMNOPQRSTUVWXYZ123456",
			version:   1,
			dimension: "COUNT",
			precision: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst, err := NewInstrument(tt.code, tt.version, tt.dimension, tt.precision)
			if err != nil {
				t.Errorf("NewInstrument() unexpected error: %v", err)
				return
			}

			if inst.Code != tt.code {
				t.Errorf("Code = %v, want %v", inst.Code, tt.code)
			}
			if inst.Version != tt.version {
				t.Errorf("Version = %v, want %v", inst.Version, tt.version)
			}
			if inst.Precision != tt.precision {
				t.Errorf("Precision = %v, want %v", inst.Precision, tt.precision)
			}
		})
	}
}

func TestNewInstrument_EmptyCode(t *testing.T) {
	_, err := NewInstrument("", 1, "CURRENCY", 2)
	if !errors.Is(err, ErrEmptyCode) {
		t.Errorf("expected ErrEmptyCode, got: %v", err)
	}
}

func TestNewInstrument_InvalidCodeFormat(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{"starts with number", "1USD"},
		{"starts with underscore", "_USD"},
		{"lowercase letters", "usd"},
		{"mixed case", "Usd"},
		{"contains lowercase", "USd"},
		{"contains hyphen", "US-D"},
		{"contains space", "US D"},
		{"contains dot", "US.D"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewInstrument(tt.code, 1, "CURRENCY", 2)
			if !errors.Is(err, ErrInvalidCodeFormat) {
				t.Errorf("expected ErrInvalidCodeFormat for code %q, got: %v", tt.code, err)
			}
		})
	}
}

func TestNewInstrument_CodeTooLong(t *testing.T) {
	longCode := "ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567" // 33 chars
	_, err := NewInstrument(longCode, 1, "CURRENCY", 2)
	if !errors.Is(err, ErrCodeTooLong) {
		t.Errorf("expected ErrCodeTooLong, got: %v", err)
	}
}

func TestNewInstrument_InvalidDimension(t *testing.T) {
	tests := []struct {
		name      string
		dimension string
	}{
		{"empty dimension", ""},
		{"unknown dimension", "UNKNOWN"},
		{"misspelled", "CURRENC"},
		{"unspecified", "UNSPECIFIED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewInstrument("USD", 1, tt.dimension, 2)
			if !errors.Is(err, ErrInvalidDimension) {
				t.Errorf("expected ErrInvalidDimension for dimension %q, got: %v", tt.dimension, err)
			}
		})
	}
}

func TestNewInstrument_NegativePrecision(t *testing.T) {
	_, err := NewInstrument("USD", 1, "CURRENCY", -1)
	if !errors.Is(err, ErrNegativePrecision) {
		t.Errorf("expected ErrNegativePrecision, got: %v", err)
	}
}

func TestNewInstrument_PrecisionTooHigh(t *testing.T) {
	_, err := NewInstrument("USD", 1, "CURRENCY", 19)
	if !errors.Is(err, ErrPrecisionTooHigh) {
		t.Errorf("expected ErrPrecisionTooHigh, got: %v", err)
	}
}

func TestInstrument_Equal(t *testing.T) {
	tests := []struct {
		name     string
		inst1    Instrument
		inst2    Instrument
		expected bool
	}{
		{
			name:     "same code and version",
			inst1:    Instrument{Code: "USD", Version: 1, Dimension: "CURRENCY", Precision: 2},
			inst2:    Instrument{Code: "USD", Version: 1, Dimension: "CURRENCY", Precision: 2},
			expected: true,
		},
		{
			name:     "different precision still equal",
			inst1:    Instrument{Code: "USD", Version: 1, Dimension: "CURRENCY", Precision: 2},
			inst2:    Instrument{Code: "USD", Version: 1, Dimension: "CURRENCY", Precision: 4},
			expected: true,
		},
		{
			name:     "different dimension still equal",
			inst1:    Instrument{Code: "USD", Version: 1, Dimension: "CURRENCY", Precision: 2},
			inst2:    Instrument{Code: "USD", Version: 1, Dimension: "ENERGY", Precision: 2},
			expected: true,
		},
		{
			name:     "different code not equal",
			inst1:    Instrument{Code: "USD", Version: 1, Dimension: "CURRENCY", Precision: 2},
			inst2:    Instrument{Code: "EUR", Version: 1, Dimension: "CURRENCY", Precision: 2},
			expected: false,
		},
		{
			name:     "different version not equal",
			inst1:    Instrument{Code: "USD", Version: 1, Dimension: "CURRENCY", Precision: 2},
			inst2:    Instrument{Code: "USD", Version: 2, Dimension: "CURRENCY", Precision: 2},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.inst1.Equal(tt.inst2); got != tt.expected {
				t.Errorf("Equal() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestInstrument_String(t *testing.T) {
	inst := Instrument{Code: "USD", Version: 1}
	expected := "USD(v1)"
	if got := inst.String(); got != expected {
		t.Errorf("String() = %v, want %v", got, expected)
	}

	inst2 := Instrument{Code: "GPU_HOUR", Version: 42}
	expected2 := "GPU_HOUR(v42)"
	if got := inst2.String(); got != expected2 {
		t.Errorf("String() = %v, want %v", got, expected2)
	}
}

func TestInstrument_IsMonetary(t *testing.T) {
	tests := []struct {
		dimension string
		expected  bool
	}{
		{"CURRENCY", true},
		{"ENERGY", false},
		{"COMPUTE", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.dimension, func(t *testing.T) {
			inst := Instrument{Code: "TEST", Version: 1, Dimension: tt.dimension}
			if got := inst.IsMonetary(); got != tt.expected {
				t.Errorf("IsMonetary() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestInstrument_IsCommodity(t *testing.T) {
	tests := []struct {
		dimension string
		expected  bool
	}{
		{"CURRENCY", false},
		{"ENERGY", true},
		{"COMPUTE", true},
		{"CARBON", true},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.dimension, func(t *testing.T) {
			inst := Instrument{Code: "TEST", Version: 1, Dimension: tt.dimension}
			if got := inst.IsCommodity(); got != tt.expected {
				t.Errorf("IsCommodity() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestInstrument_Validate(t *testing.T) {
	t.Run("valid instrument", func(t *testing.T) {
		inst := Instrument{Code: "USD", Version: 1, Dimension: "CURRENCY", Precision: 2}
		if err := inst.Validate(); err != nil {
			t.Errorf("Validate() unexpected error: %v", err)
		}
	})

	t.Run("invalid code", func(t *testing.T) {
		inst := Instrument{Code: "usd", Version: 1, Dimension: "CURRENCY", Precision: 2}
		if err := inst.Validate(); !errors.Is(err, ErrInvalidCodeFormat) {
			t.Errorf("Validate() expected ErrInvalidCodeFormat, got: %v", err)
		}
	})

	t.Run("invalid dimension", func(t *testing.T) {
		inst := Instrument{Code: "USD", Version: 1, Dimension: "INVALID", Precision: 2}
		if err := inst.Validate(); !errors.Is(err, ErrInvalidDimension) {
			t.Errorf("Validate() expected ErrInvalidDimension, got: %v", err)
		}
	})

	t.Run("invalid precision", func(t *testing.T) {
		inst := Instrument{Code: "USD", Version: 1, Dimension: "CURRENCY", Precision: 99}
		if err := inst.Validate(); !errors.Is(err, ErrPrecisionTooHigh) {
			t.Errorf("Validate() expected ErrPrecisionTooHigh, got: %v", err)
		}
	})
}

func TestValidDimensions_AllProtoValues(t *testing.T) {
	// Verify all proto enum values are represented
	expectedDimensions := []string{
		"CURRENCY",
		"ENERGY",
		"MASS",
		"VOLUME",
		"TIME",
		"COMPUTE",
		"CARBON",
		"DATA",
		"COUNT",
	}

	for _, dim := range expectedDimensions {
		if !ValidDimensions[dim] {
			t.Errorf("dimension %q missing from ValidDimensions", dim)
		}
	}

	// Verify no extra dimensions
	if len(ValidDimensions) != len(expectedDimensions) {
		t.Errorf("ValidDimensions has %d entries, expected %d", len(ValidDimensions), len(expectedDimensions))
	}
}

func TestInstrumentCodePattern_Examples(t *testing.T) {
	validCodes := []string{
		"USD", "EUR", "GBP", "JPY", "BTC",
		"KWH", "MWH", "THERM",
		"GPU_HOUR", "CPU_SECOND",
		"TONNE_CO2E",
		"A", "Z", "A1", "Z9",
		"ISO4217_USD",
	}

	for _, code := range validCodes {
		if !InstrumentCodePattern.MatchString(code) {
			t.Errorf("valid code %q should match pattern", code)
		}
	}

	invalidCodes := []string{
		"", "1", "1USD", "_USD",
		"usd", "Usd", "UsD",
		"US-D", "US D", "US.D",
	}

	for _, code := range invalidCodes {
		if InstrumentCodePattern.MatchString(code) {
			t.Errorf("invalid code %q should not match pattern", code)
		}
	}
}
