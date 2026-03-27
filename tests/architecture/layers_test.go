package architecture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modulePath = "github.com/meridianhub/meridian"

// compositionRootAllowlist contains files that are allowed to import across services
// because they wire the application together.
var compositionRootAllowlist = map[string]bool{
	"cmd/meridian/wire_services.go": true,
	"cmd/meridian/main.go":          true,
}

// knownCrossServiceInternalImports tracks existing cross-service internal/ imports.
// Do NOT add new entries — use gRPC clients instead of direct imports.
var knownCrossServiceInternalImports = map[string]bool{
	"cmd/ibactl/cmd/provision_defaults.go":        true,
	"cmd/ibactl/cmd/root.go":                      true,
	"services/control-plane/cmd/validate/main.go": true,
	"services/current-account/cmd/main.go":        true,
	"services/financial-accounting/cmd/main.go":   true,
	"services/payment-order/cmd/clients.go":       true,
}

// knownCrossServiceDomainImports tracks existing cross-service domain imports.
// Do NOT add new entries — use gRPC clients instead of direct imports.
var knownCrossServiceDomainImports = map[string]bool{
	"services/api-gateway/admin_handler.go":                                 true,
	"services/api-gateway/auth_handler.go":                                  true,
	"services/api-gateway/auth_sso_handler.go":                              true,
	"services/api-gateway/cmd/main.go":                                      true,
	"services/api-gateway/password_reset_handler.go":                        true,
	"services/api-gateway/registration_handler.go":                          true,
	"services/api-gateway/verification_handler.go":                          true,
	"services/current-account/service/deposit_orchestrator.go":              true,
	"services/current-account/service/fungibility_validator.go":             true,
	"services/current-account/service/grpc_account_endpoints.go":            true,
	"services/current-account/service/validators.go":                        true,
	"services/current-account/service/server.go":                            true,
	"services/current-account/service/withdrawal_orchestrator.go":           true,
	"services/event-router/adapters/grpc/position_keeping_client.go":        true,
	"services/event-router/adapters/messaging/audit_consumer.go":            true,
	"services/event-router/adapters/messaging/platform_metering_handler.go": true,
	"services/event-router/cmd/main.go":                                     true,
	"services/event-router/domain/measurement.go":                           true,
	"services/event-router/domain/position_keeping_client.go":               true,
	"services/event-router/internal/correlation/extractor.go":               true,
	"services/financial-accounting/cmd/main.go":                             true,
	"services/internal-account/service/grpc_account_endpoints.go":           true,
	"services/internal-account/service/server.go":                           true,
}

// knownSharedImportsServices tracks shared/ files that currently import services/.
// Do NOT add new entries — shared packages must never depend on services.
var knownSharedImportsServices = map[string]bool{
	"shared/pkg/valuationfeature/resolution.go":  true,
	"shared/pkg/valuationfeature/seeder.go":      true,
	"shared/platform/gateway/tenant_resolver.go": true,
}

// knownAdapterImportsService tracks adapter files that import their service layer.
// Do NOT add new entries — adapters should depend on domain, not service.
var knownAdapterImportsService = map[string]bool{
	"services/financial-accounting/adapters/messaging/deposit_consumer.go": true,
	"services/internal-account/adapters/grpc/position_keeping_client.go":   true,
	"services/party/adapters/http/verification_webhook.go":                 true,
	"services/payment-order/adapters/clients/reference_data_client.go":     true,
	"services/payment-order/adapters/lock/redis_lock_client.go":            true,
}

// TestNoInternalCrossServiceImports validates that no service imports another service's
// internal/ package. The composition root (cmd/meridian/) is allowlisted.
func TestNoInternalCrossServiceImports(t *testing.T) {
	root := findRepoRoot(t)
	fset := token.NewFileSet()

	servicesDir := filepath.Join(root, "services")
	services := listServiceDirs(t, servicesDir)

	walkGoFiles(t, root, func(path string, relPath string) {
		if compositionRootAllowlist[relPath] {
			return
		}

		ownerService := ""
		for _, svc := range services {
			prefix := filepath.Join("services", svc) + "/"
			if strings.HasPrefix(relPath, prefix) {
				ownerService = svc
				break
			}
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return
		}

		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(importPath, modulePath+"/services/") {
				continue
			}

			// Check if import path contains /internal/ as a full path segment
			// (not just a prefix like "internal-account").
			remainder := strings.TrimPrefix(importPath, modulePath+"/services/")
			parts := strings.SplitN(remainder, "/", 2)
			importedService := parts[0]
			if len(parts) < 2 || !hasInternalSegment(parts[1]) {
				continue
			}

			if importedService != ownerService {
				if knownCrossServiceInternalImports[relPath] {
					t.Logf("KNOWN: %s: imports internal package of service %q", relPath, importedService)
					continue
				}
				pos := fset.Position(imp.Pos())
				t.Errorf("%s:%d: imports internal package of service %q: %s",
					relPath, pos.Line, importedService, importPath)
			}
		}
	})
}

// TestNoCrossServiceDomainImports validates that services communicate via gRPC, not by
// importing each other's domain packages directly. Test files and composition root are excluded.
func TestNoCrossServiceDomainImports(t *testing.T) {
	root := findRepoRoot(t)
	fset := token.NewFileSet()

	servicesDir := filepath.Join(root, "services")
	services := listServiceDirs(t, servicesDir)

	walkGoFiles(t, root, func(path string, relPath string) {
		if compositionRootAllowlist[relPath] {
			return
		}

		ownerService := ""
		for _, svc := range services {
			prefix := filepath.Join("services", svc) + "/"
			if strings.HasPrefix(relPath, prefix) {
				ownerService = svc
				break
			}
		}
		if ownerService == "" {
			return
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return
		}

		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(importPath, modulePath+"/services/") {
				continue
			}

			remainder := strings.TrimPrefix(importPath, modulePath+"/services/")
			importedService := strings.SplitN(remainder, "/", 2)[0]

			if importedService == ownerService {
				continue
			}

			// Allow importing another service's client/ package (generated gRPC stubs).
			parts := strings.SplitN(remainder, "/", 2)
			if len(parts) > 1 && strings.HasPrefix(parts[1], "client") {
				continue
			}

			if knownCrossServiceDomainImports[relPath] {
				t.Logf("KNOWN: %s: service %q imports service %q", relPath, ownerService, importedService)
				continue
			}
			pos := fset.Position(imp.Pos())
			t.Errorf("%s:%d: service %q imports service %q directly (%s). Use gRPC client instead.",
				relPath, pos.Line, ownerService, importedService, importPath)
		}
	})
}

// TestSharedNeverImportsServices validates that shared/ packages never import services/.
func TestSharedNeverImportsServices(t *testing.T) {
	root := findRepoRoot(t)
	fset := token.NewFileSet()

	sharedDir := filepath.Join(root, "shared")
	err := filepath.Walk(sharedDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && shouldSkipDir(info.Name()) {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") || isTestFile(path) || isGeneratedFile(path) {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return nil //nolint:nilerr // intentionally skip unparseable files
		}

		relPath, _ := filepath.Rel(root, path)
		relPath = filepath.ToSlash(relPath)
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(importPath, modulePath+"/services/") {
				if knownSharedImportsServices[relPath] {
					t.Logf("KNOWN: %s: shared package imports service: %s", relPath, importPath)
					continue
				}
				pos := fset.Position(imp.Pos())
				t.Errorf("%s:%d: shared package imports service: %s",
					relPath, pos.Line, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk shared/: %v", err)
	}
}

// TestAdaptersNeverImportService validates that within each service, the adapters/ layer
// does not import the service/ layer.
func TestAdaptersNeverImportService(t *testing.T) {
	root := findRepoRoot(t)
	fset := token.NewFileSet()

	servicesDir := filepath.Join(root, "services")
	services := listServiceDirs(t, servicesDir)

	for _, svc := range services {
		adaptersDir := filepath.Join(servicesDir, svc, "adapters")
		if _, err := os.Stat(adaptersDir); os.IsNotExist(err) {
			continue
		}

		serviceImportPrefix := modulePath + "/services/" + svc + "/service"

		err := filepath.Walk(adaptersDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() && shouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") || isTestFile(path) || isGeneratedFile(path) {
				return nil
			}

			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return nil //nolint:nilerr // intentionally skip unparseable files
			}

			relPath, _ := filepath.Rel(root, path)
			relPath = filepath.ToSlash(relPath)
			for _, imp := range file.Imports {
				importPath := strings.Trim(imp.Path.Value, `"`)
				if strings.HasPrefix(importPath, serviceImportPrefix) {
					if knownAdapterImportsService[relPath] {
						t.Logf("KNOWN: %s: adapter in %q imports service layer", relPath, svc)
						continue
					}
					pos := fset.Position(imp.Pos())
					t.Errorf("%s:%d: adapter in %q imports service layer: %s",
						relPath, pos.Line, svc, importPath)
				}
			}
			return nil
		})
		if err != nil {
			t.Errorf("failed to walk adapters for %s: %v", svc, err)
		}
	}
}

func listServiceDirs(t *testing.T, servicesDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(servicesDir)
	if err != nil {
		t.Fatalf("failed to read services directory: %v", err)
	}
	var services []string
	for _, e := range entries {
		if e.IsDir() {
			services = append(services, e.Name())
		}
	}
	return services
}

// hasInternalSegment returns true if the path contains "internal" as a full path segment.
func hasInternalSegment(subPath string) bool {
	for _, seg := range strings.Split(subPath, "/") {
		if seg == "internal" {
			return true
		}
	}
	return false
}
