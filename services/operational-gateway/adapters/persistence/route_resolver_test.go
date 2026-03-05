package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDBRouteResolver_RejectsPaymentInstructionTypes(t *testing.T) {
	paymentTypes := []string{
		"payment.collect",
		"payment.initiate",
		"payment.cancel",
		"payment.refund",
		"payment.status",
	}

	// Route resolver with nil repo: payment rejection must happen before any repo call.
	resolver := NewDBRouteResolver(nil)

	for _, instrType := range paymentTypes {
		t.Run(instrType, func(t *testing.T) {
			_, err := resolver.Resolve(context.Background(), "tenant-1", instrType)
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok, "expected gRPC status error for %q", instrType)
			assert.Equal(t, codes.InvalidArgument, st.Code())
			assert.Contains(t, st.Message(), "financial-gateway")
		})
	}
}

func TestDBRouteResolver_AllowsNonPaymentInstructionTypes(t *testing.T) {
	allowedTypes := []string{
		"kyc.verify",
		"device.ping",
		"settlement.initiate",
	}

	// Use a nil DB resolver backed by a real (but empty) RouteRepository.
	// The allowed types should NOT get the financial-gateway rejection error;
	// they'll get ErrRouteNotFound (or a DB error) instead.
	db := getSharedDB(t)
	cleanTables(t, db)
	repo := NewRouteRepository(db)
	resolver := NewDBRouteResolver(repo)

	for _, instrType := range allowedTypes {
		t.Run(instrType, func(t *testing.T) {
			_, err := resolver.Resolve(context.Background(), "11111111-1111-1111-1111-111111111111", instrType)
			// Must have an error (route not found), but NOT the financial-gateway rejection.
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "financial-gateway",
				"non-payment type %q should not receive the financial-gateway rejection", instrType)
		})
	}
}
