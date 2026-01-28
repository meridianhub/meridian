package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

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
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create the party table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.party (
		id UUID PRIMARY KEY,
		party_type VARCHAR(20) NOT NULL,
		legal_name VARCHAR(255) NOT NULL,
		display_name VARCHAR(255),
		status VARCHAR(20) NOT NULL,
		external_reference VARCHAR(255),
		external_reference_type VARCHAR(50),
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		deleted_at TIMESTAMP WITH TIME ZONE,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		UNIQUE(external_reference, external_reference_type)
	)`, schemaName)).Error
	require.NoError(t, err)

	// Create the audit_outbox table
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.audit_outbox (
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
	)`, schemaName)).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
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
		kycStubEnabled  string
		expectError     bool
		expectErrorCode codes.Code
	}{
		{
			name:            "production without flag should reject",
			environment:     "production",
			kycStubEnabled:  "",
			expectError:     true,
			expectErrorCode: codes.Unimplemented,
		},
		{
			name:            "production with explicit flag should allow with warning",
			environment:     "production",
			kycStubEnabled:  "true",
			expectError:     false,
			expectErrorCode: codes.OK,
		},
		{
			name:            "development without flag should allow",
			environment:     "development",
			kycStubEnabled:  "",
			expectError:     false,
			expectErrorCode: codes.OK,
		},
		{
			name:            "development with flag should allow with warning",
			environment:     "development",
			kycStubEnabled:  "true",
			expectError:     false,
			expectErrorCode: codes.OK,
		},
		{
			name:            "staging without flag should allow",
			environment:     "staging",
			kycStubEnabled:  "",
			expectError:     false,
			expectErrorCode: codes.OK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			svc, _, ctx, cleanup := setupKYCTest(t)
			defer cleanup()

			// Set environment variables
			if tt.environment != "" {
				os.Setenv("ENVIRONMENT", tt.environment)
				defer os.Unsetenv("ENVIRONMENT")
			}
			if tt.kycStubEnabled != "" {
				os.Setenv("KYC_STUB_ENABLED", tt.kycStubEnabled)
				defer os.Unsetenv("KYC_STUB_ENABLED")
			} else {
				os.Unsetenv("KYC_STUB_ENABLED")
			}

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
		kycStubEnabled    string
		expectedInMessage string
	}{
		{
			name:              "production error should be clear",
			environment:       "production",
			kycStubEnabled:    "",
			expectedInMessage: "cannot operate in production without external provider integration",
		},
		{
			name:              "production error should mention KYC/AML",
			environment:       "production",
			kycStubEnabled:    "",
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

			if tt.kycStubEnabled != "" {
				os.Setenv("KYC_STUB_ENABLED", tt.kycStubEnabled)
				defer os.Unsetenv("KYC_STUB_ENABLED")
			} else {
				os.Unsetenv("KYC_STUB_ENABLED")
			}

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
