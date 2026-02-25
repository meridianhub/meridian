// Package resources implements MCP resource handling for the Meridian MCP server.
// Resources provide context loaded into the LLM: documentation, schemas, and live data.
//
// Supported URIs:
//   - meridian://manifest/current   — current economy manifest YAML
//   - meridian://docs/starlark-guide — Starlark language reference (embedded)
//   - meridian://docs/cel-reference  — CEL expression reference (embedded)
package resources

import (
	"context"
	"embed"
	"errors"
	"fmt"
)

//go:embed docs/starlark-guide.md docs/cel-reference.md
var embeddedDocs embed.FS

// ErrResourceNotFound is returned when a URI does not match any known resource.
var ErrResourceNotFound = errors.New("resource not found")

// Resource describes a single MCP resource entry returned by resources/list.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ResourceContent is a single content block within a resource read result.
type ResourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// ReadResult is the payload returned by resources/read.
type ReadResult struct {
	Contents []ResourceContent `json:"contents"`
}

// ManifestClient retrieves the current tenant manifest as YAML.
// Defined as an interface so tests can inject fakes.
type ManifestClient interface {
	GetCurrentManifestYAML(ctx context.Context) (string, error)
}

// Provider handles MCP resource list and read operations.
// It is safe to call List and Read concurrently; the embedded docs are
// read-only after construction.
type Provider struct {
	manifestClient ManifestClient

	// registry maps URI to resource metadata + reader function.
	registry []registeredResource
}

type registeredResource struct {
	meta   Resource
	reader func(ctx context.Context) (*ReadResult, error)
}

// New creates a Provider. manifestClient may be nil; in that case the
// meridian://manifest/current resource returns a placeholder message.
func New(manifestClient ManifestClient) *Provider {
	p := &Provider{
		manifestClient: manifestClient,
	}
	p.register()
	return p
}

func (p *Provider) register() {
	p.registry = []registeredResource{
		{
			meta: Resource{
				URI:         "meridian://manifest/current",
				Name:        "Current Economy Manifest",
				Description: "The active economy manifest for the current tenant, describing instruments, account types, sagas, valuation rules, and payment rails.",
				MIMEType:    "text/yaml",
			},
			reader: p.readManifest,
		},
		{
			meta: Resource{
				URI:         "meridian://docs/starlark-guide",
				Name:        "Starlark Saga Development Guide",
				Description: "Reference guide for writing Starlark saga scripts in Meridian, including constraints, service modules, CEL expressions, and compensation patterns.",
				MIMEType:    "text/markdown",
			},
			reader: makeEmbeddedReader("meridian://docs/starlark-guide", "docs/starlark-guide.md"),
		},
		{
			meta: Resource{
				URI:         "meridian://docs/cel-reference",
				Name:        "CEL Expression Reference",
				Description: "Reference guide for Common Expression Language (CEL) used in Meridian validation rules, bucketing expressions, and precondition checks.",
				MIMEType:    "text/markdown",
			},
			reader: makeEmbeddedReader("meridian://docs/cel-reference", "docs/cel-reference.md"),
		},
	}
}

// List returns all registered resource descriptors.
func (p *Provider) List() []Resource {
	result := make([]Resource, len(p.registry))
	for i, r := range p.registry {
		result[i] = r.meta
	}
	return result
}

// Read returns the content for the given URI.
// Returns ErrResourceNotFound if the URI is not registered.
func (p *Provider) Read(ctx context.Context, uri string) (*ReadResult, error) {
	for _, r := range p.registry {
		if r.meta.URI == uri {
			return r.reader(ctx)
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrResourceNotFound, uri)
}

// readManifest fetches the current manifest from the gRPC client.
// If no client is configured it returns an informational message.
func (p *Provider) readManifest(ctx context.Context) (*ReadResult, error) {
	if p.manifestClient == nil {
		return &ReadResult{
			Contents: []ResourceContent{{
				URI:      "meridian://manifest/current",
				MIMEType: "text/plain",
				Text:     "Manifest client not configured. Set MERIDIAN_API_URL and MERIDIAN_API_KEY to enable live manifest retrieval.",
			}},
		}, nil
	}

	yaml, err := p.manifestClient.GetCurrentManifestYAML(ctx)
	if err != nil {
		return &ReadResult{
			Contents: []ResourceContent{{
				URI:      "meridian://manifest/current",
				MIMEType: "text/plain",
				Text:     fmt.Sprintf("Failed to retrieve manifest: %v", err),
			}},
		}, nil
	}

	return &ReadResult{
		Contents: []ResourceContent{{
			URI:      "meridian://manifest/current",
			MIMEType: "text/yaml",
			Text:     yaml,
		}},
	}, nil
}

// makeEmbeddedReader returns a reader function that reads an embedded file.
func makeEmbeddedReader(uri, path string) func(ctx context.Context) (*ReadResult, error) {
	return func(_ context.Context) (*ReadResult, error) {
		data, err := embeddedDocs.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read embedded doc %q: %w", path, err)
		}
		return &ReadResult{
			Contents: []ResourceContent{{
				URI:      uri,
				MIMEType: "text/markdown",
				Text:     string(data),
			}},
		}, nil
	}
}
