package env

import (
	"testing"
	"time"
)

func TestGetEnvOrDefault(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue string
		expected     string
	}{
		{
			name:         "returns set value",
			envValue:     "custom_value",
			setEnv:       true,
			defaultValue: "default",
			expected:     "custom_value",
		},
		{
			name:         "returns default for empty value",
			envValue:     "",
			setEnv:       true,
			defaultValue: "default",
			expected:     "default",
		},
		{
			name:         "returns default for whitespace-only value",
			envValue:     "   ",
			setEnv:       true,
			defaultValue: "default",
			expected:     "default",
		},
		{
			name:         "returns default for missing var",
			envValue:     "",
			setEnv:       false,
			defaultValue: "default",
			expected:     "default",
		},
		{
			name:         "trims surrounding spaces from value",
			envValue:     "  trimmed  ",
			setEnv:       true,
			defaultValue: "default",
			expected:     "trimmed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_GET_ENV_OR_DEFAULT"
			if tt.setEnv {
				t.Setenv(key, tt.envValue)
			}

			result := GetEnvOrDefault(key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("GetEnvOrDefault(%q, %q) = %q, expected %q",
					key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvAsInt(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue int
		expected     int
	}{
		{
			name:         "returns valid int",
			envValue:     "42",
			setEnv:       true,
			defaultValue: 0,
			expected:     42,
		},
		{
			name:         "returns zero when env is zero",
			envValue:     "0",
			setEnv:       true,
			defaultValue: 99,
			expected:     0,
		},
		{
			name:         "returns negative int",
			envValue:     "-100",
			setEnv:       true,
			defaultValue: 0,
			expected:     -100,
		},
		{
			name:         "returns default for invalid format",
			envValue:     "not_a_number",
			setEnv:       true,
			defaultValue: 123,
			expected:     123,
		},
		{
			name:         "returns default for empty value",
			envValue:     "",
			setEnv:       true,
			defaultValue: 456,
			expected:     456,
		},
		{
			name:         "returns default for whitespace-only value",
			envValue:     "   ",
			setEnv:       true,
			defaultValue: 789,
			expected:     789,
		},
		{
			name:         "returns default for overflow value",
			envValue:     "99999999999999999999",
			setEnv:       true,
			defaultValue: 111,
			expected:     111,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_GET_ENV_AS_INT"
			if tt.setEnv {
				t.Setenv(key, tt.envValue)
			}

			result := GetEnvAsInt(key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("GetEnvAsInt(%q, %d) = %d, expected %d",
					key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvAsUint32(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue uint32
		expected     uint32
	}{
		{
			name:         "returns valid uint32",
			envValue:     "42",
			setEnv:       true,
			defaultValue: 0,
			expected:     42,
		},
		{
			name:         "returns zero when env is zero",
			envValue:     "0",
			setEnv:       true,
			defaultValue: 99,
			expected:     0,
		},
		{
			name:         "returns max uint32 value",
			envValue:     "4294967295",
			setEnv:       true,
			defaultValue: 0,
			expected:     4294967295,
		},
		{
			name:         "returns default for negative value",
			envValue:     "-1",
			setEnv:       true,
			defaultValue: 100,
			expected:     100,
		},
		{
			name:         "returns default for invalid format",
			envValue:     "not_a_number",
			setEnv:       true,
			defaultValue: 123,
			expected:     123,
		},
		{
			name:         "returns default for empty value",
			envValue:     "",
			setEnv:       true,
			defaultValue: 456,
			expected:     456,
		},
		{
			name:         "returns default for overflow value",
			envValue:     "4294967296",
			setEnv:       true,
			defaultValue: 111,
			expected:     111,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_GET_ENV_AS_UINT32"
			if tt.setEnv {
				t.Setenv(key, tt.envValue)
			}

			result := GetEnvAsUint32(key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("GetEnvAsUint32(%q, %d) = %d, expected %d",
					key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvAsFloat(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue float64
		expected     float64
	}{
		{
			name:         "returns valid float",
			envValue:     "3.14159",
			setEnv:       true,
			defaultValue: 0.0,
			expected:     3.14159,
		},
		{
			name:         "returns scientific notation",
			envValue:     "1.5e10",
			setEnv:       true,
			defaultValue: 0.0,
			expected:     1.5e10,
		},
		{
			name:         "returns negative float",
			envValue:     "-2.718",
			setEnv:       true,
			defaultValue: 0.0,
			expected:     -2.718,
		},
		{
			name:         "returns default for invalid format",
			envValue:     "not_a_float",
			setEnv:       true,
			defaultValue: 1.23,
			expected:     1.23,
		},
		{
			name:         "returns default for empty value",
			envValue:     "",
			setEnv:       true,
			defaultValue: 4.56,
			expected:     4.56,
		},
		{
			name:         "returns default for whitespace-only value",
			envValue:     "   ",
			setEnv:       true,
			defaultValue: 7.89,
			expected:     7.89,
		},
		{
			name:         "returns default for invalid hex format",
			envValue:     "0x1F",
			setEnv:       true,
			defaultValue: 99.9,
			expected:     99.9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_GET_ENV_AS_FLOAT"
			if tt.setEnv {
				t.Setenv(key, tt.envValue)
			}

			result := GetEnvAsFloat(key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("GetEnvAsFloat(%q, %f) = %f, expected %f",
					key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvAsBool(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue bool
		expected     bool
	}{
		{
			name:         "returns true for '1'",
			envValue:     "1",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns true for 't'",
			envValue:     "t",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns true for 'true'",
			envValue:     "true",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns true for 'TRUE'",
			envValue:     "TRUE",
			setEnv:       true,
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns false for '0'",
			envValue:     "0",
			setEnv:       true,
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "returns false for 'f'",
			envValue:     "f",
			setEnv:       true,
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "returns false for 'false'",
			envValue:     "false",
			setEnv:       true,
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "returns false for 'FALSE'",
			envValue:     "FALSE",
			setEnv:       true,
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "returns default for 'yes'",
			envValue:     "yes",
			setEnv:       true,
			defaultValue: true,
			expected:     true,
		},
		{
			name:         "returns default for empty value",
			envValue:     "",
			setEnv:       true,
			defaultValue: true,
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_GET_ENV_AS_BOOL"
			if tt.setEnv {
				t.Setenv(key, tt.envValue)
			}

			result := GetEnvAsBool(key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("GetEnvAsBool(%q, %v) = %v, expected %v",
					key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvAsDuration(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue time.Duration
		expected     time.Duration
	}{
		{
			name:         "returns valid duration 1h30m",
			envValue:     "1h30m",
			setEnv:       true,
			defaultValue: 0,
			expected:     90 * time.Minute,
		},
		{
			name:         "returns milliseconds 500ms",
			envValue:     "500ms",
			setEnv:       true,
			defaultValue: 0,
			expected:     500 * time.Millisecond,
		},
		{
			name:         "returns default for invalid format",
			envValue:     "invalid_duration",
			setEnv:       true,
			defaultValue: 5 * time.Second,
			expected:     5 * time.Second,
		},
		{
			name:         "returns default for empty value",
			envValue:     "",
			setEnv:       true,
			defaultValue: 10 * time.Second,
			expected:     10 * time.Second,
		},
		{
			name:         "returns negative duration",
			envValue:     "-30s",
			setEnv:       true,
			defaultValue: 0,
			expected:     -30 * time.Second,
		},
		{
			name:         "returns zero duration",
			envValue:     "0s",
			setEnv:       true,
			defaultValue: 1 * time.Hour,
			expected:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_GET_ENV_AS_DURATION"
			if tt.setEnv {
				t.Setenv(key, tt.envValue)
			}

			result := GetEnvAsDuration(key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("GetEnvAsDuration(%q, %v) = %v, expected %v",
					key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvAsSlice(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		setEnv       bool
		defaultValue []string
		expected     []string
	}{
		{
			name:         "returns single value",
			envValue:     "single",
			setEnv:       true,
			defaultValue: nil,
			expected:     []string{"single"},
		},
		{
			name:         "returns comma-separated list",
			envValue:     "one,two,three",
			setEnv:       true,
			defaultValue: nil,
			expected:     []string{"one", "two", "three"},
		},
		{
			name:         "trims whitespace between commas",
			envValue:     "one , two , three",
			setEnv:       true,
			defaultValue: nil,
			expected:     []string{"one", "two", "three"},
		},
		{
			name:         "filters empty string elements",
			envValue:     "one,,three",
			setEnv:       true,
			defaultValue: nil,
			expected:     []string{"one", "three"},
		},
		{
			name:         "returns default for empty var",
			envValue:     "",
			setEnv:       true,
			defaultValue: []string{"default1", "default2"},
			expected:     []string{"default1", "default2"},
		},
		{
			name:         "returns default for only commas",
			envValue:     ",,,",
			setEnv:       true,
			defaultValue: []string{"fallback"},
			expected:     []string{"fallback"},
		},
		{
			name:         "handles mixed whitespace",
			envValue:     "  a  ,  b  ,  c  ",
			setEnv:       true,
			defaultValue: nil,
			expected:     []string{"a", "b", "c"},
		},
		{
			name:         "returns default for missing var",
			envValue:     "",
			setEnv:       false,
			defaultValue: []string{"missing"},
			expected:     []string{"missing"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_GET_ENV_AS_SLICE"
			if tt.setEnv {
				t.Setenv(key, tt.envValue)
			}

			result := GetEnvAsSlice(key, tt.defaultValue)
			if !slicesEqual(result, tt.expected) {
				t.Errorf("GetEnvAsSlice(%q, %v) = %v, expected %v",
					key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

// slicesEqual compares two string slices for equality.
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
