# Git Hooks for Meridian

This directory contains shareable git hooks for the Meridian project.

## Installation

To install these hooks for your local repository:

```bash
./.githooks/install.sh
```

This will copy the hooks to your `.git/hooks/` directory.

## Hooks

### pre-commit

Runs before each commit to ensure code quality:

#### Proto File Validation

1. **buf lint**: Validates proto file style and correctness
2. **buf breaking**: Checks for breaking schema changes against develop branch

#### Markdown File Validation

1. **markdownlint-cli2**: Validates markdown file formatting and style

#### Go File Validation

1. **gofumpt**: Formats all staged Go files with stricter formatting rules
2. **golangci-lint**: Lints all staged Go files against project standards

If any check fails, the commit will be blocked. Fix the issues and try again.

**Proto Evolution**: For guidance on safe schema changes, see `.claude/skills/schema-evolution/SKILL.md`

## Automatic Tool Installation

The pre-commit hook will automatically install missing tools:

- `buf` - via `go install` (for proto validation)
- `markdownlint-cli2` - via `npx` (for markdown linting)
- `gofumpt` - via `go install` (for Go formatting)
- `golangci-lint v2.5.0` - via official install script (for Go linting)

**Note**: Markdown linting requires Node.js and npm to be installed.

## Skipping Hooks (Emergency Only)

If you absolutely need to skip hooks (not recommended):

```bash
git commit --no-verify -m "your message"
```

**Warning**: This bypasses all quality checks and may break CI.

## Troubleshooting

### Hook not running

Check if the hook is installed and executable:

```bash
ls -la .git/hooks/pre-commit
```

If missing, run `./.githooks/install.sh`

### Permission denied

Make the hook executable:

```bash
chmod +x .git/hooks/pre-commit
```

### golangci-lint not found

The hook should auto-install, but you can manually install:

```bash
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
  | sh -s -- -b $(go env GOPATH)/bin v2.5.0
```

## CI Integration

These hooks run the same checks as our GitHub Actions CI:

- `.github/workflows/proto.yml` - Matches proto validation checks
- `.github/workflows/markdown.yml` - Matches markdown linting checks
- `.github/workflows/quality.yml` - Matches Go linting checks
- Ensures commits that pass locally will pass CI

## Schema Evolution Workflow

When modifying proto files, the hook will:

1. Check style with `buf lint`
2. Verify compatibility with `buf breaking --against develop`
3. Block commit if breaking changes detected
4. Provide helpful tips for safe evolution patterns

For detailed guidance, see `.claude/skills/schema-evolution/SKILL.md`
