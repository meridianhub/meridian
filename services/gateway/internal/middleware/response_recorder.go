package middleware

import (
	"bytes"
	"net/http"
	"sync"
)

// responseRecorder captures the HTTP response status code and body written by a
// downstream handler, allowing the middleware to inspect and rewrite the response.
type responseRecorder struct {
	code        int
	headers     http.Header
	buf         *bytes.Buffer
	wroteHeader bool
}

// bufPool is a pool of byte buffers used to capture response bodies without
// allocating a new buffer per request.
var bufPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

func acquireBuffer() *bytes.Buffer {
	b, _ := bufPool.Get().(*bytes.Buffer)
	if b == nil {
		b = new(bytes.Buffer)
	}
	b.Reset()
	return b
}

func releaseBuffer(b *bytes.Buffer) {
	// Avoid holding large buffers in the pool indefinitely.
	if b.Cap() <= 64*1024 {
		bufPool.Put(b)
	}
}

// newResponseRecorder allocates a responseRecorder backed by a pooled buffer.
func newResponseRecorder() *responseRecorder {
	return &responseRecorder{
		code:    http.StatusOK,
		headers: make(http.Header),
		buf:     acquireBuffer(),
	}
}

// Header returns the response headers map for the downstream handler to write into.
func (r *responseRecorder) Header() http.Header {
	return r.headers
}

// Write captures the response body bytes. If WriteHeader has not been called,
// it implicitly sets the status to 200, matching net/http.ResponseWriter semantics.
func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.buf.Write(b)
}

// WriteHeader captures the HTTP status code. Subsequent calls are no-ops,
// matching net/http.ResponseWriter first-call-wins semantics.
func (r *responseRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.code = code
	r.wroteHeader = true
}

// release returns the underlying buffer to the pool.
func (r *responseRecorder) release() {
	releaseBuffer(r.buf)
}
