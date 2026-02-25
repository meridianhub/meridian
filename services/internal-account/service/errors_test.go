package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Test error to gRPC status code mappings as specified in subtask 15.3:
// - ErrAccountNotFound -> codes.NotFound
// - ErrDuplicateAccountCode -> codes.AlreadyExists
// - ErrVersionMismatch -> codes.Aborted (for optimistic locking conflicts)
// - ErrInvalidTransition -> codes.FailedPrecondition (for invalid state transitions)

// Test sentinel errors for error mapping tests.
var (
	errUnexpectedDatabase = errors.New("unexpected database error")
	errConnectionRefused  = errors.New("connection refused")
)

func TestErrorMapping_AccountNotFound_MapsToNotFound(t *testing.T) {
	err := mapDomainErrorToGRPC(domain.ErrAccountNotFound)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), domain.ErrAccountNotFound.Error())
}

func TestErrorMapping_DuplicateAccountCode_MapsToAlreadyExists(t *testing.T) {
	err := mapDomainErrorToGRPC(domain.ErrDuplicateAccountCode)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.AlreadyExists, st.Code())
	assert.Contains(t, st.Message(), domain.ErrDuplicateAccountCode.Error())
}

func TestErrorMapping_VersionMismatch_MapsToAborted(t *testing.T) {
	err := mapDomainErrorToGRPC(domain.ErrVersionMismatch)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.Aborted, st.Code())
	assert.Contains(t, st.Message(), domain.ErrVersionMismatch.Error())
}

func TestErrorMapping_InvalidStatusTransition_MapsToFailedPrecondition(t *testing.T) {
	err := mapDomainErrorToGRPC(domain.ErrInvalidStatusTransition)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), domain.ErrInvalidStatusTransition.Error())
}

func TestErrorMapping_AccountClosed_MapsToFailedPrecondition(t *testing.T) {
	err := mapDomainErrorToGRPC(domain.ErrAccountClosed)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), domain.ErrAccountClosed.Error())
}

func TestErrorMapping_AccountSuspended_MapsToFailedPrecondition(t *testing.T) {
	err := mapDomainErrorToGRPC(domain.ErrAccountSuspended)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), domain.ErrAccountSuspended.Error())
}

func TestErrorMapping_InvalidAccountType_MapsToInvalidArgument(t *testing.T) {
	err := mapDomainErrorToGRPC(domain.ErrInvalidAccountType)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), domain.ErrInvalidAccountType.Error())
}

func TestErrorMapping_CounterpartyRequired_MapsToInvalidArgument(t *testing.T) {
	err := mapDomainErrorToGRPC(domain.ErrCounterpartyRequired)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), domain.ErrCounterpartyRequired.Error())
}

func TestErrorMapping_CounterpartyNotAllowed_MapsToInvalidArgument(t *testing.T) {
	err := mapDomainErrorToGRPC(domain.ErrCounterpartyNotAllowed)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), domain.ErrCounterpartyNotAllowed.Error())
}

func TestErrorMapping_AccountIDRequired_MapsToInvalidArgument(t *testing.T) {
	err := mapDomainErrorToGRPC(domain.ErrAccountIDRequired)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), domain.ErrAccountIDRequired.Error())
}

func TestErrorMapping_AccountCodeRequired_MapsToInvalidArgument(t *testing.T) {
	err := mapDomainErrorToGRPC(domain.ErrAccountCodeRequired)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), domain.ErrAccountCodeRequired.Error())
}

func TestErrorMapping_NameRequired_MapsToInvalidArgument(t *testing.T) {
	err := mapDomainErrorToGRPC(domain.ErrNameRequired)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), domain.ErrNameRequired.Error())
}

func TestErrorMapping_UnknownError_MapsToInternal(t *testing.T) {
	err := mapDomainErrorToGRPC(errUnexpectedDatabase)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "internal error")
}

func TestErrorMapping_WrappedError_PreservesMapping(t *testing.T) {
	// Test that wrapped errors still map correctly
	wrappedErr := fmt.Errorf("operation failed: %w", domain.ErrAccountNotFound)
	err := mapDomainErrorToGRPC(wrappedErr)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.NotFound, st.Code())
}

// Test persistence layer errors that are mapped in server.go

func TestPersistenceErrorMapping_DuplicateCode_Context(t *testing.T) {
	// Verify the persistence.ErrDuplicateCode is correctly defined
	assert.NotNil(t, persistence.ErrDuplicateCode)
	assert.Equal(t, "account code already exists", persistence.ErrDuplicateCode.Error())
}

func TestPersistenceErrorMapping_VersionConflict_Context(t *testing.T) {
	// Verify the persistence.ErrVersionConflict is correctly defined
	assert.NotNil(t, persistence.ErrVersionConflict)
	assert.Contains(t, persistence.ErrVersionConflict.Error(), "version conflict")
}

// Test Position Keeping error mapping

func TestPositionKeepingErrorMapping_NotFound_MapsToInternal(t *testing.T) {
	// Position Keeping NotFound means something is wrong with our integration
	err := mapPositionKeepingErrorToGRPC(status.Error(codes.NotFound, "position not found"))
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "balance not found in position keeping")
}

func TestPositionKeepingErrorMapping_Unavailable_MapsToUnavailable(t *testing.T) {
	err := mapPositionKeepingErrorToGRPC(status.Error(codes.Unavailable, "service down"))
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.Unavailable, st.Code())
	assert.Contains(t, st.Message(), "position keeping service unavailable")
}

func TestPositionKeepingErrorMapping_DeadlineExceeded_MapsToUnavailable(t *testing.T) {
	err := mapPositionKeepingErrorToGRPC(status.Error(codes.DeadlineExceeded, "timeout"))
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestPositionKeepingErrorMapping_ResourceExhausted_MapsToUnavailable(t *testing.T) {
	err := mapPositionKeepingErrorToGRPC(status.Error(codes.ResourceExhausted, "rate limited"))
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestPositionKeepingErrorMapping_InvalidArgument_MapsToInternal(t *testing.T) {
	// InvalidArgument from PK means our code sent a bad request
	err := mapPositionKeepingErrorToGRPC(status.Error(codes.InvalidArgument, "invalid request"))
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "invalid request to position keeping")
}

func TestPositionKeepingErrorMapping_NonGRPCError_MapsToUnavailable(t *testing.T) {
	// Non-gRPC errors (connection errors, etc.) map to Unavailable
	err := mapPositionKeepingErrorToGRPC(errConnectionRefused)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestPositionKeepingErrorMapping_OtherGRPCError_MapsToInternal(t *testing.T) {
	// Other gRPC errors map to Internal
	err := mapPositionKeepingErrorToGRPC(status.Error(codes.PermissionDenied, "forbidden"))
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
}

// Service-level error tests

func TestServiceError_RepositoryNil(t *testing.T) {
	assert.NotNil(t, ErrRepositoryNil)
	assert.Equal(t, "repository cannot be nil", ErrRepositoryNil.Error())
}

func TestServiceError_PositionKeepingClientNil(t *testing.T) {
	assert.NotNil(t, ErrPositionKeepingClientNil)
	assert.Contains(t, ErrPositionKeepingClientNil.Error(), "position keeping client")
}

func TestServiceError_HealthCheckerRepositoryNil(t *testing.T) {
	assert.NotNil(t, ErrHealthCheckerRepositoryNil)
	assert.Contains(t, ErrHealthCheckerRepositoryNil.Error(), "health checker")
}
