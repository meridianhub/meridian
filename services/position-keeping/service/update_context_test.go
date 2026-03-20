package service_test

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	"github.com/meridianhub/meridian/services/position-keeping/service"
)

func TestExtractIPAddress(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected string
	}{
		{
			name:     "nil context",
			ctx:      nil,
			expected: "",
		},
		{
			name:     "empty context",
			ctx:      context.Background(),
			expected: "",
		},
		{
			name: "x-forwarded-for header",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"x-forwarded-for", "203.0.113.50, 70.41.3.18, 150.172.238.178",
			)),
			expected: "203.0.113.50",
		},
		{
			name: "x-forwarded-for single IP",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"x-forwarded-for", "10.0.0.1",
			)),
			expected: "10.0.0.1",
		},
		{
			name: "x-real-ip header",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"x-real-ip", "192.168.1.100",
			)),
			expected: "192.168.1.100",
		},
		{
			name: "x-forwarded-for takes priority over x-real-ip",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"x-forwarded-for", "10.0.0.1",
				"x-real-ip", "192.168.1.100",
			)),
			expected: "10.0.0.1",
		},
		{
			name: "peer address fallback",
			ctx: peer.NewContext(context.Background(), &peer.Peer{
				Addr: &net.TCPAddr{IP: net.ParseIP("192.168.1.1"), Port: 50051},
			}),
			expected: "192.168.1.1",
		},
		{
			name: "empty x-forwarded-for falls back to x-real-ip",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"x-forwarded-for", "",
				"x-real-ip", "10.0.0.2",
			)),
			expected: "10.0.0.2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.ExtractIPAddressForTesting(tt.ctx)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractSystemContext(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected map[string]string
	}{
		{
			name: "nil context returns service only",
			ctx:  nil,
			expected: map[string]string{
				"service": "position-keeping",
			},
		},
		{
			name: "empty context returns service only",
			ctx:  context.Background(),
			expected: map[string]string{
				"service": "position-keeping",
			},
		},
		{
			name: "x-correlation-id extracted",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"x-correlation-id", "corr-123",
			)),
			expected: map[string]string{
				"service":        "position-keeping",
				"correlation_id": "corr-123",
			},
		},
		{
			name: "correlation-id fallback",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"correlation-id", "corr-456",
			)),
			expected: map[string]string{
				"service":        "position-keeping",
				"correlation_id": "corr-456",
			},
		},
		{
			name: "x-request-id fallback",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"x-request-id", "req-789",
			)),
			expected: map[string]string{
				"service":        "position-keeping",
				"correlation_id": "req-789",
			},
		},
		{
			name: "tenant ID extracted",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"x-tenant-id", "tenant-abc",
			)),
			expected: map[string]string{
				"service":   "position-keeping",
				"tenant_id": "tenant-abc",
			},
		},
		{
			name: "user-agent extracted",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"user-agent", "grpc-go/1.50.0",
			)),
			expected: map[string]string{
				"service":    "position-keeping",
				"user_agent": "grpc-go/1.50.0",
			},
		},
		{
			name: "all headers extracted",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"x-correlation-id", "corr-all",
				"x-tenant-id", "tenant-all",
				"user-agent", "test-agent",
			)),
			expected: map[string]string{
				"service":        "position-keeping",
				"correlation_id": "corr-all",
				"tenant_id":      "tenant-all",
				"user_agent":     "test-agent",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.ExtractSystemContextForTesting(tt.ctx)
			assert.Equal(t, tt.expected, result)
		})
	}
}
