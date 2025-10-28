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

1. **gofumpt**: Formats all staged Go files with stricter formatting rules
2. **golangci-lint**: Lints all staged Go files against project standards

If any check fails, the commit will be blocked. Fix the issues and try again.

## Automatic Tool Installation

The pre-commit hook will automatically install missing tools:
- `gofumpt` - via `go install`
- `golangci-lint v2.5.0` - via official install script

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
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.5.0
```

## CI Integration

These hooks run the same checks as our GitHub Actions CI:
- `.github/workflows/lint.yml` - Matches hook checks
- Ensures commits that pass locally will pass CI
