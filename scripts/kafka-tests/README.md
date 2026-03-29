# Kafka Multi-Broker Testing Scripts

Automated testing scripts for the 3-broker Kafka cluster with KRaft quorum.

## Scripts

### `cluster-health.sh`

Fast health check that verifies the Kafka cluster is properly configured and operational.

**What it checks:**

- ✅ All 3 broker pods are running
- ✅ All 3 brokers are ready
- ✅ KRaft quorum is formed
- ✅ Brokers are registered with the cluster
- ✅ Cluster ID consistency
- ✅ Basic topic operations (create/delete with RF=2)

**Usage:**

```bash

# Run manually

./scripts/kafka-tests/cluster-health.sh

# Or via Tilt

tilt trigger kafka-health
```

**Expected runtime:** ~10-15 seconds

---

### `failover-test.sh`

Comprehensive failover test that simulates broker failure and verifies cluster resilience.

**Test phases:**

1. **Setup**: Create test topic with RF=2, produce 100 messages
2. **Failover**: Kill kafka-1 broker, wait for leader election
3. **Data Persistence**: Verify all messages survived, produce 50 new messages
4. **Recovery**: Wait for broker to recover, verify final state
5. **Cleanup**: Delete test topic

**What it validates:**

- ✅ Message persistence during broker failure
- ✅ Automatic leader election
- ✅ Cluster continues operating with 2/3 brokers
- ✅ New messages can be produced/consumed during failure
- ✅ Broker recovery and partition rebalancing

**Usage:**

```bash

# Run manually

./scripts/kafka-tests/failover-test.sh

# Or via Tilt

tilt trigger kafka-failover
```

**Expected runtime:** ~60-90 seconds

---

## Tilt Integration

Both scripts are integrated into the Tiltfile as local resources:

### `kafka-health`

- **Auto-runs**: Yes (on Tilt startup, after kafka-cluster is ready)
- **Trigger mode**: Manual (can re-run with `tilt trigger kafka-health`)
- **Label**: `messaging`
- **Use case**: Quick validation that cluster is healthy

### `kafka-failover`

- **Auto-runs**: No (manual trigger only)
- **Trigger mode**: Manual (`tilt trigger kafka-failover`)
- **Label**: `messaging`
- **Use case**: Testing failover scenarios before deployments

---

## CI/CD Integration

The GitHub Actions workflow `.github/workflows/kafka-integration-tests.yml` runs both tests automatically on:

- Pull requests modifying Tiltfile or Kafka scripts
- Pushes to develop/main branches
- Manual workflow dispatch

**CI Test Flow:**

1. Create Kind cluster
2. Deploy 3-broker Kafka cluster
3. Wait for all brokers to be ready
4. Run `cluster-health.sh`
5. Run `failover-test.sh`
6. Collect logs on failure

---

## Prerequisites

**Required tools:**

- `kubectl` - Kubernetes CLI
- `jq` - JSON processor (for parsing pod status)

**Cluster requirements:**

- Kubernetes cluster with Kafka StatefulSet deployed
- 3 Kafka broker pods labeled `app=kafka`
- Pods named: `kafka-0`, `kafka-1`, `kafka-2`

---

## Troubleshooting

### Health check fails: "Expected 3 brokers, found X"

- Check pod status: `kubectl get pods -l app=kafka`
- View pod logs: `kubectl logs kafka-0`
- Verify resources: `kubectl describe pod kafka-0`

### Failover test timeout during leader election

- Increase `ELECTION_TIMEOUT` in the script (default: 30s)
- Check if 2 brokers are still running: `kubectl get pods -l app=kafka`
- Verify partition replication: `kubectl exec kafka-0 -- kafka-topics --describe --bootstrap-server localhost:9092`

### Messages lost during failover

- This indicates RF=1 (single replica) or under-replicated partitions
- Check topic configuration: `kubectl exec kafka-0 -- kafka-topics --describe --topic <topic> --bootstrap-server
localhost:9092`
- Verify replication factor settings in Tiltfile

---

## Development

### Adding new tests

Create new scripts in `scripts/kafka-tests/` and add them to Tiltfile:

```python
local_resource(
  'my-kafka-test',
  cmd='./scripts/kafka-tests/my-test.sh',
  resource_deps=['kafka-cluster'],
  labels=['messaging'],
  auto_init=False,  # Manual trigger
)
```

### Test script conventions

- Use `set -euo pipefail` for safety
- Exit 0 on success, non-zero on failure
- Use colored output (GREEN/RED/YELLOW) for clarity
- Clean up resources (test topics) before exiting
- Include timeout handling for async operations
- Log actions with timestamps for debugging

---

## Related Documentation

- [Tiltfile](../../Tiltfile) - Kafka cluster configuration
- [ADR-0006](../../docs/adr/0006-tilt-local-development.md) - Tilt development environment
- [Demo Guide](../../docs/archive/DEMO_GUIDE.md) - Kafka failover testing

---

## Questions?

See [CONTRIBUTING.md](../../CONTRIBUTING.md) or ask in the project discussions.
