# Service Coupling Analysis Scripts

This directory contains scripts for analyzing and quantifying coupling between microservices in the Meridian platform.

## Scripts Overview

### 1. `analyze-coupling.sh`

Analyzes the codebase for coupling violations and patterns.

**Output**: JSON report with violations, proto usage, gRPC clients, database schemas, and Kafka patterns.

**Usage**:

```bash
./scripts/analyze-coupling.sh > output.json
```

**What it detects**:

- Cross-service imports (violations)
- Internal platform imports
- Proto message usage (expected for gRPC)
- Database schema ownership
- Kafka event patterns

### 2. `calculate-coupling-metrics.sh`

Calculates quantitative coupling metrics for all services.

**Output**: `docs/architecture/coupling-metrics.json`

**Usage**:

```bash
./scripts/calculate-coupling-metrics.sh
```

**Metrics calculated**:

- **Afferent Coupling (Ca)**: Number of services depending on this service
- **Efferent Coupling (Ce)**: Number of services this service depends on
- **Instability (I)**: Ce / (Ca + Ce) - measures resistance to change
  - 0 = maximally stable (only depended upon)
  - 1 = maximally unstable (only depends on others)
  - Ideal range: 0.3-0.7
- **Distance from Main Sequence**: |A + I - 1| where A is abstractness
- **Assessment**: Qualitative evaluation (stable, acceptable, too-dependent, too-rigid)

**Assessment thresholds**:

- `stable`: I < 0.3 and Ca ≤ 3
- `acceptable`: 0.3 ≤ I ≤ 0.7
- `too-dependent`: I > 0.7 and Ce > Ca
- `too-rigid`: I < 0.3 and Ca > 3

### 3. `generate-coupling-mermaid.sh`

Generates Mermaid diagrams visualizing service dependencies.

**Output**: `docs/architecture/coupling-diagram.md`

**Usage**:

```bash
./scripts/generate-coupling-mermaid.sh
```

**Diagram types**:

- Service dependency graph
- Coupling metrics table
- Platform dependencies visualization

### 4. `test-coupling-metrics.sh`

Validates coupling metrics calculations.

**Usage**:

```bash
./scripts/test-coupling-metrics.sh
```

**Test suites**:

1. Efferent Coupling verification
2. Afferent Coupling verification
3. Instability calculations
4. Assessment classifications
5. JSON structure validation
6. Value range checks
7. Architectural expectations

## Workflow

**Full analysis workflow**:

```bash
# 1. Run coupling analysis
./scripts/analyze-coupling.sh

# 2. Calculate metrics
./scripts/calculate-coupling-metrics.sh

# 3. Generate visualizations
./scripts/generate-coupling-mermaid.sh

# 4. Validate results
./scripts/test-coupling-metrics.sh
```

## Current Metrics (2025-11-19)

### Service Coupling Summary

| Service | Ca | Ce | Instability | Assessment |
|---------|----|----|-------------|------------|
| position-keeping | 1 | 0 | 0.00 | stable |
| financial-accounting | 1 | 0 | 0.00 | stable |
| current-account | 0 | 2 | 1.00 | too-dependent |

### Interpretation

**position-keeping** (I=0, stable):

- Pure provider service
- Depended upon by current-account
- Has no service dependencies
- Low risk of change propagation

**financial-accounting** (I=0, stable):

- Pure provider service
- Depended upon by current-account
- Has no service dependencies
- Low risk of change propagation

**current-account** (I=1.0, too-dependent):

- Pure consumer/orchestrator service
- Depends on both position-keeping and financial-accounting
- No services depend on it
- High instability - changes to dependencies will affect this service
- Expected pattern for an orchestration service

### Architectural Insights

1. **Clear Layer Separation**: The architecture shows a clean separation with current-account as an
   orchestrator and the other two services as providers.

2. **Low Coupling Risk**: With only 3 services and instability values at the extremes (0 and 1), the
   coupling is well-defined and manageable.

3. **Expected Pattern**: current-account's high instability (I=1.0) is expected and acceptable for an
   orchestration service that coordinates between other services.

4. **Future Consideration**: As the system grows, monitor if current-account's efferent coupling (Ce)
   increases significantly, which could indicate it's becoming too complex and should be split.

## Metrics Reference

### Instability (I)

- **Formula**: Ce / (Ca + Ce)
- **Range**: 0 to 1
- **Interpretation**:
  - Low (0-0.3): Stable, many dependents, few dependencies
  - Medium (0.3-0.7): Balanced, moderate change risk
  - High (0.7-1.0): Unstable, few dependents, many dependencies

### Distance from Main Sequence (D)

- **Formula**: |A + I - 1| where A is abstractness
- **Ideal**: Close to 0 (on the main sequence)
- **Interpretation**:
  - Zone of Pain: Low abstractness, high stability (D > 0.5, I < 0.3)
  - Zone of Uselessness: High abstractness, high instability (D > 0.5, I > 0.7)

## Troubleshooting

**Issue**: Metrics don't match expectations

- Run `./scripts/test-coupling-metrics.sh` to validate calculations
- Check `docs/architecture/coupling-metrics.json` for raw data
- Review coupling analysis output for violations

**Issue**: Services not detected

- Ensure services are in `internal/<service-name>/` directories
- Check that services have proper proto definitions
- Verify service names match directory names

## Future Enhancements

1. **Abstractness Calculation**: Calculate actual abstractness from interface/concrete ratio
2. **Trend Tracking**: Store historical metrics to track coupling evolution
3. **Threshold Alerts**: Automated warnings when metrics exceed thresholds
4. **Event Coupling**: Analyze Kafka event dependencies more deeply
5. **Database Coupling**: Detect shared schema access patterns
