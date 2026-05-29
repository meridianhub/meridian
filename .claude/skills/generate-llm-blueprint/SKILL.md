---
name: generate-llm-blueprint
description: Generate codebase blueprint for Claude Projects or other LLMs
triggers:
  - Uploading codebase to Claude Projects
  - Creating context for LLMs
  - Repomix blueprint generation
instructions: |
  Run repomix from meridian-main to generate meridian-blueprint.md.
  Includes PRDs, ADRs, architecture, protos, atlas configs. Excludes Go code,
  SQL migrations, service READMEs, skills docs, and API contract docs.
---

# Generate LLM Blueprint

Generate a codebase blueprint for uploading to Claude Projects or other LLMs.

## Command

Run from the `meridian-main` directory:

<!-- markdownlint-disable MD013 -->

```bash
repomix --style markdown \
  --include "docs/prd/**/*.md,docs/adr/**/*.md,docs/architecture/**/*.md,docs/guides/**/*.md,api/proto/**/*.proto,**/atlas.hcl,**/openapi.json,go.mod,CLAUDE.md,CONTRIBUTING.md,README.md" \
  --ignore "node_modules/**,**/CHANGELOG.md,docs/architecture/api-contracts/**,.claude/skills/**,services/**/*.md" \
  --remove-empty-lines \
  -o ../meridian-blueprint.md
```

<!-- markdownlint-enable MD013 -->

## What's Included

| Content | ~Tokens | Purpose |
|---------|---------|---------|
| PRDs (`docs/prd/`) | 261k | Project logic and feature blueprints |
| ADRs (`docs/adr/`) | 146k | Architectural decisions and rationale |
| Proto files (`api/proto/`) | 127k | gRPC API contracts and data models |
| Architecture docs | 46k | System design (excluding API contract docs) |
| Guides | 41k | Development conventions and checklists |
| Atlas HCL, OpenAPI, go.mod | 11k | Migration configs, REST specs, dependencies |

## What's Excluded

- Go implementation code (too verbose for context windows)
- SQL migrations (~57k tokens, redundant with proto definitions)
- API contract docs (~31k tokens, redundant with proto files)
- Service READMEs (~67k tokens, overlap with architecture docs)
- Skills docs (~20k tokens, internal tooling instructions)
- Test files, generated protobuf code, node_modules, CHANGELOGs

## Why This Works

PRDs define what's being built, ADRs capture why decisions were made, and proto files define the
contracts. An LLM with this context can understand *how Meridian works* without reading implementation
code. SQL migrations and API contract docs were removed as they duplicate information already captured
by proto files and architecture docs.

## Output Location

The file is written to `../meridian-blueprint.md` (repo root, outside `meridian-main`) to keep it
separate from the codebase and easily accessible for upload.
