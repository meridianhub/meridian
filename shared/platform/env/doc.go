// Package env provides helper functions for parsing environment variables
// with default values and consistent error handling.
//
// All functions follow a consistent pattern:
//   - Get the environment variable value
//   - Trim leading/trailing whitespace
//   - Return default if empty
//   - Parse using standard library (strconv, time)
//   - Return default on parse error
//
// This design ensures graceful degradation: invalid values silently fall back
// to defaults rather than causing application crashes, making configuration
// more resilient during development and deployment.
//
// # Boolean Parsing
//
// GetEnvAsBool uses strconv.ParseBool which accepts the following values:
//   - true: "1", "t", "T", "true", "TRUE", "True"
//   - false: "0", "f", "F", "false", "FALSE", "False"
//
// Any other value (e.g., "yes", "no", "on", "off") returns the default.
//
// # Duration Parsing
//
// GetEnvAsDuration uses time.ParseDuration which accepts formats like:
//   - "300ms", "1.5s", "2m", "1h30m", "24h"
//
// Valid units: ns, us (or microsecond), ms, s, m, h.
//
// # Slice Parsing
//
// GetEnvAsSlice splits by comma and trims each element. Empty elements
// after trimming are excluded. For example:
//   - "a, b, c" -> ["a", "b", "c"]
//   - "a,,b" -> ["a", "b"]
//   - "  a  ,  b  " -> ["a", "b"]
//
// # Example Usage
//
//	import "github.com/meridianhub/meridian/shared/platform/env"
//
//	port := env.GetEnvOrDefault("PORT", "8080")
//	maxConns := env.GetEnvAsInt("MAX_CONNECTIONS", 100)
//	timeout := env.GetEnvAsDuration("TIMEOUT", 30*time.Second)
//	debug := env.GetEnvAsBool("DEBUG", false)
//	brokers := env.GetEnvAsSlice("KAFKA_BROKERS", []string{"localhost:9092"})
package env
