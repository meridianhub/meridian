package architecture_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// errPattern matches exported error variables following ErrEntityAction or ErrEntity convention.
var errPattern = regexp.MustCompile(`^Err[A-Z][a-zA-Z]*$`)

// TestDomainErrorNaming validates that exported error variables in domain/errors.go files
// follow the ErrEntityAction naming pattern.
func TestDomainErrorNaming(t *testing.T) {
	root := findRepoRoot(t)
	fset := token.NewFileSet()

	errFiles := findDomainErrorFiles(t, root)
	if len(errFiles) == 0 {
		t.Fatal("found no domain/errors.go files — something is wrong with the search")
	}

	for _, path := range errFiles {
		relPath, _ := filepath.Rel(root, path)
		relPath = filepath.ToSlash(relPath)

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Errorf("could not parse %s: %v", relPath, err)
			continue
		}

		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.VAR {
				continue
			}
			for _, spec := range genDecl.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					if !name.IsExported() {
						continue
					}
					if !strings.HasPrefix(name.Name, "Err") {
						continue
					}
					if !errPattern.MatchString(name.Name) {
						pos := fset.Position(name.Pos())
						t.Errorf("%s:%d: error variable %q does not follow ErrEntityAction pattern. See docs/guides/service-file-conventions.md",
							relPath, pos.Line, name.Name)
					}
				}
			}
		}
	}
}

// repositoryMethodVerbs are the standard verb prefixes for repository methods.
var repositoryMethodVerbs = []string{
	"Create", "Find", "Update", "List", "Delete",
	"Save", "Count", "Exists", "Get", "Remove", "Upsert",
	// Domain-specific verbs common in this codebase.
	"Insert", "Record", "Query", "Retrieve", "Load",
	"Mark", "Sum", "Is", "Soft", "Deprecate",
}

// TestRepositoryMethodVerbs validates that methods on types ending in "Repository"
// use standard verb prefixes.
func TestRepositoryMethodVerbs(t *testing.T) {
	root := findRepoRoot(t)
	fset := token.NewFileSet()
	var violations []string

	servicesDir := filepath.Join(root, "services")
	err := filepath.Walk(servicesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if shouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") || isTestFile(path) || isGeneratedFile(path) {
			return nil
		}
		if !strings.Contains(path, "/domain/") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil //nolint:nilerr // intentionally skip unparseable files
		}

		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !strings.HasSuffix(ts.Name.Name, "Repository") {
					continue
				}
				iface, ok := ts.Type.(*ast.InterfaceType)
				if !ok || iface.Methods == nil {
					continue
				}
				for _, method := range iface.Methods.List {
					if len(method.Names) == 0 {
						continue // embedded interface
					}
					methodName := method.Names[0].Name
					if !method.Names[0].IsExported() {
						continue
					}
					if !hasStandardVerb(methodName) {
						pos := fset.Position(method.Pos())
						relPath, _ := filepath.Rel(root, path)
						relPath = filepath.ToSlash(relPath)
						violations = append(violations, fmt.Sprintf(
							"%s:%d: repository method %s.%s does not start with a standard verb (%s). See docs/guides/service-file-conventions.md",
							relPath, pos.Line, ts.Name.Name, methodName, strings.Join(repositoryMethodVerbs, "/"),
						))
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk services: %v", err)
	}

	for _, v := range violations {
		t.Error(v)
	}
}

// knownMissingComplianceDeclarations tracks repository structs that are currently
// missing interface compliance declarations. Do NOT add new entries.
var knownMissingComplianceDeclarations = map[string]bool{
	"services/current-account/adapters/persistence/lien_repository.go::LienRepository":                       true,
	"services/current-account/adapters/persistence/repository.go::Repository":                                true,
	"services/current-account/adapters/persistence/withdrawal_repository.go::WithdrawalRepository":           true,
	"services/financial-accounting/adapters/persistence/repository.go::LedgerRepository":                     true,
	"services/identity/adapters/persistence/repository.go::Repository":                                       true,
	"services/internal-account/adapters/persistence/lien_repository.go::LienRepository":                      true,
	"services/market-information/adapters/persistence/base_repository.go::baseRepository":                    true,
	"services/operational-gateway/adapters/persistence/connection_repository.go::ConnectionRepository":       true,
	"services/operational-gateway/adapters/persistence/instruction_repository.go::InstructionRepository":     true,
	"services/operational-gateway/adapters/persistence/route_repository.go::RouteRepository":                 true,
	"services/party/adapters/persistence/party_type_definition_repository.go::PartyTypeDefinitionRepository": true,
	"services/party/adapters/persistence/payment_method_repository.go::PaymentMethodRepository":              true,
	"services/party/adapters/persistence/repository.go::Repository":                                          true,
	"services/party/adapters/persistence/verification_repository.go::VerificationRepository":                 true,
	"services/payment-order/adapters/persistence/saga_execution_repository.go::SagaExecutionRepository":      true,
	"services/position-keeping/adapters/persistence/postgres_repository.go::PostgresRepository":              true,
}

// TestInterfaceComplianceDeclarations validates that persistence repository implementations
// include a compile-time interface compliance check: var _ domain.XxxRepository = (*XxxRepository)(nil)
func TestInterfaceComplianceDeclarations(t *testing.T) {
	root := findRepoRoot(t)
	fset := token.NewFileSet()

	servicesDir := filepath.Join(root, "services")
	err := filepath.Walk(servicesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if shouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") || isTestFile(path) || isGeneratedFile(path) {
			return nil
		}
		if !strings.Contains(path, "/adapters/persistence/") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil //nolint:nilerr // intentionally skip unparseable files
		}

		// Find struct types that look like repository implementations.
		structNames := map[string]bool{}
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, ok := ts.Type.(*ast.StructType); ok && strings.HasSuffix(ts.Name.Name, "Repository") {
					structNames[ts.Name.Name] = true
				}
			}
		}

		if len(structNames) == 0 {
			return nil
		}

		// Check for var _ SomeType = (*StructName)(nil) declarations.
		hasComplianceCheck := map[string]bool{}
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.VAR {
				continue
			}
			for _, spec := range genDecl.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "_" {
					continue
				}
				for _, val := range vs.Values {
					call, ok := val.(*ast.CallExpr)
					if !ok || len(call.Args) != 1 {
						continue
					}
					ident, ok := call.Args[0].(*ast.Ident)
					if !ok || ident.Name != "nil" {
						continue
					}
					paren, ok := call.Fun.(*ast.ParenExpr)
					if !ok {
						continue
					}
					star, ok := paren.X.(*ast.StarExpr)
					if !ok {
						continue
					}
					if id, ok := star.X.(*ast.Ident); ok {
						hasComplianceCheck[id.Name] = true
					}
				}
			}
		}

		relPath, _ := filepath.Rel(root, path)
		relPath = filepath.ToSlash(relPath)
		for name := range structNames {
			if hasComplianceCheck[name] {
				continue
			}
			key := relPath + "::" + name
			if knownMissingComplianceDeclarations[key] {
				t.Logf("KNOWN: %s: repository struct %s missing interface compliance declaration", relPath, name)
				continue
			}
			t.Errorf("%s: repository struct %s is missing interface compliance declaration (var _ SomeInterface = (*%s)(nil)). See docs/guides/service-file-conventions.md",
				relPath, name, name)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk services: %v", err)
	}
}

func hasStandardVerb(methodName string) bool {
	for _, verb := range repositoryMethodVerbs {
		if methodName == verb {
			return true
		}
		if strings.HasPrefix(methodName, verb) {
			// Verify the character after the verb is uppercase (word boundary).
			// This prevents "Is" from matching "Issue" or "Soft" from matching "Software".
			rest := methodName[len(verb):]
			if len(rest) > 0 && rest[0] >= 'A' && rest[0] <= 'Z' {
				return true
			}
		}
	}
	return false
}

func findDomainErrorFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	servicesDir := filepath.Join(root, "services")
	err := filepath.Walk(servicesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && shouldSkipDir(info.Name()) {
			return filepath.SkipDir
		}
		if info.Name() == "errors.go" && strings.Contains(filepath.Dir(path), "/domain") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk services for error files: %v", err)
	}

	// Also check shared packages.
	sharedDir := filepath.Join(root, "shared")
	_ = filepath.Walk(sharedDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && shouldSkipDir(info.Name()) {
			return filepath.SkipDir
		}
		if info.Name() == "errors.go" {
			files = append(files, path)
		}
		return nil
	})

	return files
}
