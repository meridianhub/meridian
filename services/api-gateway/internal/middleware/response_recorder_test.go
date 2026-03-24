package middleware

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResponseRecorder_Defaults(t *testing.T) {
	rec := newResponseRecorder()
	defer rec.release()

	assert.Equal(t, http.StatusOK, rec.code)
	assert.NotNil(t, rec.headers)
	assert.NotNil(t, rec.buf)
	assert.False(t, rec.wroteHeader)
}

func TestResponseRecorder_WriteHeader_Idempotent(t *testing.T) {
	rec := newResponseRecorder()
	defer rec.release()

	rec.WriteHeader(http.StatusCreated)
	rec.WriteHeader(http.StatusBadRequest) // second call is a no-op

	assert.Equal(t, http.StatusCreated, rec.code)
	assert.True(t, rec.wroteHeader)
}

func TestResponseRecorder_Write_SetsWroteHeader(t *testing.T) {
	rec := newResponseRecorder()
	defer rec.release()

	assert.False(t, rec.wroteHeader)

	_, err := rec.Write([]byte("hello"))
	require.NoError(t, err)

	assert.True(t, rec.wroteHeader)
	assert.Equal(t, http.StatusOK, rec.code)
}

func TestResponseRecorder_Write_AccumulatesBody(t *testing.T) {
	rec := newResponseRecorder()
	defer rec.release()

	_, err := rec.Write([]byte("foo"))
	require.NoError(t, err)
	_, err = rec.Write([]byte("bar"))
	require.NoError(t, err)

	assert.Equal(t, "foobar", rec.buf.String())
}

func TestResponseRecorder_Header_ReturnsHeaders(t *testing.T) {
	rec := newResponseRecorder()
	defer rec.release()

	rec.Header().Set("X-Custom", "value")

	assert.Equal(t, "value", rec.headers.Get("X-Custom"))
	assert.Equal(t, rec.headers, rec.Header())
}

func TestAcquireBuffer_ReturnsEmptyBuffer(t *testing.T) {
	b := acquireBuffer()
	defer releaseBuffer(b)

	assert.Equal(t, 0, b.Len())
}

func TestReleaseBuffer_ReturnedBufferIsReset(t *testing.T) {
	b := acquireBuffer()
	_, err := b.WriteString("some data")
	require.NoError(t, err)

	releaseBuffer(b)

	// Acquire again - should get a reset buffer (may or may not be the same object).
	b2 := acquireBuffer()
	defer releaseBuffer(b2)
	assert.Equal(t, 0, b2.Len())
}

func TestReleaseBuffer_LargeBuffer_DoesNotPanic(t *testing.T) {
	// Create a buffer larger than 64KB threshold.
	// releaseBuffer drops oversized buffers instead of returning them to the pool.
	large := make([]byte, 65*1024)
	b := &bytes.Buffer{}
	_, err := b.Write(large)
	require.NoError(t, err)
	assert.Greater(t, b.Cap(), 64*1024)

	assert.NotPanics(t, func() {
		releaseBuffer(b)
	})
}
