// Package health provides health checking capabilities for services and their dependencies.
//
// A [Checker] aggregates the health of multiple named components (database, Redis,
// upstream gRPC services, etc.) and exposes the result as a single composite
// [Status]. The HTTP handler serves a JSON health endpoint compatible with
// Kubernetes liveness and readiness probes.
//
// # Status Values
//
//   - StatusHealthy:   component is fully operational
//   - StatusDegraded:  operational with reduced functionality
//   - StatusUnhealthy: not operational
//   - StatusUnknown:   status cannot be determined
//
// # Usage
//
//	checker := health.NewChecker()
//	checker.Register("database", dbHealthFunc)
//	checker.Register("redis",    redisHealthFunc)
//
//	http.Handle("/healthz", health.NewHTTPHandler(checker))
package health
