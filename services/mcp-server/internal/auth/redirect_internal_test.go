package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsAllowedRedirectURI(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want bool
	}{
		// Allowed
		{"https production", "https://app.example.com/callback", true},
		{"http localhost", "http://localhost:8090/callback", true},
		{"http 127.0.0.1", "http://127.0.0.1:3000/cb", true},
		{"http ipv6 loopback", "http://[::1]:3000/cb", true},

		// Rejected: wrong scheme
		{"javascript scheme", "javascript:alert(1)", false},
		{"data scheme", "data:text/html,<h1>evil</h1>", false},
		{"ftp scheme", "ftp://evil.com/file", false},
		{"no scheme", "/relative/path", false},

		// Rejected: http to non-localhost
		{"http remote host", "http://evil.com/callback", false},

		// Rejected: opaque URIs (bypass via url.Parse quirks)
		{"opaque https", "https:evil.com", false},
		{"opaque http", "http:evil.com", false},

		// Rejected: empty host
		{"empty host https", "https:///callback", false},
		{"empty host http", "http:///callback", false},

		// Rejected: invalid URI
		{"invalid URI", "://broken", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAllowedRedirectURI(tt.uri)
			assert.Equal(t, tt.want, got, "isAllowedRedirectURI(%q)", tt.uri)
		})
	}
}

func TestBuildAuthRedirect(t *testing.T) {
	t.Run("includes code and state", func(t *testing.T) {
		result, err := buildAuthRedirect("https://app.example.com/callback", "abc123", "mystate")
		assert.NoError(t, err)
		assert.Contains(t, result, "code=abc123")
		assert.Contains(t, result, "state=mystate")
	})

	t.Run("omits state when empty", func(t *testing.T) {
		result, err := buildAuthRedirect("https://app.example.com/callback", "abc123", "")
		assert.NoError(t, err)
		assert.Contains(t, result, "code=abc123")
		assert.NotContains(t, result, "state=")
	})

	t.Run("preserves existing query params", func(t *testing.T) {
		result, err := buildAuthRedirect("https://app.example.com/callback?foo=bar", "abc123", "")
		assert.NoError(t, err)
		assert.Contains(t, result, "foo=bar")
		assert.Contains(t, result, "code=abc123")
	})
}
