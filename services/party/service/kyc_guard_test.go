package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/lib/pq"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

const testKYCTenantID = "test_kyc"

// setupKYCTest creates a test database for KYC integration tests
func setupKYCTest(t *testing.T) (*Service, *gorm.DB, context.Context, func()) {
	t.Helper()

	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.PartyEntity{},
		&audit.AuditOutbox{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(testKYCTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the party table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party (
		id UUID PRIMARY KEY,
		party_type VARCHAR(20) NOT NULL,
		legal_name VARCHAR(255) NOT NULL,
		display_name VARCHAR(255),
		status VARCHAR(20) NOT NULL,
		external_reference VARCHAR(255),
		external_reference_type VARCHAR(50),
		attributes JSONB NOT NULL DEFAULT '[]'::jsonb,
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		deleted_at TIMESTAMP WITH TIME ZONE,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		UNIQUE(external_reference, external_reference_type)
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the audit_outbox table
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.audit_outbox (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		table_name VARCHAR(100) NOT NULL,
		operation VARCHAR(10) NOT NULL,
		record_id VARCHAR(50) NOT NULL,
		old_values TEXT,
		new_values TEXT,
		status VARCHAR(20) NOT NULL DEFAULT 'pending',
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		retry_count INT NOT NULL DEFAULT 0,
		last_error TEXT,
		changed_by VARCHAR(100),
		transaction_id VARCHAR(100),
		client_ip VARCHAR(45),
		user_agent TEXT
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc, err := NewService(repo, logger)
	require.NoError(t, err, "Failed to create service")

	return svc, db, ctx, cleanup
}

func TestExchangeDemographics_ProductionSafety(t *testing.T) {
	tests := []struct {
		name            string
		environment     string
		expectError     bool
		expectErrorCode codes.Code
	}{
		{
			name:            "production without provider should reject",
			environment:     "production",
			expectError:     true,
			expectErrorCode: codes.Unimplemented,
		},
		{
			name:            "development without provider should return stub",
			environment:     "development",
			expectError:     false,
			expectErrorCode: codes.OK,
		},
		{
			name:            "staging without provider should reject",
			environment:     "staging",
			expectError:     true,
			expectErrorCode: codes.Unimplemented,
		},
		{
			name:            "test without provider should return stub",
			environment:     "test",
			expectError:     false,
			expectErrorCode: codes.OK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			svc, _, ctx, cleanup := setupKYCTest(t)
			defer cleanup()

			// Set environment variable
			os.Setenv("ENVIRONMENT", tt.environment)
			defer os.Unsetenv("ENVIRONMENT")

			// No verification provider configured - tests stub fallback behavior

			// Create test party using RegisterParty
			registerReq := &pb.RegisterPartyRequest{
				PartyType: pb.PartyType_PARTY_TYPE_PERSON,
				LegalName: "Test Party",
			}
			registerResp, err := svc.RegisterParty(ctx, registerReq)
			require.NoError(t, err)
			partyID := registerResp.Party.PartyId

			// Execute ExchangeDemographics
			req := &pb.ExchangeDemographicsRequest{
				PartyId:          partyID,
				VerificationData: "test_verification_data",
			}

			resp, err := svc.ExchangeDemographics(ctx, req)

			// Verify
			if tt.expectError {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok, "error should be a gRPC status error")
				assert.Equal(t, tt.expectErrorCode, st.Code())
				assert.Contains(t, st.Message(), "KYC/AML verification not implemented")
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
				assert.Equal(t, partyID, resp.PartyId)
				assert.Equal(t, "VERIFIED", resp.VerificationStatus)
				assert.NotNil(t, resp.VerificationTimestamp)
			}
		})
	}
}

func TestExchangeDemographics_ErrorMessages(t *testing.T) {
	tests := []struct {
		name              string
		environment       string
		expectedInMessage string
	}{
		{
			name:              "production error should mention no provider",
			environment:       "production",
			expectedInMessage: "no verification provider configured",
		},
		{
			name:              "production error should mention KYC/AML",
			environment:       "production",
			expectedInMessage: "KYC/AML verification not implemented",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			svc, _, ctx, cleanup := setupKYCTest(t)
			defer cleanup()

			os.Setenv("ENVIRONMENT", tt.environment)
			defer os.Unsetenv("ENVIRONMENT")

			// No verification provider configured

			// Create test party
			registerReq := &pb.RegisterPartyRequest{
				PartyType: pb.PartyType_PARTY_TYPE_PERSON,
				LegalName: "Test Party",
			}
			registerResp, err := svc.RegisterParty(ctx, registerReq)
			require.NoError(t, err)
			partyID := registerResp.Party.PartyId

			// Execute
			req := &pb.ExchangeDemographicsRequest{
				PartyId:          partyID,
				VerificationData: "test_verification_data",
			}

			_, err = svc.ExchangeDemographics(ctx, req)

			// Verify error message
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Contains(t, st.Message(), tt.expectedInMessage)
		})
	}
}
