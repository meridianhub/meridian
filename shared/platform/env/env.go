package env

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// GetEnvOrDefault returns the environment variable value or the default if empty.
// The value is trimmed of leading and trailing whitespace.
func GetEnvOrDefault(key, defaultValue string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	return value
}

// GetEnvAsInt returns the environment variable value as int or the default if
// empty or invalid. Uses strconv.Atoi for parsing.
func GetEnvAsInt(key string, defaultValue int) int {
	valueStr := strings.TrimSpace(os.Getenv(key))
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

// GetEnvAsUint32 returns the environment variable value as uint32 or the default
// if empty or invalid. Uses strconv.ParseUint with base 10 and 32-bit size.
func GetEnvAsUint32(key string, defaultValue uint32) uint32 {
	valueStr := strings.TrimSpace(os.Getenv(key))
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.ParseUint(valueStr, 10, 32)
	if err != nil {
		return defaultValue
	}
	return uint32(value)
}

// GetEnvAsFloat returns the environment variable value as float64 or the default
// if empty or invalid. Uses strconv.ParseFloat with 64-bit precision.
func GetEnvAsFloat(key string, defaultValue float64) float64 {
	valueStr := strings.TrimSpace(os.Getenv(key))
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return defaultValue
	}
	return value
}

// GetEnvAsBool returns the environment variable value as bool or the default
// if empty or invalid. Uses strconv.ParseBool which accepts:
//   - true: "1", "t", "T", "true", "TRUE", "True"
//   - false: "0", "f", "F", "false", "FALSE", "False"
func GetEnvAsBool(key string, defaultValue bool) bool {
	valueStr := strings.TrimSpace(os.Getenv(key))
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.ParseBool(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

// GetEnvAsDuration returns the environment variable value as time.Duration or
// the default if empty or invalid. Uses time.ParseDuration which accepts
// formats like "300ms", "1.5h", "2h45m", etc.
func GetEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	valueStr := strings.TrimSpace(os.Getenv(key))
	if valueStr == "" {
		return defaultValue
	}

	value, err := time.ParseDuration(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

// GetEnvAsSlice returns the environment variable value as a string slice or
// the default if empty. The value is split by comma and each element is trimmed
// of whitespace. Empty elements after trimming are excluded.
func GetEnvAsSlice(key string, defaultValue []string) []string {
	valueStr := strings.TrimSpace(os.Getenv(key))
	if valueStr == "" {
		return defaultValue
	}

	var result []string
	parts := strings.Split(valueStr, ",")
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	if len(result) == 0 {
		return defaultValue
	}
	return result
}

// IsProduction returns true if the ENVIRONMENT variable indicates a production
// environment. Recognized production values (case-insensitive): "production", "prod".
// Returns false for all other values including empty string.
func IsProduction() bool {
	environment := strings.ToLower(strings.TrimSpace(os.Getenv("ENVIRONMENT")))
	return environment == "production" || environment == "prod"
}
