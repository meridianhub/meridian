package saga

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContainsIgnoreCase(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		substr string
		want   bool
	}{
		{"exact match", "hello world", "hello", true},
		{"case insensitive match", "Hello World", "hello", true},
		{"case insensitive substr", "hello world", "WORLD", true},
		{"no match", "hello world", "xyz", false},
		{"empty substr matches", "hello", "", true},
		{"empty string no match", "", "hello", false},
		{"both empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsIgnoreCase(tt.s, tt.substr)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestIsDuplicateKeyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"duplicate key", errors.New("duplicate key value violates unique constraint"), true},
		{"unique constraint", errors.New("unique constraint violation on column id"), true},
		{"postgres error code", errors.New("ERROR: 23505 duplicate key"), true},
		{"unrelated error", errors.New("connection timeout"), false},
		{"case insensitive duplicate key", errors.New("DUPLICATE KEY detected"), true},
		{"case insensitive unique constraint", errors.New("UNIQUE CONSTRAINT violated"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isDuplicateKeyError(tt.err)
			assert.Equal(t, tt.want, result)
		})
	}
}
