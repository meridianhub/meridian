package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	quantitypb "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// validatorsTestLogger returns a logger for validator tests.
func validatorsTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

// newGBPAccount creates a GBP CurrentAccount using the builder for testing.
func newGBPAccount(accountID string) domain.CurrentAccount {
	acc, err := domain.NewCurrentAccount(accountID, "GB82WEST12345698765432", "party-1", "GBP")
	if err != nil {
		panic("failed to create test account: " + err.Error())
	}
	return acc
}

// ---------------------------------------------------------------------------
// validateOrgPartyID
// ---------------------------------------------------------------------------

func TestValidateOrgPartyID(t *testing.T) {
	validUUID := uuid.New()

	tests := []struct {
		name       string
		input      string
		wantUUID   uuid.UUID
		wantErr    bool
		wantCode   codes.Code
		wantErrMsg string
	}{
		{
			name:     "empty string returns nil UUID",
			input:    "",
			wantUUID: uuid.Nil,
			wantErr:  false,
		},
		{
			name:     "valid UUID returns parsed UUID",
			input:    validUUID.String(),
			wantUUID: validUUID,
			wantErr:  false,
		},
		{
			name:       "invalid string returns InvalidArgument",
			input:      "not-a-uuid",
			wantUUID:   uuid.Nil,
			wantErr:    true,
			wantCode:   codes.InvalidArgument,
			wantErrMsg: "invalid org_party_id",
		},
		{
			name:       "zero UUID returns InvalidArgument",
			input:      uuid.Nil.String(),
			wantUUID:   uuid.Nil,
			wantErr:    true,
			wantCode:   codes.InvalidArgument,
			wantErrMsg: "zero UUID is not allowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateOrgPartyID(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok, "expected gRPC status error")
				assert.Equal(t, tc.wantCode, st.Code())
				assert.Contains(t, st.Message(), tc.wantErrMsg)
				assert.Equal(t, tc.wantUUID, got)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantUUID, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseDepositInput
// ---------------------------------------------------------------------------

func TestParseDepositInput(t *testing.T) {
	account := newGBPAccount("ACC-001")

	tests := []struct {
		name     string
		input    *quantitypb.InstrumentAmount
		wantErr  bool
		wantCode codes.Code
		wantMsg  string
	}{
		{
			name:     "empty amount string",
			input:    &quantitypb.InstrumentAmount{Amount: "", InstrumentCode: "GBP"},
			wantErr:  true,
			wantCode: codes.InvalidArgument,
			wantMsg:  "input.amount and input.instrument_code are required",
		},
		{
			name:     "empty instrument code",
			input:    &quantitypb.InstrumentAmount{Amount: "10.00", InstrumentCode: ""},
			wantErr:  true,
			wantCode: codes.InvalidArgument,
			wantMsg:  "input.amount and input.instrument_code are required",
		},
		{
			name:     "non-numeric amount",
			input:    &quantitypb.InstrumentAmount{Amount: "abc", InstrumentCode: "GBP"},
			wantErr:  true,
			wantCode: codes.InvalidArgument,
			wantMsg:  "invalid input amount",
		},
		{
			name:     "negative amount",
			input:    &quantitypb.InstrumentAmount{Amount: "-10.00", InstrumentCode: "GBP"},
			wantErr:  true,
			wantCode: codes.InvalidArgument,
			wantMsg:  "deposit amount must be positive",
		},
		{
			name:     "zero amount",
			input:    &quantitypb.InstrumentAmount{Amount: "0", InstrumentCode: "GBP"},
			wantErr:  true,
			wantCode: codes.InvalidArgument,
			wantMsg:  "deposit amount must be positive",
		},
		{
			name:     "instrument code mismatch",
			input:    &quantitypb.InstrumentAmount{Amount: "10.00", InstrumentCode: "USD"},
			wantErr:  true,
			wantCode: codes.InvalidArgument,
			wantMsg:  "instrument mismatch",
		},
		{
			name:     "amount exceeds precision",
			input:    &quantitypb.InstrumentAmount{Amount: "10.123", InstrumentCode: "GBP"},
			wantErr:  true,
			wantCode: codes.InvalidArgument,
			wantMsg:  "exceeds instrument precision",
		},
		{
			name:    "valid input",
			input:   &quantitypb.InstrumentAmount{Amount: "10.50", InstrumentCode: "GBP"},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			amt, opStatus, err := parseDepositInput(tc.input, account)
			if tc.wantErr {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok, "expected gRPC status error")
				assert.Equal(t, tc.wantCode, st.Code())
				assert.Contains(t, st.Message(), tc.wantMsg)
				assert.NotEmpty(t, opStatus)
			} else {
				require.NoError(t, err)
				assert.Empty(t, opStatus)
				assert.True(t, amt.IsPositive(), "expected positive amount")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// validateWithdrawalAmount
// ---------------------------------------------------------------------------

func TestValidateWithdrawalAmount(t *testing.T) {
	account := newGBPAccount("ACC-002")

	tests := []struct {
		name     string
		amount   *commonpb.MoneyAmount
		wantErr  bool
		wantCode codes.Code
		wantMsg  string
	}{
		{
			name: "currency mismatch",
			amount: &commonpb.MoneyAmount{
				Amount: &money.Money{CurrencyCode: "USD", Units: 10, Nanos: 0},
			},
			wantErr:  true,
			wantCode: codes.InvalidArgument,
			wantMsg:  "currency mismatch",
		},
		{
			name: "zero amount",
			amount: &commonpb.MoneyAmount{
				Amount: &money.Money{CurrencyCode: "GBP", Units: 0, Nanos: 0},
			},
			wantErr:  true,
			wantCode: codes.InvalidArgument,
			wantMsg:  "withdrawal amount must be positive",
		},
		{
			name: "negative amount",
			amount: &commonpb.MoneyAmount{
				Amount: &money.Money{CurrencyCode: "GBP", Units: -10, Nanos: 0},
			},
			wantErr:  true,
			wantCode: codes.InvalidArgument,
			wantMsg:  "withdrawal amount must be positive",
		},
		{
			name: "valid amount",
			amount: &commonpb.MoneyAmount{
				Amount: &money.Money{CurrencyCode: "GBP", Units: 10, Nanos: 500000000},
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			amt, opStatus, err := validateWithdrawalAmount(tc.amount, account)
			if tc.wantErr {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok, "expected gRPC status error")
				assert.Equal(t, tc.wantCode, st.Code())
				assert.Contains(t, st.Message(), tc.wantMsg)
				assert.NotEmpty(t, opStatus)
			} else {
				require.NoError(t, err)
				assert.Empty(t, opStatus)
				assert.True(t, amt.IsPositive(), "expected positive amount")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveInstrument
// ---------------------------------------------------------------------------

func TestResolveInstrument(t *testing.T) {
	tests := []struct {
		name             string
		instrumentGetter InstrumentGetter
		instrumentCode   string
		wantErr          bool
		wantCode         codes.Code
		wantMsg          string
		ctxFunc          func() context.Context
	}{
		{
			name:             "nil instrumentGetter returns FailedPrecondition",
			instrumentGetter: nil,
			instrumentCode:   "GBP",
			wantErr:          true,
			wantCode:         codes.FailedPrecondition,
			wantMsg:          "Reference Data service is required",
		},
		{
			name: "instrument not found returns InvalidArgument",
			instrumentGetter: &mockInstrumentGetter{
				instruments: map[string]*cache.CachedInstrument{},
			},
			instrumentCode: "XYZ",
			wantErr:        true,
			wantCode:       codes.InvalidArgument,
			wantMsg:        "unknown instrument_code",
		},
		{
			name: "context canceled returns Canceled",
			instrumentGetter: &mockInstrumentGetter{
				err: context.Canceled,
			},
			instrumentCode: "GBP",
			wantErr:        true,
			wantCode:       codes.Canceled,
			wantMsg:        "request canceled",
		},
		{
			name: "context deadline exceeded returns DeadlineExceeded",
			instrumentGetter: &mockInstrumentGetter{
				err: context.DeadlineExceeded,
			},
			instrumentCode: "GBP",
			wantErr:        true,
			wantCode:       codes.DeadlineExceeded,
			wantMsg:        "instrument lookup timed out",
		},
		{
			name: "other error returns Unavailable",
			instrumentGetter: &mockInstrumentGetter{
				err: errors.New("connection refused"),
			},
			instrumentCode: "GBP",
			wantErr:        true,
			wantCode:       codes.Unavailable,
			wantMsg:        "instrument lookup failed",
		},
		{
			name: "valid instrument returns resolved",
			instrumentGetter: &mockInstrumentGetter{
				instruments: map[string]*cache.CachedInstrument{
					"GBP": {Definition: &registry.InstrumentDefinition{
						Code:      "GBP",
						Dimension: "MONETARY",
						Precision: 2,
					}},
				},
			},
			instrumentCode: "GBP",
			wantErr:        false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{
				instrumentGetter: tc.instrumentGetter,
				logger:           validatorsTestLogger(),
			}

			ctx := context.Background()
			resolved, opStatus, err := svc.resolveInstrument(ctx, tc.instrumentCode, "test-account")

			if tc.wantErr {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok, "expected gRPC status error")
				assert.Equal(t, tc.wantCode, st.Code())
				assert.Contains(t, st.Message(), tc.wantMsg)
				assert.NotEmpty(t, opStatus)
				assert.Nil(t, resolved)
			} else {
				require.NoError(t, err)
				assert.Empty(t, opStatus)
				require.NotNil(t, resolved)
				assert.Equal(t, "CURRENCY", resolved.dimension) // MONETARY maps to CURRENCY
				assert.Equal(t, 2, resolved.precision)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// validatePartyForAccountCreation
// ---------------------------------------------------------------------------

// simplePartyClient is a minimal PartyClient for testing validatePartyForAccountCreation.
type simplePartyClient struct {
	validateErr error
}

func (m *simplePartyClient) ValidateParty(_ context.Context, _ string) error {
	return m.validateErr
}

func (m *simplePartyClient) GetParty(_ context.Context, _ string) (*partyv1.Party, error) {
	return nil, nil
}

func (m *simplePartyClient) Close() error { return nil }

func TestValidatePartyForAccountCreation(t *testing.T) {
	tests := []struct {
		name     string
		client   PartyClient
		wantErr  bool
		wantCode codes.Code
		wantMsg  string
	}{
		{
			name:     "party not found returns InvalidArgument",
			client:   &simplePartyClient{validateErr: ErrPartyNotFound},
			wantErr:  true,
			wantCode: codes.InvalidArgument,
			wantMsg:  "party not found",
		},
		{
			name:     "party not active returns FailedPrecondition",
			client:   &simplePartyClient{validateErr: ErrPartyNotActive},
			wantErr:  true,
			wantCode: codes.FailedPrecondition,
			wantMsg:  "party not active",
		},
		{
			name:     "other error returns Internal",
			client:   &simplePartyClient{validateErr: errors.New("connection refused")},
			wantErr:  true,
			wantCode: codes.Internal,
			wantMsg:  "party validation failed",
		},
		{
			name:    "valid party succeeds",
			client:  &simplePartyClient{validateErr: nil},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{
				partyClient: tc.client,
				logger:      validatorsTestLogger(),
			}

			opStatus, err := svc.validatePartyForAccountCreation(context.Background(), "party-1", "test-account")
			if tc.wantErr {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok, "expected gRPC status error")
				assert.Equal(t, tc.wantCode, st.Code())
				assert.Contains(t, st.Message(), tc.wantMsg)
				assert.NotEmpty(t, opStatus)
			} else {
				require.NoError(t, err)
				assert.Empty(t, opStatus)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// checkEligibility
// ---------------------------------------------------------------------------

func TestCheckEligibility_NilPartyClient(t *testing.T) {
	svc := &Service{
		partyClient: nil,
		logger:      validatorsTestLogger(),
	}

	cachedType := &CachedAccountType{}
	opStatus, err := svc.checkEligibility(context.Background(), cachedType, "party-1", nil, "SAVINGS", "test-account")
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "party service is required")
	assert.Equal(t, "eligibility_unavailable", opStatus)
}

func TestCheckEligibility_GetPartyError(t *testing.T) {
	svc := &Service{
		partyClient: &simplePartyClient{validateErr: nil},
		logger:      validatorsTestLogger(),
	}

	// simplePartyClient.GetParty returns (nil, nil) which is sufficient here -
	// the checkEligibility function calls GetParty, and if error is non-nil it
	// returns Internal. We need a client that returns an error from GetParty.
	client := &getPartyErrorClient{err: errors.New("connection refused")}
	svc.partyClient = client

	cachedType := &CachedAccountType{}
	opStatus, err := svc.checkEligibility(context.Background(), cachedType, "party-1", nil, "SAVINGS", "test-account")
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "eligibility check failed")
	assert.Equal(t, "eligibility_check_failed", opStatus)
}

// getPartyErrorClient is a PartyClient that returns an error from GetParty.
type getPartyErrorClient struct {
	err error
}

func (m *getPartyErrorClient) ValidateParty(_ context.Context, _ string) error { return nil }
func (m *getPartyErrorClient) GetParty(_ context.Context, _ string) (*partyv1.Party, error) {
	return nil, m.err
}
func (m *getPartyErrorClient) Close() error { return nil }

// ---------------------------------------------------------------------------
// validateProductTypeConstraints
// ---------------------------------------------------------------------------

func TestValidateProductTypeConstraints_EligibilityCELWithNilProgram(t *testing.T) {
	svc := &Service{
		logger: validatorsTestLogger(),
	}

	def := &accounttype.Definition{
		EligibilityCEL: `party.type == "PERSON"`,
	}
	cachedType := &CachedAccountType{
		Definition:         def,
		EligibilityProgram: nil,
	}

	opStatus, err := svc.validateProductTypeConstraints(context.Background(), cachedType, "party-1", nil, "SAVINGS", "test-account")
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "eligibility rule is configured but not compiled")
	assert.Equal(t, "eligibility_not_compiled", opStatus)
}
