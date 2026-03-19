package architecture_test

import (
	"os"
	"path/filepath"
	"testing"
)

// nonStandardServices are excluded from standard service layout checks
// because they follow different architectural patterns.
var nonStandardServices = map[string]bool{
	"api-gateway":   true,
	"event-router":  true,
	"control-plane": true,
	"mcp-server":    true,
}

// servicesWithoutServerGo tracks services that lack service/server.go.
// Do NOT add new entries — all gRPC services should have server.go.
var servicesWithoutServerGo = map[string]bool{
	"forecasting":    true,
	"reference-data": true,
}

// servicesWithNonStandardLayout tracks services that don't follow standard layout.
// Do NOT add new entries.
var servicesWithNonStandardLayout = map[string]bool{
	"financial-gateway": true,
	"forecasting":       true,
	"reference-data":    true,
}

// TestServiceServerGoExists validates that all gRPC services have a service/server.go file.
func TestServiceServerGoExists(t *testing.T) {
	root := findRepoRoot(t)
	servicesDir := filepath.Join(root, "services")
	services := listServiceDirs(t, servicesDir)

	for _, svc := range services {
		if nonStandardServices[svc] {
			continue
		}
		serverPath := filepath.Join(servicesDir, svc, "service", "server.go")
		if _, err := os.Stat(serverPath); os.IsNotExist(err) {
			if servicesWithoutServerGo[svc] {
				t.Logf("KNOWN: services/%s: missing service/server.go", svc)
				continue
			}
			t.Errorf("services/%s: missing service/server.go", svc)
		}
	}
}

// knownMissingDocGo tracks shared packages that currently lack doc.go.
// Do NOT add new entries — create doc.go for new packages.
var knownMissingDocGo = map[string]bool{
	"shared/pkg/saga/schema":                 true,
	"shared/pkg/saga/validation":             true,
	"shared/pkg/valuation/internal/builtins": true,
	"shared/platform/events/topics":          true,
	"shared/platform/quantity/currency":      true,
}

// TestSharedPackagesHaveDocGo validates that all shared/pkg/ and shared/platform/
// packages have a doc.go file for package documentation.
func TestSharedPackagesHaveDocGo(t *testing.T) {
	root := findRepoRoot(t)

	dirs := []string{
		filepath.Join(root, "shared", "pkg"),
		filepath.Join(root, "shared", "platform"),
	}

	for _, dir := range dirs {
		checkDocGo(t, root, dir)
	}
}

// TestServiceDirectoryLayout validates that standard services have the expected
// directory structure: domain/, adapters/persistence/, service/.
func TestServiceDirectoryLayout(t *testing.T) {
	root := findRepoRoot(t)
	servicesDir := filepath.Join(root, "services")
	services := listServiceDirs(t, servicesDir)

	requiredDirs := []string{"domain", "adapters/persistence", "service"}

	for _, svc := range services {
		if nonStandardServices[svc] || servicesWithNonStandardLayout[svc] {
			continue
		}
		for _, dir := range requiredDirs {
			dirPath := filepath.Join(servicesDir, svc, dir)
			if _, err := os.Stat(dirPath); os.IsNotExist(err) {
				t.Errorf("services/%s: missing required directory %s/", svc, dir)
			}
		}
	}
}

func checkDocGo(t *testing.T, root string, dir string) {
	t.Helper()

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if shouldSkipDir(info.Name()) {
			return filepath.SkipDir
		}
		// Skip the root directory itself.
		if path == dir {
			return nil
		}

		// Only check directories that contain .go files (are Go packages).
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			return readErr
		}
		hasGoFiles := false
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".go" {
				hasGoFiles = true
				break
			}
		}
		if !hasGoFiles {
			return nil
		}

		docPath := filepath.Join(path, "doc.go")
		if _, statErr := os.Stat(docPath); os.IsNotExist(statErr) {
			relPath, _ := filepath.Rel(root, path)
			relPath = filepath.ToSlash(relPath)
			if knownMissingDocGo[relPath] {
				t.Logf("KNOWN: %s: missing doc.go", relPath)
				return nil
			}
			t.Errorf("%s: missing doc.go", relPath)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk %s: %v", dir, err)
	}
}
