package service_test

import (
	"context"
	"log"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/meridianhub/meridian/internal/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/internal/current-account/clients"
	"github.com/meridianhub/meridian/internal/current-account/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// ExampleHealthChecker demonstrates how to set up health checking for the CurrentAccount service.
func ExampleHealthChecker() {
	// Create dependencies (repository, clients, etc.)
	// In production, these would be properly initialized
	var repo *persistence.Repository
	var posKeepingClient clients.PositionKeepingClient
	var finAcctClient clients.FinancialAccountingClient

	// Create health checker
	healthChecker := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:                repo,
		PositionKeepingClient:     posKeepingClient,
		FinancialAccountingClient: finAcctClient,
		Logger:                    slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		ServiceName:               "current-account",
		CheckTimeout:              5 * time.Second,
	})

	// Register health checker with gRPC server
	grpcServer := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Start server using ListenConfig for context support
	lc := &net.ListenConfig{}
	lis, err := lc.Listen(context.Background(), "tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Println("Health check endpoint registered at :50051")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}

// ExampleHealthChecker_Check demonstrates performing a synchronous health check.
func ExampleHealthChecker_Check() {
	// Assume healthChecker is already created and registered
	var healthChecker *service.HealthChecker

	// Perform health check
	ctx := context.Background()
	resp, err := healthChecker.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "current-account",
	})
	if err != nil {
		log.Fatalf("Health check failed: %v", err)
	}

	switch resp.Status {
	case grpc_health_v1.HealthCheckResponse_SERVING:
		log.Println("Service is healthy and serving requests")
	case grpc_health_v1.HealthCheckResponse_NOT_SERVING:
		log.Println("Service is unhealthy and not serving requests")
	case grpc_health_v1.HealthCheckResponse_UNKNOWN:
		log.Println("Service health status is unknown")
	case grpc_health_v1.HealthCheckResponse_SERVICE_UNKNOWN:
		log.Println("Service not found")
	}
}

// ExampleHealthChecker_kubernetes demonstrates Kubernetes readiness/liveness probe configuration.
func ExampleHealthChecker_kubernetes() {
	// In your Kubernetes deployment YAML:
	//
	// apiVersion: v1
	// kind: Service
	// metadata:
	//   name: current-account
	// spec:
	//   selector:
	//     app: current-account
	//   ports:
	//   - name: grpc
	//     port: 50051
	//     targetPort: 50051
	//
	// ---
	// apiVersion: apps/v1
	// kind: Deployment
	// metadata:
	//   name: current-account
	// spec:
	//   replicas: 3
	//   selector:
	//     matchLabels:
	//       app: current-account
	//   template:
	//     metadata:
	//       labels:
	//         app: current-account
	//     spec:
	//       containers:
	//       - name: current-account
	//         image: current-account:latest
	//         ports:
	//         - containerPort: 50051
	//           name: grpc
	//         # Liveness probe: Is the service alive?
	//         # Kills and restarts the container if fails
	//         livenessProbe:
	//           grpc:
	//             port: 50051
	//             service: current-account
	//           initialDelaySeconds: 10
	//           periodSeconds: 10
	//           timeoutSeconds: 5
	//           failureThreshold: 3
	//         # Readiness probe: Is the service ready to serve traffic?
	//         # Removes from service endpoints if fails
	//         readinessProbe:
	//           grpc:
	//             port: 50051
	//             service: current-account
	//           initialDelaySeconds: 5
	//           periodSeconds: 5
	//           timeoutSeconds: 5
	//           failureThreshold: 2
}

// ExampleHealthChecker_grpccurl demonstrates checking health using grpcurl CLI.
func ExampleHealthChecker_grpccurl() {
	// Check health using grpcurl command line tool:
	//
	// # Check overall service health
	// grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check
	//
	// # Check specific service
	// grpcurl -plaintext -d '{"service":"current-account"}' localhost:50051 grpc.health.v1.Health/Check
	//
	// # Watch for health status changes (streaming)
	// grpcurl -plaintext -d '{"service":"current-account"}' localhost:50051 grpc.health.v1.Health/Watch
	//
	// Expected response (healthy):
	// {
	//   "status": "SERVING"
	// }
	//
	// Expected response (unhealthy):
	// {
	//   "status": "NOT_SERVING"
	// }
}

// ExampleHealthChecker_clientUsage demonstrates using a gRPC health check client.
func ExampleHealthChecker_clientUsage() {
	// Create gRPC connection with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	//nolint:staticcheck // Example code showing connection pattern
	conn, err := grpc.DialContext(ctx, "localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			log.Printf("Failed to close connection: %v", closeErr)
		}
	}()

	// Create health check client
	client := grpc_health_v1.NewHealthClient(conn)

	// Perform synchronous health check (reusing ctx from connection)
	checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
	defer checkCancel()

	resp, err := client.Check(checkCtx, &grpc_health_v1.HealthCheckRequest{
		Service: "current-account",
	})
	if err != nil {
		log.Fatalf("Health check failed: %v", err)
	}

	log.Printf("Service health: %v", resp.Status)

	// Stream health updates
	stream, err := client.Watch(checkCtx, &grpc_health_v1.HealthCheckRequest{
		Service: "current-account",
	})
	if err != nil {
		log.Fatalf("Failed to watch health: %v", err)
	}

	// Receive health updates
	for {
		resp, err := stream.Recv()
		if err != nil {
			log.Printf("Stream ended: %v", err)
			break
		}
		log.Printf("Health status changed: %v", resp.Status)
	}
}
