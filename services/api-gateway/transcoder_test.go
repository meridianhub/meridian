package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	descriptorpb "google.golang.org/protobuf/types/descriptorpb"

	_ "embed"
)

//go:embed testdata/descriptor.binpb
var testDescriptorBytes []byte

// partyBackend provides a single ServiceBackend entry for PartyService, which has
// non-conflicting REST routes and is suitable for use in unit tests.
var partyBackend = []ServiceBackend{
	{ServiceName: "meridian.party.v1.PartyService", BackendAddr: "localhost:50051"},
}

// TestNewTranscoder_ReturnsNonNilHandler verifies that NewTranscoder constructs
// a valid http.Handler from a well-formed FileDescriptorSet and valid backends.
func TestNewTranscoder_ReturnsNonNilHandler(t *testing.T) {
	handler, err := NewTranscoder(testDescriptorBytes, partyBackend)
	require.NoError(t, err)
	assert.NotNil(t, handler)
}

// TestNewTranscoder_InvalidDescriptor verifies that NewTranscoder returns an error
// when given unparseable descriptor bytes.
func TestNewTranscoder_InvalidDescriptor(t *testing.T) {
	_, err := NewTranscoder([]byte("not valid protobuf"), partyBackend)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

// TestNewTranscoder_NoBackends verifies that NewTranscoder returns an error
// when no backends are provided.
func TestNewTranscoder_NoBackends(t *testing.T) {
	_, err := NewTranscoder(testDescriptorBytes, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoBackends)
}

// TestNewTranscoder_UnknownService verifies that NewTranscoder returns an error
// when a requested service name is not present in the descriptor set.
func TestNewTranscoder_UnknownService(t *testing.T) {
	backends := []ServiceBackend{
		{ServiceName: "nonexistent.v1.FakeService", BackendAddr: "localhost:50051"},
	}
	_, err := NewTranscoder(testDescriptorBytes, backends)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in descriptor set")
}

// TestNewTranscoder_EmptyDescriptor verifies that NewTranscoder returns an error
// when given a valid but empty FileDescriptorSet (no files).
func TestNewTranscoder_EmptyDescriptor(t *testing.T) {
	empty := &descriptorpb.FileDescriptorSet{}
	data, err := proto.Marshal(empty)
	require.NoError(t, err)

	_, err = NewTranscoder(data, partyBackend)
	require.Error(t, err)
	// Either "not found" (service lookup fails) or a file registry error.
	assert.NotEmpty(t, err.Error())
}

// TestNewTranscoder_MultipleServices verifies that multiple non-conflicting services
// can be registered in a single transcoder.
func TestNewTranscoder_MultipleServices(t *testing.T) {
	backends := []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService", BackendAddr: "localhost:50051"},
		{ServiceName: "meridian.tenant.v1.TenantService", BackendAddr: "localhost:50052"},
	}
	handler, err := NewTranscoder(testDescriptorBytes, backends)
	require.NoError(t, err)
	assert.NotNil(t, handler)
}

// TestNewTranscoder_ServeHTTP verifies that the transcoder handler responds to
// HTTP requests (even if the backend is unavailable, it should return a valid
// HTTP response rather than panicking).
func TestNewTranscoder_ServeHTTP(t *testing.T) {
	handler, err := NewTranscoder(testDescriptorBytes, partyBackend)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/unknown.path/Method", nil)
	handler.ServeHTTP(rec, req)

	// Vanguard returns 404 for unknown paths.
	assert.Equal(t, http.StatusNotFound, rec.Code, "expected 404 for unknown path, got %d", rec.Code)
}
