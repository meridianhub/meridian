// Package ports provides centralized port constant definitions for all Meridian services.
//
// This package eliminates scattered hardcoded port numbers across the codebase,
// providing a single source of truth for service port assignments. All services,
// Kubernetes manifests, Tilt configurations, and integration tests should reference
// these constants instead of hardcoding port numbers.
//
// # Port Allocation Strategy
//
// Meridian uses the following port allocation scheme:
//
//	50051-50099: gRPC services (internal, service-to-service communication)
//	8080:        HTTP/Connect gateway (external, client-facing)
//	8081:        HTTP health checks (internal, Kubernetes probes)
//	9090:        Prometheus metrics (internal, observability)
//
// # gRPC Service Ports (50051-50099)
//
// Each BIAN domain service receives a dedicated gRPC port in the 50051-50099 range.
// These ports are used for internal service-to-service communication and are not
// exposed externally. The Connect/gRPC-Web gateway handles external client traffic.
//
// Port assignments follow service creation order and should not be reused
// even if a service is deprecated, to avoid confusion in logs and traces.
//
// # Adding New Services
//
// When adding a new BIAN domain service:
//  1. Allocate the next available port in the 50051-50099 range
//  2. Add a constant here with comprehensive documentation
//  3. Update Kubernetes manifests and Tilt configurations
//  4. Update the gateway's service routing configuration
//
// # Usage Example
//
//	import "github.com/meridianhub/meridian/shared/platform/ports"
//
//	func main() {
//	    addr := fmt.Sprintf(":%d", ports.CurrentAccount)
//	    lis, err := net.Listen("tcp", addr)
//	    if err != nil {
//	        log.Fatalf("failed to listen: %v", err)
//	    }
//	    // ...
//	}
package ports

// gRPC service ports (internal, service-to-service communication).
// These ports are used for inter-service gRPC calls within the Kubernetes cluster.
// They are not exposed externally; the Connect gateway handles external traffic.
const (
	// CurrentAccount is the gRPC port for the Current Account service.
	// BIAN Service Domain: Current Account
	// Protocol: gRPC (internal)
	// Visibility: Cluster-internal only
	CurrentAccount = 50051

	// FinancialAccounting is the gRPC port for the Financial Accounting service.
	// BIAN Service Domain: Financial Accounting
	// Protocol: gRPC (internal)
	// Visibility: Cluster-internal only
	FinancialAccounting = 50052

	// PositionKeeping is the gRPC port for the Position Keeping service.
	// BIAN Service Domain: Position Keeping
	// Protocol: gRPC (internal)
	// Visibility: Cluster-internal only
	PositionKeeping = 50053

	// PaymentOrder is the gRPC port for the Payment Order service.
	// BIAN Service Domain: Payment Order
	// Protocol: gRPC (internal)
	// Visibility: Cluster-internal only
	PaymentOrder = 50054

	// Party is the gRPC port for the Party service.
	// BIAN Service Domain: Party Reference Data Directory
	// Protocol: gRPC (internal)
	// Visibility: Cluster-internal only
	Party = 50055

	// Tenant is the gRPC port for the Tenant service.
	// Platform service for multi-tenant isolation management.
	// Protocol: gRPC (internal)
	// Visibility: Cluster-internal only
	Tenant = 50056

	// InternalAccount is the gRPC port for the Internal Account service.
	// BIAN Service Domain: Internal Account
	// Manages counterparty and operational accounts for internal accounting.
	// Protocol: gRPC (internal)
	// Visibility: Cluster-internal only
	InternalAccount = 50057

	// MarketInformation is the gRPC port for the Market Information service.
	// BIAN Service Domain: Market Information Management
	// Manages price benchmarks, market data feeds, and reference prices.
	// Protocol: gRPC (internal)
	// Visibility: Cluster-internal only
	MarketInformation = 50058

	// ReferenceData is the gRPC port for the Reference Data service.
	// BIAN Service Domain: Reference Data Directory
	// Manages instrument definitions, validation rules, and fungibility expressions.
	// Protocol: gRPC (internal)
	// Visibility: Cluster-internal only
	ReferenceData = 50059

	// Reconciliation is the gRPC port for the Account Reconciliation service.
	// BIAN Service Domain: Account Reconciliation
	// Manages reconciliation of account positions across services.
	// Protocol: gRPC (internal)
	// Visibility: Cluster-internal only
	Reconciliation = 50060

	// Forecasting is the gRPC port for the Forecasting service.
	// Manages forecasting strategies that generate forward curves from market data.
	// Protocol: gRPC (internal)
	// Visibility: Cluster-internal only
	Forecasting = 50061

	// ControlPlane is the gRPC port for the Control Plane service.
	// Manages manifest application, validation, and diffing.
	// Protocol: gRPC (internal)
	// Visibility: Cluster-internal only
	ControlPlane = 50062
)

// HTTP service ports (various protocols).
const (
	// Gateway is the HTTP/Connect port for the API gateway.
	// This is the primary external-facing port for client applications.
	// Supports HTTP/1.1, HTTP/2, gRPC-Web, and Connect protocol.
	// Protocol: HTTP/Connect (external)
	// Visibility: Exposed via Ingress/LoadBalancer
	Gateway = 8080

	// HTTPHealth is the port for HTTP health check endpoints.
	// Used by Kubernetes liveness and readiness probes.
	// Exposes /healthz and /readyz endpoints.
	// Protocol: HTTP (internal)
	// Visibility: Cluster-internal only (used by kubelet)
	HTTPHealth = 8081

	// HTTPMetrics is the port for Prometheus metrics endpoints.
	// Exposes /metrics endpoint for scraping by Prometheus.
	// Protocol: HTTP (internal)
	// Visibility: Cluster-internal only (scraped by Prometheus)
	HTTPMetrics = 9090
)
