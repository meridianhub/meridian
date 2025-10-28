# Meridian - Production-Grade Open Banking Ledger

A production-grade open banking ledger implementing BIAN (Banking Industry Architecture Network) standards.

## Project Structure

This project follows a worktree-based development pattern for parallel work on multiple features:

```
meridian/
├── .taskmaster/        # Task Master AI project management
├── CLAUDE.md          # Claude Code instructions
├── .mcp.json          # MCP server configuration
├── meridian-main/     # Main git repository (default branch: develop)
└── worktree/          # Git worktrees for parallel development
    ├── 1-infra/       # Infrastructure workstream
    ├── 2-api/         # API contracts workstream
    └── ...            # Additional feature branches
```

## Development Workflow

### Initial Setup

1. **Clone the repository** (if starting fresh):
   ```bash
   git clone git@github.com:bjcoombs/meridian.git meridian-main
   cd ..
   mkdir worktree
   ```

2. **Create a worktree for your feature**:
   ```bash
   cd meridian-main
   git worktree add ../worktree/1-infra 1-infra
   cd ../worktree/1-infra
   ```

3. **Start working**:
   ```bash
   # Start Claude Code in the worktree
   claude

   # Or work directly with Task Master
   cd ../.. # Back to parent meridian/ directory
   task-master next --tag=1-infra
   ```

### Task Master Integration

Task Master coordinates all development work from the parent `meridian/` directory:

```bash
# View tasks for a workstream
task-master list --tag=1-infra

# Get next task
task-master next --tag=1-infra

# View task details
task-master show 1.2

# Mark task as in progress
task-master set-status --id=1.2 --status=in-progress

# Update task with implementation notes
task-master update-subtask --id=1.2.1 --prompt="Implemented Docker configuration with multi-stage builds"

# Mark complete
task-master set-status --id=1.2 --status=done
```

### Multi-Worktree Development

Work on multiple features simultaneously:

```bash
# Terminal 1: Infrastructure work
cd meridian/worktree/1-infra && claude

# Terminal 2: API development
cd meridian/worktree/2-api && claude

# Terminal 3: Platform services
cd meridian/worktree/3-platform && claude
```

Each Claude Code session has access to the same Task Master instance for coordination.

## GitHub Issues

Each workstream is tracked as a GitHub issue:

- **Issue #1**: Infrastructure & Deployment (`1-infra` tag)
- **Issue #2**: API Contracts (`2-api-contracts` tag)
- **Issue #3**: Platform Services (`3-platform` tag)
- **Issue #4**: Financial Accounting (`4-financial-accounting` tag)
- **Issue #5**: Position Keeping (`5-position-keeping` tag)
- **Issue #6**: Current Account (`6-current-account` tag)

## BIAN Standards

This project implements the following BIAN service domains:

- **FinancialAccounting**: Double-entry general ledger
- **PositionKeeping**: Pre-ledger transaction log
- **CurrentAccount**: Customer-facing accounts
- **AccountReconciliation**: Transaction verification

Reference specifications: `../bian/bian-public-main/release13.0.0/`

## Technology Stack

- **Language**: Go 1.25.3+
- **API**: Protocol Buffers 3 + gRPC
- **Database**: CockroachDB or YugabyteDB
- **Messaging**: Apache Kafka 3.x
- **Cache**: Redis 7.x
- **Orchestration**: Kubernetes 1.28+
- **Local Dev**: Tilt
- **Observability**: OpenTelemetry + Prometheus + Grafana

## Architecture Decision Records

See [docs/adr/README.md](docs/adr/README.md) for architectural decisions.

## Contributing

1. Create a worktree for your feature
2. Follow Task Master workflow
3. Create PR when ready
4. Link PR to the corresponding GitHub issue

---

For detailed development guidelines, see the CLAUDE.md file in the parent directory.
