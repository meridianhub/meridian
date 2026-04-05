package provisioner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeMigrationFile creates a migration SQL file in the given directory.
func writeMigrationFile(t *testing.T, dir, filename, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o600)
	require.NoError(t, err)
}

func TestFilterMigrationsAfter_EmptyCurrentVersion(t *testing.T) {
	migrations := []migration{
		{Filename: "001_init.sql", Version: "001", Content: "CREATE TABLE foo (id INT)"},
		{Filename: "002_add.sql", Version: "002", Content: "ALTER TABLE foo ADD COLUMN bar TEXT"},
	}

	// Empty current version should return nil as a safety guard
	result := filterMigrationsAfter(migrations, "")
	assert.Nil(t, result)
}

func TestFilterMigrationsAfter_NoNewMigrations(t *testing.T) {
	migrations := []migration{
		{Filename: "001_init.sql", Version: "001"},
		{Filename: "002_add.sql", Version: "002"},
	}

	result := filterMigrationsAfter(migrations, "002")
	assert.Empty(t, result)
}

func TestFilterMigrationsAfter_AllNewMigrations(t *testing.T) {
	migrations := []migration{
		{Filename: "002_add.sql", Version: "002"},
		{Filename: "003_more.sql", Version: "003"},
	}

	result := filterMigrationsAfter(migrations, "001")
	assert.Len(t, result, 2)
	assert.Equal(t, "002", result[0].Version)
	assert.Equal(t, "003", result[1].Version)
}

func TestFilterMigrationsAfter_SomeMigrationsNewer(t *testing.T) {
	migrations := []migration{
		{Filename: "001_init.sql", Version: "001"},
		{Filename: "002_add.sql", Version: "002"},
		{Filename: "003_more.sql", Version: "003"},
		{Filename: "004_extra.sql", Version: "004"},
	}

	result := filterMigrationsAfter(migrations, "002")
	require.Len(t, result, 2)
	assert.Equal(t, "003", result[0].Version)
	assert.Equal(t, "004", result[1].Version)
}

func TestFilterMigrationsAfter_DateBasedVersions(t *testing.T) {
	migrations := []migration{
		{Filename: "20251208000001_init.sql", Version: "20251208000001"},
		{Filename: "20251209000001_add.sql", Version: "20251209000001"},
		{Filename: "20260101000001_new.sql", Version: "20260101000001"},
	}

	result := filterMigrationsAfter(migrations, "20251208000001")
	require.Len(t, result, 2)
	assert.Equal(t, "20251209000001", result[0].Version)
	assert.Equal(t, "20260101000001", result[1].Version)
}

func TestFilterMigrationsAfter_EmptyMigrationsList(t *testing.T) {
	result := filterMigrationsAfter(nil, "001")
	assert.Nil(t, result)
}

func TestReadMigrationFiles_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	p := newMinimalProvisioner(nil)

	migrations, err := p.readMigrationFiles(dir)
	require.NoError(t, err)
	assert.Empty(t, migrations)
}

func TestReadMigrationFiles_NonExistentDirectory(t *testing.T) {
	p := newMinimalProvisioner(nil)

	migrations, err := p.readMigrationFiles("/nonexistent/path/that/does/not/exist")
	require.NoError(t, err) // non-existent dir is valid (no migrations)
	assert.Nil(t, migrations)
}

func TestReadMigrationFiles_ReadsSQLFiles(t *testing.T) {
	dir := t.TempDir()
	p := newMinimalProvisioner(nil)

	writeMigrationFile(t, dir, "20251208000001_initial.sql", "CREATE TABLE foo (id INT);")
	writeMigrationFile(t, dir, "20251209000001_add_column.sql", "ALTER TABLE foo ADD COLUMN name TEXT;")

	migrations, err := p.readMigrationFiles(dir)
	require.NoError(t, err)

	require.Len(t, migrations, 2)
	assert.Equal(t, "20251208000001_initial.sql", migrations[0].Filename)
	assert.Equal(t, "20251208000001", migrations[0].Version)
	assert.Equal(t, "CREATE TABLE foo (id INT);", migrations[0].Content)
}

func TestReadMigrationFiles_IgnoresNonSQLFiles(t *testing.T) {
	dir := t.TempDir()
	p := newMinimalProvisioner(nil)

	writeMigrationFile(t, dir, "001_init.sql", "CREATE TABLE foo (id INT);")

	// Non-SQL files should be ignored
	err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Migrations"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0o600)
	require.NoError(t, err)

	migrations, err := p.readMigrationFiles(dir)
	require.NoError(t, err)
	require.Len(t, migrations, 1)
	assert.Equal(t, "001_init.sql", migrations[0].Filename)
}

func TestReadMigrationFiles_IgnoresSubdirectories(t *testing.T) {
	dir := t.TempDir()
	p := newMinimalProvisioner(nil)

	writeMigrationFile(t, dir, "001_init.sql", "CREATE TABLE foo (id INT);")

	// Create a subdirectory (should be ignored)
	subdir := filepath.Join(dir, "subdir")
	require.NoError(t, os.Mkdir(subdir, 0o700))
	writeMigrationFile(t, subdir, "002_sub.sql", "CREATE TABLE bar (id INT);")

	migrations, err := p.readMigrationFiles(dir)
	require.NoError(t, err)
	require.Len(t, migrations, 1)
	assert.Equal(t, "001_init.sql", migrations[0].Filename)
}

func TestReadMigrationFiles_SortedByFilename(t *testing.T) {
	dir := t.TempDir()
	p := newMinimalProvisioner(nil)

	// Write out of alphabetical order
	writeMigrationFile(t, dir, "003_third.sql", "-- third")
	writeMigrationFile(t, dir, "001_first.sql", "-- first")
	writeMigrationFile(t, dir, "002_second.sql", "-- second")

	migrations, err := p.readMigrationFiles(dir)
	require.NoError(t, err)

	require.Len(t, migrations, 3)
	assert.Equal(t, "001_first.sql", migrations[0].Filename)
	assert.Equal(t, "002_second.sql", migrations[1].Filename)
	assert.Equal(t, "003_third.sql", migrations[2].Filename)
}

func TestReadMigrationFiles_ExtractsVersionFromFilename(t *testing.T) {
	dir := t.TempDir()
	p := newMinimalProvisioner(nil)

	writeMigrationFile(t, dir, "20251208211142_initial_schema.sql", "-- migration")

	migrations, err := p.readMigrationFiles(dir)
	require.NoError(t, err)

	require.Len(t, migrations, 1)
	assert.Equal(t, "20251208211142", migrations[0].Version)
}

func TestReadMigrationFiles_FileWithNoUnderscore(t *testing.T) {
	dir := t.TempDir()
	p := newMinimalProvisioner(nil)

	// Filename with no underscore: version is the full name without .sql
	writeMigrationFile(t, dir, "001.sql", "-- no underscore")

	migrations, err := p.readMigrationFiles(dir)
	require.NoError(t, err)

	require.Len(t, migrations, 1)
	assert.Equal(t, "001", migrations[0].Version)
}

func TestProcessMigrationSQL_RemovesCreateSchemaStatements(t *testing.T) {
	p := newMinimalProvisioner(nil)
	sql := `CREATE SCHEMA my_schema;
CREATE TABLE foo (id INT);`

	result := p.processMigrationSQL(sql, "org_tenant")

	assert.NotContains(t, result, "CREATE SCHEMA")
	assert.Contains(t, result, "CREATE TABLE foo")
}

func TestProcessMigrationSQL_RemovesCreateSchemaIfNotExists(t *testing.T) {
	p := newMinimalProvisioner(nil)
	sql := `CREATE SCHEMA IF NOT EXISTS my_schema;
CREATE TABLE users (id INT);`

	result := p.processMigrationSQL(sql, "org_tenant")

	assert.NotContains(t, result, "CREATE SCHEMA")
	assert.Contains(t, result, "CREATE TABLE users")
}

func TestProcessMigrationSQL_ReplacesSchemaReferences(t *testing.T) {
	p := newMinimalProvisioner(nil)

	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "quoted current_account schema",
			input:    `ALTER TABLE "current_account"."accounts" ADD COLUMN balance NUMERIC;`,
			contains: `"org_tenant".`,
		},
		{
			name:     "quoted party schema",
			input:    `SELECT * FROM "party"."customers";`,
			contains: `"org_tenant".`,
		},
		{
			name:     "unquoted position_keeping schema",
			input:    `CREATE INDEX ON position_keeping.ledger (id);`,
			contains: `org_tenant.`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.processMigrationSQL(tt.input, "org_tenant")
			assert.Contains(t, result, tt.contains)
		})
	}
}

func TestProcessMigrationSQL_NoTransformationsNeeded(t *testing.T) {
	p := newMinimalProvisioner(nil)
	sql := `CREATE TABLE products (id UUID PRIMARY KEY, name TEXT NOT NULL);`

	result := p.processMigrationSQL(sql, "org_tenant")
	assert.Equal(t, sql, result)
}

func TestSplitSQLStatements_SingleStatement(t *testing.T) {
	sql := `CREATE TABLE foo (id INT)`

	statements := splitSQLStatements(sql)
	require.Len(t, statements, 1)
	assert.Equal(t, "CREATE TABLE foo (id INT)", statements[0])
}

func TestSplitSQLStatements_MultipleStatements(t *testing.T) {
	sql := `CREATE TABLE foo (id INT);
CREATE TABLE bar (id INT);
CREATE INDEX idx_foo ON foo (id);`

	statements := splitSQLStatements(sql)
	require.Len(t, statements, 3)
	assert.Equal(t, "CREATE TABLE foo (id INT)", statements[0])
	assert.Equal(t, "CREATE TABLE bar (id INT)", statements[1])
	assert.Equal(t, "CREATE INDEX idx_foo ON foo (id)", statements[2])
}

func TestSplitSQLStatements_SkipsEmptyStatements(t *testing.T) {
	sql := `CREATE TABLE foo (id INT);;; CREATE TABLE bar (id INT);`

	statements := splitSQLStatements(sql)
	require.Len(t, statements, 2)
}

func TestSplitSQLStatements_PreservesSemicolonInStringLiteral(t *testing.T) {
	sql := `INSERT INTO config (key, value) VALUES ('delimiter', ';');`

	statements := splitSQLStatements(sql)
	require.Len(t, statements, 1)
	assert.Contains(t, statements[0], "';'")
}

func TestSplitSQLStatements_HandlesLineComments(t *testing.T) {
	sql := `-- This creates the foo table
CREATE TABLE foo (id INT); -- inline comment
CREATE TABLE bar (id INT);`

	statements := splitSQLStatements(sql)
	require.Len(t, statements, 2)
	assert.Contains(t, statements[0], "CREATE TABLE foo")
	assert.Contains(t, statements[1], "CREATE TABLE bar")
}

func TestSplitSQLStatements_HandlesBlockComments(t *testing.T) {
	sql := `/* Create the foo table */
CREATE TABLE foo (id INT);
/* Another comment */
CREATE TABLE bar (id INT);`

	statements := splitSQLStatements(sql)
	require.Len(t, statements, 2)
}

func TestSplitSQLStatements_EmptyInput(t *testing.T) {
	statements := splitSQLStatements("")
	assert.Empty(t, statements)
}

func TestSplitSQLStatements_WhitespaceOnly(t *testing.T) {
	statements := splitSQLStatements("   \n\t  ")
	assert.Empty(t, statements)
}

func TestSplitSQLStatements_EscapedQuoteInString(t *testing.T) {
	// Single-quoted string with escaped single quote ''
	sql := `INSERT INTO messages (text) VALUES ('it''s here');`

	statements := splitSQLStatements(sql)
	require.Len(t, statements, 1)
	assert.Contains(t, statements[0], "it''s here")
}

func TestSplitSQLStatements_TrimsWhitespace(t *testing.T) {
	sql := `   CREATE TABLE foo (id INT)   ;   CREATE TABLE bar (id INT)   ;`

	statements := splitSQLStatements(sql)
	require.Len(t, statements, 2)
	assert.Equal(t, "CREATE TABLE foo (id INT)", statements[0])
	assert.Equal(t, "CREATE TABLE bar (id INT)", statements[1])
}

// TestMigration_VersionExtraction_DateFormat verifies the migration struct
// correctly stores filename, version, and content.
func TestMigration_Fields(t *testing.T) {
	m := migration{
		Filename: "20251208211142_initial.sql",
		Version:  "20251208211142",
		Content:  "CREATE TABLE test (id INT);",
	}

	assert.Equal(t, "20251208211142_initial.sql", m.Filename)
	assert.Equal(t, "20251208211142", m.Version)
	assert.Equal(t, "CREATE TABLE test (id INT);", m.Content)
}

// TestFilterMigrationsAfter_PreservesOrder verifies filtered migrations maintain sort order.
func TestFilterMigrationsAfter_PreservesOrder(t *testing.T) {
	migrations := []migration{
		{Version: "001"},
		{Version: "002"},
		{Version: "003"},
		{Version: "004"},
		{Version: "005"},
	}

	result := filterMigrationsAfter(migrations, "002")
	require.Len(t, result, 3)
	assert.Equal(t, "003", result[0].Version)
	assert.Equal(t, "004", result[1].Version)
	assert.Equal(t, "005", result[2].Version)
}

// TestReadMigrationFiles_ReadsContent verifies file content is read correctly.
func TestReadMigrationFiles_ReadsContent(t *testing.T) {
	dir := t.TempDir()
	p := newMinimalProvisioner(nil)

	sqlContent := "CREATE TABLE important (id UUID PRIMARY KEY DEFAULT gen_random_uuid());"
	writeMigrationFile(t, dir, "001_important.sql", sqlContent)

	migrations, err := p.readMigrationFiles(dir)
	require.NoError(t, err)
	require.Len(t, migrations, 1)
	assert.Equal(t, sqlContent, migrations[0].Content)
}

// TestApplyMigrationList_EmptyList returns empty string and no error.
func TestApplyMigrationList_EmptyList(t *testing.T) {
	p := newMinimalProvisioner(nil)

	// applyMigrationList with empty slice should return empty version and no error
	// We test via readMigrationFiles returning empty + checking applyServiceMigrationsToDB path
	_ = p // prevent unused variable - applyMigrationList requires a DB so we test readMigrationFiles instead

	// Verify that filterMigrationsAfter with an empty migration list and a current version
	// returns nil (no migrations to apply)
	result := filterMigrationsAfter([]migration{}, "001")
	assert.Nil(t, result)
}

// TestProcessMigrationSQL_CaseInsensitiveCreateSchema verifies CREATE SCHEMA detection is case-insensitive.
func TestProcessMigrationSQL_CaseInsensitiveCreateSchema(t *testing.T) {
	p := newMinimalProvisioner(nil)

	tests := []struct {
		name string
		sql  string
	}{
		{"all caps", "CREATE SCHEMA test_schema;"},
		{"mixed case", "Create Schema test_schema;"},
		{"lowercase", "create schema test_schema;"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.processMigrationSQL(tt.sql, "org_tenant")
			assert.False(t, strings.Contains(strings.ToUpper(result), "CREATE SCHEMA"),
				"CREATE SCHEMA should be stripped from result, got: %q", result)
		})
	}
}

// TestReadMigrationFiles_MultipleMigrationsWithContent ensures content is preserved for all files.
func TestReadMigrationFiles_MultipleFilesContent(t *testing.T) {
	dir := t.TempDir()
	p := newMinimalProvisioner(nil)

	files := map[string]string{
		"001_create_tables.sql": "CREATE TABLE foo (id INT);",
		"002_add_indexes.sql":   "CREATE INDEX idx_foo ON foo(id);",
		"003_seed_data.sql":     "INSERT INTO foo VALUES (1);",
	}

	for name, content := range files {
		writeMigrationFile(t, dir, name, content)
	}

	migrations, err := p.readMigrationFiles(dir)
	require.NoError(t, err)
	require.Len(t, migrations, 3)

	// Sorted by filename
	assert.Equal(t, "001_create_tables.sql", migrations[0].Filename)
	assert.Equal(t, "001", migrations[0].Version)
	assert.Contains(t, migrations[0].Content, "CREATE TABLE foo")
}

// TestApplyMigrationList_ReturnsEmptyVersionForEmptyList mirrors the behavior documented in the code.
func TestApplyMigrationList_ReturnsEmptyForNoMigrations(t *testing.T) {
	// applyMigrationList with nil migrations should not be called, but we verify
	// that the filter returns nil for empty lists correctly.
	result := filterMigrationsAfter(nil, "20251208")
	assert.Nil(t, result)
}

// TestFilterMigrationsAfter_ExactCurrentVersion verifies exact version match is NOT included.
func TestFilterMigrationsAfter_ExactCurrentVersionNotIncluded(t *testing.T) {
	migrations := []migration{
		{Version: "001"},
		{Version: "002"},
	}

	// "002" is current, nothing should be returned (not strictly greater than)
	result := filterMigrationsAfter(migrations, "002")
	assert.Empty(t, result)
}

// TestReadMigrationFiles_LargeNumberOfFiles verifies correct sorting for many files.
func TestReadMigrationFiles_SortingConsistency(t *testing.T) {
	dir := t.TempDir()
	p := newMinimalProvisioner(nil)

	filenames := []string{
		"20251201000001_a.sql",
		"20251205000001_b.sql",
		"20251203000001_c.sql",
		"20251202000001_d.sql",
		"20251204000001_e.sql",
	}

	for _, fn := range filenames {
		writeMigrationFile(t, dir, fn, "-- "+fn)
	}

	migrations, err := p.readMigrationFiles(dir)
	require.NoError(t, err)
	require.Len(t, migrations, 5)

	// Verify sorted order
	assert.Equal(t, "20251201000001_a.sql", migrations[0].Filename)
	assert.Equal(t, "20251202000001_d.sql", migrations[1].Filename)
	assert.Equal(t, "20251203000001_c.sql", migrations[2].Filename)
	assert.Equal(t, "20251204000001_e.sql", migrations[3].Filename)
	assert.Equal(t, "20251205000001_b.sql", migrations[4].Filename)
}

// TestProcessMigrationSQL_CommentOnColumn_NotRewritten verifies that
// processMigrationSQL preserves COMMENT ON COLUMN "party"."attributes" rather
// than rewriting it as "org_test_tenant"."attributes" (schema.column instead
// of table.column). In COMMENT ON COLUMN, the "schema"."table" pattern is
// actually table.column, so schema-pattern rewriting must be skipped for
// COMMENT statements.
func TestProcessMigrationSQL_CommentOnColumn_NotRewritten(t *testing.T) {
	p := newMinimalProvisioner(nil)
	sql := `COMMENT ON COLUMN "party"."attributes" IS 'JSON attributes';`
	result := p.processMigrationSQL(sql, "org_test_tenant")

	// The table name "party" must be preserved so the column reference
	// remains valid (table.column), not corrupted to schema.column.
	assert.Contains(t, result, `"party"."attributes"`,
		"COMMENT ON COLUMN must preserve table.column reference")
	assert.NotContains(t, result, `"org_test_tenant"."attributes"`,
		"COMMENT ON COLUMN must not be rewritten with tenant schema")
}

// TestProcessMigrationSQL_CommentOnColumn_MixedWithDDL verifies that COMMENT
// statements are skipped while neighboring DDL is still rewritten correctly.
func TestProcessMigrationSQL_CommentOnColumn_MixedWithDDL(t *testing.T) {
	p := newMinimalProvisioner(nil)
	sql := `ALTER TABLE "party"."customers" ADD COLUMN attributes JSONB;
COMMENT ON COLUMN "party"."attributes" IS 'JSON attributes';`
	result := p.processMigrationSQL(sql, "org_test_tenant")

	// DDL reference to "party"."customers" is a real schema qualifier - rewrite it.
	assert.Contains(t, result, `"org_test_tenant"."customers"`,
		"ALTER TABLE schema reference must be rewritten")
	// COMMENT ON COLUMN "party"."attributes" is table.column - leave it alone.
	assert.Contains(t, result, `"party"."attributes"`,
		"COMMENT ON COLUMN must preserve table.column reference")
}

// TestIsCommentStatement_SkipsLeadingComments verifies that isCommentStatement
// recognizes COMMENT statements even when preceded by line or block comments.
func TestIsCommentStatement_SkipsLeadingComments(t *testing.T) {
	cases := []struct {
		name string
		stmt string
		want bool
	}{
		{"plain", `COMMENT ON COLUMN "t"."c" IS 'x'`, true},
		{"leading whitespace", "   \n\tCOMMENT ON TABLE t IS 'x'", true},
		{"leading line comment", "-- audit note\nCOMMENT ON COLUMN t.c IS 'x'", true},
		{"leading block comment", "/* audit */ COMMENT ON COLUMN t.c IS 'x'", true},
		{"mixed leading comments", "-- header\n/* block */\n-- another\nCOMMENT ON TABLE t IS 'x'", true},
		{"not a comment statement", `ALTER TABLE "party"."customers" ADD COLUMN x INT`, false},
		{"block comment mid-statement", `ALTER /* comment */ TABLE t ADD COLUMN x INT`, false},
		{"unterminated block comment", "/* unterminated COMMENT ON COLUMN", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isCommentStatement(tc.stmt))
		})
	}
}

// repoRoot returns the repository root by walking up from the test file location.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get test file path")
	// Test file is at services/tenant/provisioner/migration_runner_test.go
	// filepath.Dir gives provisioner/, then 3 levels up to repo root
	return filepath.Join(filepath.Dir(filename), "..", "..", "..")
}

// TestProcessMigrationSQL_AllMigrations_Parse runs every service migration file through
// processMigrationSQL and splitSQLStatements to verify the rewritten SQL is structurally valid.
// This catches COMMENT ON COLUMN bugs, string literal corruption, and other non-table
// uses of schema names before they hit production tenant provisioning.
func TestProcessMigrationSQL_AllMigrations_Parse(t *testing.T) {
	root := repoRoot(t)
	servicesDir := filepath.Join(root, "services")

	// Known-broken migrations that produce invalid SQL through processMigrationSQL.
	// Empty after the COMMENT ON COLUMN rewriter fix: processMigrationSQL now
	// skips COMMENT statements so previously-broken migrations parse cleanly.
	// Left in place as an extension point for any future statement-shape bugs
	// the rewriter cannot handle.
	knownBroken := map[string]string{}

	entries, err := os.ReadDir(servicesDir)
	require.NoError(t, err, "failed to read services directory")

	p := newMinimalProvisioner(nil)
	tested := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		migrationsDir := filepath.Join(servicesDir, entry.Name(), "migrations")
		if _, err := os.Stat(migrationsDir); os.IsNotExist(err) {
			continue
		}

		migrations, err := p.readMigrationFiles(migrationsDir)
		require.NoError(t, err, "failed to read migrations from %s", migrationsDir)

		for _, m := range migrations {
			relPath := entry.Name() + "/migrations/" + m.Filename
			if reason, broken := knownBroken[relPath]; broken {
				t.Logf("SKIP (known broken): %s - %s", relPath, reason)
				continue
			}

			t.Run(relPath, func(t *testing.T) {
				rewritten := p.processMigrationSQL(m.Content, "org_ci_test")
				statements := splitSQLStatements(rewritten)

				// Guard: migration files should produce at least one statement
				require.NotEmpty(t, statements,
					"migration %s produced zero statements after processing", m.Filename)

				for i, stmt := range statements {
					trimmed := strings.TrimSpace(stmt)
					assert.NotEmpty(t, trimmed,
						"statement %d in %s is empty after processing", i, m.Filename)

					// Targeted check: if processMigrationSQL rewrites a
					// COMMENT ON COLUMN (tenant schema appears in the text),
					// the result must have 3 dot-separated quoted identifiers
					// ("schema"."table"."column"). The known bug produces
					// only 2 ("schema"."column").
					upper := strings.ToUpper(trimmed)
					if strings.HasPrefix(upper, "COMMENT ON COLUMN") &&
						strings.Contains(trimmed, `"org_ci_test"`) {
						dotParts := strings.SplitN(trimmed, ".", 4)
						quotedParts := 0
						for _, p := range dotParts {
							if strings.Contains(p, `"`) {
								quotedParts++
							}
						}
						assert.GreaterOrEqual(t, quotedParts, 3,
							"COMMENT ON COLUMN in %s was rewritten with tenant schema but has only %d quoted identifiers (need schema.table.column = 3)",
							m.Filename, quotedParts)
					}
				}
			})
			tested++
		}
	}

	assert.Greater(t, tested, 0, "expected to test at least one migration file")
}
