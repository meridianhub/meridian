package resources_test

import (
	"context"
	"strings"
	"testing"

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

func TestResourceProvider_List_ReturnsAllResources(t *testing.T) {
	provider := resources.New(nil)

	list := provider.List()

	if len(list) == 0 {
		t.Fatal("expected at least one resource, got none")
	}

	// Verify each resource has a non-empty URI and name
	for _, r := range list {
		if r.URI == "" {
			t.Errorf("resource missing URI: %+v", r)
		}
		if r.Name == "" {
			t.Errorf("resource missing Name: %+v", r)
		}
	}
}

func TestResourceProvider_List_IncludesDocResources(t *testing.T) {
	provider := resources.New(nil)
	list := provider.List()

	uris := make(map[string]bool)
	for _, r := range list {
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
	provider := resources.New(nil)
	list := provider.List()

	uris := make(map[string]bool)
	for _, r := range list {
		uris[r.URI] = true
	}

	if !uris["meridian://manifest/current"] {
		t.Error("expected meridian://manifest/current in resource list")
	}
}

func TestResourceProvider_Read_StarlarkGuide(t *testing.T) {
	provider := resources.New(nil)

	result, err := provider.Read(context.Background(), "meridian://docs/starlark-guide")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
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
	provider := resources.New(nil)

	result, err := provider.Read(context.Background(), "meridian://docs/cel-reference")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
	provider := resources.New(client)

	result, err := provider.Read(context.Background(), "meridian://manifest/current")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
	provider := resources.New(nil)

	result, err := provider.Read(context.Background(), "meridian://manifest/current")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected at least one content block")
	}
	// Should return a message indicating no client configured
	if result.Contents[0].Text == "" {
		t.Error("expected non-empty content for manifest with no client")
	}
}

func TestResourceProvider_Read_UnknownURI_ReturnsError(t *testing.T) {
	provider := resources.New(nil)

	_, err := provider.Read(context.Background(), "meridian://unknown/resource")
	if err == nil {
		t.Fatal("expected error for unknown URI")
	}
}

func TestResourceProvider_EmbeddedDocs_NotEmpty(t *testing.T) {
	provider := resources.New(nil)

	for _, uri := range []string{
		"meridian://docs/starlark-guide",
		"meridian://docs/cel-reference",
	} {
		result, err := provider.Read(context.Background(), uri)
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
