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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/meridianhub/meridian/services/mcp-server/internal/mcputil"
)

var errManifestRetrieval = errors.New("retrieve current manifest")

//go:embed docs/starlark-guide.md docs/cel-reference.md
var embeddedDocs embed.FS

// ManifestClient retrieves the current tenant manifest as YAML.
// Defined as an interface so tests can inject fakes.
type ManifestClient interface {
	GetCurrentManifestYAML(ctx context.Context) (string, error)
}

// RegisterEmbeddedDocs registers static embedded documentation resources.
// These are always available regardless of backend connectivity.
func RegisterEmbeddedDocs(srv *mcp.Server) {
	// Starlark saga development guide (embedded).
	srv.AddResource(&mcp.Resource{
		URI:         "meridian://docs/starlark-guide",
		Name:        "Starlark Saga Development Guide",
		Description: "Reference guide for writing Starlark saga scripts in Meridian, including constraints, service modules, CEL expressions, and compensation patterns.",
		MIMEType:    "text/markdown",
	}, embeddedResourceHandler("meridian://docs/starlark-guide", "docs/starlark-guide.md"))

	// CEL expression reference (embedded).
	srv.AddResource(&mcp.Resource{
		URI:         "meridian://docs/cel-reference",
		Name:        "CEL Expression Reference",
		Description: "Reference guide for Common Expression Language (CEL) used in Meridian validation rules, bucketing expressions, and precondition checks.",
		MIMEType:    "text/markdown",
	}, embeddedResourceHandler("meridian://docs/cel-reference", "docs/cel-reference.md"))
}

// RegisterManifestResource registers the current economy manifest resource.
// manifestClient may be nil; in that case a placeholder message is returned.
func RegisterManifestResource(srv *mcp.Server, manifestClient ManifestClient) {
	srv.AddResource(&mcp.Resource{
		URI:         "meridian://manifest/current",
		Name:        "Current Economy Manifest",
		Description: "The active economy manifest for the current tenant, describing instruments, account types, sagas, valuation rules, and payment rails.",
		MIMEType:    "text/yaml",
	}, manifestResourceHandler(manifestClient))
}

// manifestResourceHandler returns a ResourceHandler that fetches the current manifest.
// If manifestClient is nil, it returns a placeholder message.
func manifestResourceHandler(manifestClient ManifestClient) mcp.ResourceHandler {
	return func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		if manifestClient == nil {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      "meridian://manifest/current",
					MIMEType: "text/plain",
					Text:     "Manifest client not configured. Set MERIDIAN_API_URL and MERIDIAN_API_KEY to enable live manifest retrieval.",
				}},
			}, nil
		}

		yaml, err := manifestClient.GetCurrentManifestYAML(ctx)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", errManifestRetrieval, mcputil.SanitizeError(err))
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      "meridian://manifest/current",
				MIMEType: "text/yaml",
				Text:     yaml,
			}},
		}, nil
	}
}

// embeddedResourceHandler returns a ResourceHandler that reads an embedded file.
func embeddedResourceHandler(uri, path string) mcp.ResourceHandler {
	return func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		data, err := embeddedDocs.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read embedded doc %q: %w", path, err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      uri,
				MIMEType: "text/markdown",
				Text:     string(data),
			}},
		}, nil
	}
}
