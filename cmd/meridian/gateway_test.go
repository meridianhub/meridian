package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWireGateway_Config(t *testing.T) {
	// wireGateway should construct a gateway.Server with correct loopback routing
	// for all 11 services. Verify the server starts and responds to health checks.
	const grpcPort = 50099 // Use non-standard port to avoid conflicts
	const httpPort = 8099  // Use non-standard port to avoid conflicts

	databaseURL := "postgres://root@localhost:26257/defaultdb?sslmode=disable"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv := wireGateway(grpcPort, httpPort, databaseURL, logger)
	require.NotNil(t, srv)

	// Start the gateway server
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Start(ctx); err != nil {
			serverErr <- err
		}
	}()

	// Wait for server to bind (poll up to 2 seconds)
	addr := fmt.Sprintf("localhost:%d", httpPort)
	dialer := &net.Dialer{}
	var dialErr error
	for range 40 {
		select {
		case err := <-serverErr:
			t.Fatalf("gateway server failed to start: %v", err)
		default:
		}
		var conn net.Conn
		conn, dialErr = dialer.DialContext(ctx, "tcp", addr)
		if dialErr == nil {
			conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.NoError(t, dialErr, "gateway server did not start")

	client := &http.Client{Timeout: 5 * time.Second}

	// Verify /healthz returns 200
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/healthz", httpPort), nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify /health returns 200
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/health", httpPort), nil)
	require.NoError(t, err)
	resp2, err := client.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	// Graceful shutdown
	shutdownCtx := context.Background()
	require.NoError(t, srv.Shutdown(shutdownCtx))
}
