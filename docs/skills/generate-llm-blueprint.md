---
name: generate-llm-blueprint
description: Generate codebase blueprint for Claude Projects or other LLMs
triggers:
  - Uploading codebase to Claude Projects
  - Creating context for LLMs
  - Repomix blueprint generation
instructions: |
  Run repomix from meridian-main to generate meridian-blueprint.md.
  Includes docs, protos, SQL, atlas configs. Excludes Go implementation code.
---

# Generate LLM Blueprint

Generate a comprehensive codebase blueprint for uploading to Claude Projects or other LLMs.

## Command

Run from the `meridian-main` directory:

```bash
repomix --style markdown \
  --include "**/*.md,api/proto/**/*.proto,**/*.sql,**/atlas.hcl,**/openapi.json,go.mod" \
  --ignore "node_modules/**,**/CHANGELOG.md" \
  --remove-empty-lines \
  -o ../meridian-blueprint.md
```

## What's Included

| Content | Purpose |
|---------|---------|
| Markdown docs | PRDs, ADRs, architecture, runbooks, READMEs |
| Proto files | gRPC API contracts and data models |
| SQL migrations | Database schemas |
| Atlas HCL | Migration configurations |
| OpenAPI specs | REST API definitions |
| go.mod | Dependency graph |

## What's Excluded

- Go implementation code (too verbose for context windows)
- Test files
- Generated protobuf code (`*.pb.go`, `*_grpc.pb.go`)
- node_modules
- CHANGELOG files

## Why This Works

The markdown documentation serves as a compressed blueprint of system behavior. ADRs capture
architectural decisions and rationale, PRDs define what's being built, and proto files define the
contracts. An LLM with this context can understand *how Meridian works* without reading 861k+ tokens
of implementation code.

## Output Location

The file is written to `../meridian-blueprint.md` (repo root, outside `meridian-main`) to keep it
separate from the codebase and easily accessible for upload.
