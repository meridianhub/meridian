# Position-Keeping Service - Chaos Testing Strategy

**Status**: Framework design (implementation pending)
**Target**: System resilience validation under failure conditions

## Overview

Chaos testing validates system resilience by intentionally injecting failures and verifying recovery behaviour. This
document outlines the chaos testing strategy for the position-keeping service.

## Objectives

1. **Validate Resilience**: Verify system handles failures gracefully
2. **Identify Weaknesses**: Discover failure modes before production
3. **Document Behaviour**: Establish expected recovery patterns
4. **Prevent Regressions**: Automated tests catch resilience issues

## Chaos Scenarios

### 1. Database Resilience

#### Scenario: Connection Loss During Transaction

```text
GIVEN: Active database transaction in progress
WHEN: Database connection is terminated mid-transaction
THEN:

  - Transaction rolls back cleanly
  - Error is returned to caller
  - No data corruption
  - Connection pool recovers
  - Subsequent requests succeed

```

**Test Approach**:

- Use testcontainers to pause/stop PostgreSQL container
- Inject failure at specific points in transaction lifecycle
- Verify rollback and error handling
- Validate connection pool recovery

#### Scenario: Database Unavailable on Startup

```text
GIVEN: Service starting up
WHEN: PostgreSQL is not yet available
THEN:

  - Service retries connection with exponential backoff
  - Health check reports unhealthy
  - Service eventually connects when database becomes available
  - No crashes or panics

```

#### Scenario: Slow Database Queries

```text
GIVEN: Normal operation
WHEN: Database queries become slow (>5s response time)
THEN:

  - Requests timeout appropriately
  - Circuit breaker activates (future enhancement)
  - Error messages are informative
  - System remains responsive for other operations

```

### 2. Kafka Event Publishing Resilience

#### Scenario: Kafka Broker Unavailable

```text
GIVEN: Event ready for publication
WHEN: Kafka broker is unreachable
THEN:

  - Publish operation fails with clear error
  - Domain state remains consistent
  - Transaction can be retried
  - No event loss or duplication

```

**Test Approach**:

- Mock Kafka publisher with controlled failures
- Verify event publisher interface contract
- Test retry logic (if implemented)
- Validate error propagation

#### Scenario: Kafka Topic Does Not Exist

```text
GIVEN: Event ready for publication
WHEN: Target topic has not been created
THEN:

  - Publish fails with descriptive error
  - System logs error clearly
  - Health check reflects Kafka status

```

### 3. gRPC Service Resilience

#### Scenario: High Concurrent Load

```text
GIVEN: Normal operation
WHEN: 1000 concurrent gRPC requests arrive
THEN:

  - All requests processed correctly
  - Response times remain acceptable
  - No goroutine leaks
  - Memory usage stays bounded
  - Database connection pool not exhausted

```

**Test Approach**:

- Use load testing tools (e.g., ghz)
- Monitor resource usage
- Verify connection pool limits
- Check for race conditions

#### Scenario: Invalid Request Payloads

```text
GIVEN: gRPC service running
WHEN: Client sends malformed/invalid requests
THEN:

  - Validation errors returned with clear messages
  - No panics or crashes
  - Other requests not affected
  - Attack attempts logged

```

### 4. Container/Network Failures

#### Scenario: Container Restart

```text
GIVEN: Service running with active connections
WHEN: Container is restarted (SIGTERM)
THEN:

  - Graceful shutdown initiated
  - In-flight requests complete (or timeout)
  - Database connections closed cleanly
  - Kafka connections closed cleanly
  - No data loss

```

#### Scenario: Network Partition

```text
GIVEN: Service communicating with database and Kafka
WHEN: Network partition occurs
THEN:

  - Operations fail with timeout errors
  - System doesn't hang indefinitely
  - Recovery happens when network restored
  - No permanent state corruption

```

## Implementation Approach

### Phase 1: Foundation (Current)

- ✅ Document chaos testing strategy
- ✅ Identify key failure scenarios
- ⏳ Define test framework approach

### Phase 2: Basic Scenarios

- Implement database connection failure tests
- Implement Kafka unavailability tests
- Add container lifecycle tests
- Validate graceful degradation

### Phase 3: Advanced Scenarios

- Load testing with resource constraints
- Network partition simulation
- Multi-failure scenarios
- Recovery time measurement

### Phase 4: Continuous Chaos

- Automated chaos tests in CI
- Scheduled chaos runs in staging
- Monitoring and alerting integration
- Incident playbook generation

## Test Framework Design

### Option 1: Testcontainers-Based (Recommended)

**Pros**:

- Already using testcontainers
- Full control over container lifecycle
- Real failure injection (not mocks)
- Works in CI environment

**Implementation**:

```go
func TestChaos_DatabaseConnectionLoss(t *testing.T) {
    tc := testhelpers.SetupTestContainer(t)
    defer tc.Cleanup(t)

    // Create test data
    log := createTestLog(t, "ACC-001")
    err := tc.Repo.Create(context.Background(), log)
    require.NoError(t, err)

    // Inject chaos: pause container
    ctx := context.Background()
    err = tc.Container.Pause(ctx)
    require.NoError(t, err)

    // Verify operation fails gracefully
    _, err = tc.Repo.FindByID(context.Background(), log.LogID)
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "connection")

    // Resume container
    err = tc.Container.Unpause(ctx)
    require.NoError(t, err)

    // Verify recovery
    time.Sleep(100 * time.Millisecond)
    retrieved, err := tc.Repo.FindByID(context.Background(), log.LogID)
    assert.NoError(t, err)
    assert.Equal(t, log.LogID, retrieved.LogID)
}
```

### Option 2: Chaos Mesh Integration

**Pros**:

- Industry-standard chaos engineering tool
- Rich failure injection capabilities
- Kubernetes-native

**Cons**:

- Requires Kubernetes environment
- More complex setup
- Overkill for unit/integration tests

**Use Case**: Staging/production chaos testing (future)

### Option 3: Toxiproxy

**Pros**:

- Network-level failure injection
- Latency, bandwidth, connection issues
- Works with any protocol

**Cons**:

- Additional infrastructure dependency
- Complexity for simple scenarios

**Use Case**: Network partition testing (Phase 3)

## Test Organisation

### Directory Structure

```text
internal/position-keeping/
├── repository/
│   ├── postgres_repository_test.go          # Integration tests
│   ├── postgres_repository_chaos_test.go    # Chaos tests (future)
│   └── testhelpers/
│       ├── testcontainer.go                 # Base setup
│       └── chaos.go                         # Chaos injection helpers (future)
├── service/
│   └── service_chaos_test.go                # gRPC chaos tests (future)
└── app/
    └── resilience_test.go                   # Full-stack chaos (future)
```

### Test Naming Convention

```text
TestChaos_<Component>_<FailureType>
TestChaos_Database_ConnectionLoss
TestChaos_Kafka_BrokerUnavailable
TestChaos_Service_HighConcurrency
```

## Metrics & Observability

### Key Metrics to Track

1. **Recovery Time**: Time from failure injection to full recovery
2. **Error Rate**: Percentage of operations failing during chaos
3. **Data Integrity**: Zero data corruption/loss events
4. **Resource Leaks**: No goroutine/connection leaks
5. **Availability**: System remains partially available during failures

### Success Criteria

- ✅ No panics or crashes
- ✅ Graceful degradation
- ✅ Clear error messages
- ✅ Automatic recovery
- ✅ No data loss/corruption
- ✅ Performance within acceptable bounds

## Integration with CI/CD

### Test Execution

```yaml

# GitHub Actions workflow (future)

- name: Run Chaos Tests

  run: go test -v -tags=chaos ./...
  timeout-minutes: 15

  # Only on develop/main, not all PRs (too resource-intensive)

  if: github.ref == 'refs/heads/develop' || github.ref == 'refs/heads/main'
```

### Build Tags

```go
//go:build chaos
// +build chaos

package repository

// Chaos tests are only run when explicitly requested
// due to longer execution time and resource requirements
```

## Next Steps

1. **Implement Phase 1**: Basic database chaos tests
   - Connection loss during read
   - Connection loss during write
   - Slow query handling

1. **Add Helper Functions**: Chaos injection utilities
   - `InjectDatabaseFailure(tc *TestContainer, duration time.Duration)`
   - `InjectKafkaFailure(publisher EventPublisher, errorType string)`
   - `InjectNetworkLatency(tc *TestContainer, latencyMs int)`

1. **Document Runbooks**: Operational guidance
   - Expected behaviour during failures
   - Recovery procedures
   - Monitoring and alerting setup

1. **Continuous Improvement**: Regular chaos reviews
   - Quarterly chaos game days
   - Post-incident chaos test additions
   - Failure mode documentation

## References

- [Chaos Engineering Principles](https://principlesofchaos.org/)
- [Testcontainers Documentation](https://golang.testcontainers.org/)
- [Google SRE Book - Testing for Reliability](https://sre.google/sre-book/testing-reliability/)
- [AWS Chaos Engineering](https://aws.amazon.com/blogs/architecture/chaos-engineering-on-aws/)

## Conclusion

Chaos testing will significantly improve system resilience by validating failure handling before production incidents
occur. The testcontainers-based approach provides a pragmatic starting point with real failure injection, and the
framework can evolve to include more sophisticated scenarios as needed.

**Implementation Priority**: Medium
**Value**: High (prevents production incidents)
**Complexity**: Medium (leverages existing testcontainers)

The testing strategy is designed to integrate seamlessly with existing test infrastructure while providing
comprehensive resilience validation.
