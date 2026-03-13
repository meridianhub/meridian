package resources_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/meridianhub/meridian/services/mcp-server/internal/resources"
)

// fakeManifestClient is a test double for ManifestClient.
type fakeManifestClient struct {
	yaml string
	err  error
}

func (f *fakeManifestClient) GetCurrentManifestYAML(_ context.Context) (string, error) {
	return f.yaml, f.err
}

// setupResourceServer creates an in-memory MCP server+client pair with
// resources registered, returning the connected client session.
func setupResourceServer(t *testing.T, client resources.ManifestClient) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.0.1"}, nil)
	resources.RegisterResources(srv, client)

	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })

	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })

	return cs
}

func TestResourceProvider_List_ReturnsAllResources(t *testing.T) {
	cs := setupResourceServer(t, nil)
	ctx := context.Background()

	result, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}

	if len(result.Resources) == 0 {
		t.Fatal("expected at least one resource, got none")
	}

	for _, r := range result.Resources {
		if r.URI == "" {
			t.Errorf("resource missing URI: %+v", r)
		}
		if r.Name == "" {
			t.Errorf("resource missing Name: %+v", r)
		}
	}
}

func TestResourceProvider_List_IncludesDocResources(t *testing.T) {
	cs := setupResourceServer(t, nil)
	ctx := context.Background()

	result, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}

	uris := make(map[string]bool)
	for _, r := range result.Resources {
		uris[r.URI] = true
	}

	required := []string{
		"meridian://docs/starlark-guide",
		"meridian://docs/cel-reference",
	}
	for _, uri := range required {
		if !uris[uri] {
			t.Errorf("expected resource %q in list, but not found", uri)
		}
	}
}

func TestResourceProvider_List_IncludesManifestResource(t *testing.T) {
	cs := setupResourceServer(t, nil)
	ctx := context.Background()

	result, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}

	uris := make(map[string]bool)
	for _, r := range result.Resources {
		uris[r.URI] = true
	}

	if !uris["meridian://manifest/current"] {
		t.Error("expected meridian://manifest/current in resource list")
	}
}

func TestResourceProvider_Read_StarlarkGuide(t *testing.T) {
	cs := setupResourceServer(t, nil)
	ctx := context.Background()

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "meridian://docs/starlark-guide",
	})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected at least one content block")
	}

	text := result.Contents[0].Text
	if !strings.Contains(text, "Starlark") {
		t.Errorf("expected Starlark guide content, got: %q", truncate(text, 100))
	}
}

func TestResourceProvider_Read_CELReference(t *testing.T) {
	cs := setupResourceServer(t, nil)
	ctx := context.Background()

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "meridian://docs/cel-reference",
	})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected at least one content block")
	}

	text := result.Contents[0].Text
	if !strings.Contains(text, "CEL") {
		t.Errorf("expected CEL reference content, got: %q", truncate(text, 100))
	}
}

func TestResourceProvider_Read_ManifestCurrent_WithClient(t *testing.T) {
	manifestYAML := "instruments:\n  - code: GBP\n    name: British Pound\n"
	client := &fakeManifestClient{yaml: manifestYAML}
	cs := setupResourceServer(t, client)
	ctx := context.Background()

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "meridian://manifest/current",
	})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected at least one content block")
	}

	text := result.Contents[0].Text
	if !strings.Contains(text, "GBP") {
		t.Errorf("expected manifest content containing 'GBP', got: %q", truncate(text, 200))
	}
}

func TestResourceProvider_Read_ManifestCurrent_NoClient(t *testing.T) {
	cs := setupResourceServer(t, nil)
	ctx := context.Background()

	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "meridian://manifest/current",
	})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected at least one content block")
	}
	if result.Contents[0].Text == "" {
		t.Error("expected non-empty content for manifest with no client")
	}
}

func TestResourceProvider_Read_UnknownURI_ReturnsError(t *testing.T) {
	cs := setupResourceServer(t, nil)
	ctx := context.Background()

	_, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "meridian://unknown/resource",
	})
	if err == nil {
		t.Fatal("expected error for unknown URI")
	}
}

func TestResourceProvider_EmbeddedDocs_NotEmpty(t *testing.T) {
	cs := setupResourceServer(t, nil)
	ctx := context.Background()

	for _, uri := range []string{
		"meridian://docs/starlark-guide",
		"meridian://docs/cel-reference",
	} {
		result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{
			URI: uri,
		})
		if err != nil {
			t.Errorf("URI %q: unexpected error: %v", uri, err)
			continue
		}
		if len(result.Contents) == 0 || result.Contents[0].Text == "" {
			t.Errorf("URI %q: expected non-empty content", uri)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
