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

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("failed to read directory %s: %v", dir, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if shouldSkipDir(e.Name()) {
			continue
		}

		pkgDir := filepath.Join(dir, e.Name())

		// Only check directories that contain .go files (are Go packages).
		hasGoFiles := false
		subEntries, err := os.ReadDir(pkgDir)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() && filepath.Ext(sub.Name()) == ".go" {
				hasGoFiles = true
				break
			}
		}
		if !hasGoFiles {
			continue
		}

		docPath := filepath.Join(pkgDir, "doc.go")
		if _, err := os.Stat(docPath); os.IsNotExist(err) {
			relPath, _ := filepath.Rel(root, pkgDir)
			t.Errorf("%s: missing doc.go", relPath)
		}
	}
}
