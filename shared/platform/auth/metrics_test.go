package auth

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

func TestRecordTenantMismatch(t *testing.T) {
	// Reset counter state for each test by recording known values
	// Note: promauto counters can't be reset, so we test increments

	t.Run("increments counter with correct labels", func(t *testing.T) {
		// Get initial count
		initialCount := testutil.ToFloat64(tenantMismatchTotal.WithLabelValues("acme_bank", "other_bank"))

		// Record a mismatch
		RecordTenantMismatch("acme_bank", "other_bank")

		// Verify count increased by 1
		newCount := testutil.ToFloat64(tenantMismatchTotal.WithLabelValues("acme_bank", "other_bank"))
		assert.Equal(t, initialCount+1, newCount)
	})

	t.Run("tracks different tenant combinations separately", func(t *testing.T) {
		// Get initial counts
		initialCount1 := testutil.ToFloat64(tenantMismatchTotal.WithLabelValues("tenant_a", "tenant_b"))
		initialCount2 := testutil.ToFloat64(tenantMismatchTotal.WithLabelValues("tenant_c", "tenant_d"))

		// Record mismatches for different combinations
		RecordTenantMismatch("tenant_a", "tenant_b")
		RecordTenantMismatch("tenant_a", "tenant_b")
		RecordTenantMismatch("tenant_c", "tenant_d")

		// Verify counts
		newCount1 := testutil.ToFloat64(tenantMismatchTotal.WithLabelValues("tenant_a", "tenant_b"))
		newCount2 := testutil.ToFloat64(tenantMismatchTotal.WithLabelValues("tenant_c", "tenant_d"))

		assert.Equal(t, initialCount1+2, newCount1)
		assert.Equal(t, initialCount2+1, newCount2)
	})
}

func TestExtractClientIP(t *testing.T) {
	t.Run("returns empty string for nil context", func(t *testing.T) {
		var nilCtx context.Context
		ip := extractClientIP(nilCtx)
		assert.Empty(t, ip)
	})

	t.Run("returns empty string for context without metadata or peer", func(t *testing.T) {
		ctx := context.Background()
		ip := extractClientIP(ctx)
		assert.Empty(t, ip)
	})

	t.Run("extracts IP from x-forwarded-for header", func(t *testing.T) {
		md := metadata.Pairs("x-forwarded-for", "192.168.1.100")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		ip := extractClientIP(ctx)
		assert.Equal(t, "192.168.1.100", ip)
	})

	t.Run("extracts first IP from x-forwarded-for chain", func(t *testing.T) {
		// x-forwarded-for may contain multiple IPs: client, proxy1, proxy2
		md := metadata.Pairs("x-forwarded-for", "192.168.1.100, 10.0.0.1, 10.0.0.2")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		ip := extractClientIP(ctx)
		assert.Equal(t, "192.168.1.100", ip)
	})

	t.Run("extracts IP from x-real-ip header", func(t *testing.T) {
		md := metadata.Pairs("x-real-ip", "192.168.1.200")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		ip := extractClientIP(ctx)
		assert.Equal(t, "192.168.1.200", ip)
	})

	t.Run("prefers x-forwarded-for over x-real-ip", func(t *testing.T) {
		md := metadata.Pairs(
			"x-forwarded-for", "192.168.1.100",
			"x-real-ip", "192.168.1.200",
		)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		ip := extractClientIP(ctx)
		assert.Equal(t, "192.168.1.100", ip)
	})

	t.Run("falls back to x-real-ip when x-forwarded-for is empty", func(t *testing.T) {
		md := metadata.Pairs(
			"x-forwarded-for", "",
			"x-real-ip", "192.168.1.200",
		)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		ip := extractClientIP(ctx)
		assert.Equal(t, "192.168.1.200", ip)
	})

	t.Run("extracts IP from peer address", func(t *testing.T) {
		ctx := peer.NewContext(context.Background(), &peer.Peer{
			Addr: &net.TCPAddr{
				IP:   net.ParseIP("192.168.1.50"),
				Port: 50051,
			},
		})

		ip := extractClientIP(ctx)
		assert.Equal(t, "192.168.1.50", ip)
	})

	t.Run("prefers headers over peer address", func(t *testing.T) {
		md := metadata.Pairs("x-forwarded-for", "192.168.1.100")
		ctx := metadata.NewIncomingContext(context.Background(), md)
		ctx = peer.NewContext(ctx, &peer.Peer{
			Addr: &net.TCPAddr{
				IP:   net.ParseIP("10.0.0.1"),
				Port: 50051,
			},
		})

		ip := extractClientIP(ctx)
		assert.Equal(t, "192.168.1.100", ip)
	})

	t.Run("handles IPv6 addresses in header", func(t *testing.T) {
		md := metadata.Pairs("x-forwarded-for", "2001:db8::1")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		ip := extractClientIP(ctx)
		assert.Equal(t, "2001:db8::1", ip)
	})

	t.Run("handles IPv6 peer addresses with port", func(t *testing.T) {
		// IPv6 addresses with port are formatted as "[ip]:port"
		ctx := peer.NewContext(context.Background(), &peer.Peer{
			Addr: &net.TCPAddr{
				IP:   net.ParseIP("2001:db8::1"),
				Port: 50051,
			},
		})

		ip := extractClientIP(ctx)
		assert.Equal(t, "2001:db8::1", ip)
	})

	t.Run("handles peer with nil address", func(t *testing.T) {
		ctx := peer.NewContext(context.Background(), &peer.Peer{
			Addr: nil,
		})

		ip := extractClientIP(ctx)
		assert.Empty(t, ip)
	})
}

// TestTenantMismatchMetricsIntegration verifies that the tenant mismatch
// detection in the interceptor correctly records metrics.
func TestTenantMismatchMetricsIntegration(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := NewJWTValidator(publicKey)
	require.NoError(t, err)

	t.Run("records metric on tenant mismatch", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Get initial count for this label combination
		initialCount := testutil.ToFloat64(tenantMismatchTotal.WithLabelValues("acme_bank", "other_bank"))

		// Create token with tenant claim
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		// Create context with MISMATCHED tenants
		md := metadata.Pairs(
			"authorization", "Bearer "+tokenString,
			"x-tenant-id", "other_bank",
		)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		// Call interceptor
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		_, err = interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		// Should fail with permission denied
		assert.Error(t, err)

		// Verify metric was recorded
		newCount := testutil.ToFloat64(tenantMismatchTotal.WithLabelValues("acme_bank", "other_bank"))
		assert.Equal(t, initialCount+1, newCount, "tenant mismatch metric should be incremented")
	})

	t.Run("no metric on successful validation", func(t *testing.T) {
		cfg := &InterceptorConfig{
			Validator: validator,
		}

		interceptor, err := NewAuthInterceptor(cfg)
		require.NoError(t, err)

		// Get initial count
		initialCount := testutil.ToFloat64(tenantMismatchTotal.WithLabelValues("matching_tenant", "matching_tenant"))

		// Create token with tenant claim
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "matching_tenant",
			Roles:    []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		// Create context with MATCHING tenants
		md := metadata.Pairs(
			"authorization", "Bearer "+tokenString,
			"x-tenant-id", "matching_tenant",
		)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		// Call interceptor
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		_, err = interceptor.UnaryInterceptor()(ctx, nil, info, mockUnaryHandler)

		// Should succeed
		assert.NoError(t, err)

		// Verify metric was NOT recorded (count unchanged)
		newCount := testutil.ToFloat64(tenantMismatchTotal.WithLabelValues("matching_tenant", "matching_tenant"))
		assert.Equal(t, initialCount, newCount, "tenant mismatch metric should not be incremented on success")
	})
}
