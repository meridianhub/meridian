package schema

import (
	_ "embed"
	"fmt"
)

//go:embed handlers.yaml
var handlersYAMLBytes []byte

// HandlersYAML returns the embedded handlers.yaml content.
// Useful for callers that need to load the canonical handler definitions
// without filesystem access (e.g., embedded binaries).
func HandlersYAML() []byte {
	return append([]byte(nil), handlersYAMLBytes...)
}

// NewRegistryWithHandlers creates a Registry pre-loaded with the canonical
// handler definitions from the embedded handlers.yaml.
func NewRegistryWithHandlers() (*Registry, error) {
	r := NewRegistry()
	if err := r.LoadFromYAML(handlersYAMLBytes); err != nil {
		return nil, fmt.Errorf("load embedded handlers.yaml: %w", err)
	}
	return r, nil
}
