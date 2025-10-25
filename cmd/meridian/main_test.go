package main

import "testing"

func TestVersionVariables(t *testing.T) {
	tests := []struct {
		name     string
		variable string
		notEmpty bool
	}{
		{"Version is set", Version, true},
		{"Commit is set", Commit, true},
		{"BuildDate is set", BuildDate, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.notEmpty && tt.variable == "" {
				t.Errorf("expected %s to be set, got empty string", tt.name)
			}
		})
	}
}
