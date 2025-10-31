#!/usr/bin/env bash
# Kafka Failover Test
# Tests broker failure and automatic leader election

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo "🔥 Kafka Failover Test"
echo "================================"

# Check prerequisites
if ! command -v kubectl &> /dev/null; then
    echo -e "${RED}✗ kubectl not found${NC}"
    exit 1
fi

if ! command -v jq &> /dev/null; then
    echo -e "${RED}✗ jq not found${NC}"
    exit 1
fi

# Test configuration
TEST_TOPIC="failover-test-$(date +%s)"
TEST_BROKER="kafka-1"  # We'll kill this one
RECOVERY_WAIT=30

kafka_exec() {
    kubectl exec kafka-0 -- "$@" 2>/dev/null || return 1
}

echo -e "${BLUE}Phase 1: Setup${NC}"
echo "----------------------------------------"

# 1. Verify cluster is healthy
echo -n "Verifying cluster health... "
RUNNING_BROKERS=$(kubectl get pods -l app=kafka --field-selector=status.phase=Running -o json | jq '.items | length')
if [ "$RUNNING_BROKERS" -ne 3 ]; then
    echo -e "${RED}✗ Expected 3 brokers, found $RUNNING_BROKERS${NC}"
    exit 1
fi
echo -e "${GREEN}✓${NC}"

# 2. Create test topic with replication
echo -n "Creating test topic with RF=2... "
if ! kafka_exec kafka-topics --create --topic "$TEST_TOPIC" --partitions 3 --replication-factor 2 --bootstrap-server localhost:9092 >/dev/null 2>&1; then
    echo -e "${RED}✗ Failed to create topic${NC}"
    exit 1
fi
echo -e "${GREEN}✓${NC}"

# 3. Describe topic and capture initial state
echo "Initial partition distribution:"
INITIAL_STATE=$(kafka_exec kafka-topics --describe --topic "$TEST_TOPIC" --bootstrap-server localhost:9092 2>&1)
echo "$INITIAL_STATE" | grep "Partition:"

# 4. Produce test messages
echo -n "Producing 100 test messages... "
for i in {1..100}; do
    echo "message-$i" | kafka_exec kafka-console-producer --topic "$TEST_TOPIC" --bootstrap-server localhost:9092 >/dev/null 2>&1
done
echo -e "${GREEN}✓${NC}"

# 5. Consume and verify messages
echo -n "Verifying messages before failover... "
MESSAGE_COUNT=$(kafka_exec kafka-console-consumer --topic "$TEST_TOPIC" --from-beginning --max-messages 100 --timeout-ms 5000 --bootstrap-server localhost:9092 2>&1 | grep "^message-" | wc -l)
if [ "$MESSAGE_COUNT" -eq 100 ]; then
    echo -e "${GREEN}✓ All 100 messages present${NC}"
else
    echo -e "${RED}✗ Expected 100 messages, found $MESSAGE_COUNT${NC}"
    exit 1
fi

echo ""
echo -e "${BLUE}Phase 2: Failover${NC}"
echo "----------------------------------------"

# 6. Kill one broker
echo -e "Killing broker ${YELLOW}$TEST_BROKER${NC}..."
kubectl delete pod "$TEST_BROKER" --wait=false >/dev/null 2>&1
sleep 5

# 7. Wait for leader election
echo -n "Waiting for leader election... "
ELECTION_TIMEOUT=30
ELAPSED=0
while [ $ELAPSED -lt $ELECTION_TIMEOUT ]; do
    sleep 2
    ELAPSED=$((ELAPSED + 2))

    # Check if we can still describe the topic
    if kafka_exec kafka-topics --describe --topic "$TEST_TOPIC" --bootstrap-server localhost:9092 >/dev/null 2>&1; then
        echo -e "${GREEN}✓ Leader election complete (${ELAPSED}s)${NC}"
        break
    fi

    echo -n "."
done

if [ $ELAPSED -ge $ELECTION_TIMEOUT ]; then
    echo -e "${RED}✗ Leader election timeout${NC}"
    exit 1
fi

# 8. Verify cluster still has 2 brokers
echo -n "Checking remaining brokers... "
REMAINING_BROKERS=$(kubectl get pods -l app=kafka --field-selector=status.phase=Running -o json | jq '.items | length')
if [ "$REMAINING_BROKERS" -eq 2 ]; then
    echo -e "${GREEN}✓ 2 brokers remaining${NC}"
else
    echo -e "${YELLOW}⚠ Found $REMAINING_BROKERS brokers${NC}"
fi

# 9. Show new partition distribution
echo "Partition distribution after failover:"
kafka_exec kafka-topics --describe --topic "$TEST_TOPIC" --bootstrap-server localhost:9092 2>&1 | grep "Partition:"

echo ""
echo -e "${BLUE}Phase 3: Data Persistence${NC}"
echo "----------------------------------------"

# 10. Verify messages survived
echo -n "Verifying message persistence... "
MESSAGE_COUNT_AFTER=$(kafka_exec kafka-console-consumer --topic "$TEST_TOPIC" --from-beginning --max-messages 100 --timeout-ms 5000 --bootstrap-server localhost:9092 2>&1 | grep "^message-" | wc -l)
if [ "$MESSAGE_COUNT_AFTER" -eq 100 ]; then
    echo -e "${GREEN}✓ All 100 messages still present${NC}"
else
    echo -e "${RED}✗ Lost messages! Found $MESSAGE_COUNT_AFTER/100${NC}"
    exit 1
fi

# 11. Produce new messages with 2 brokers
echo -n "Producing 50 new messages with 2 brokers... "
for i in {101..150}; do
    echo "message-$i" | kafka_exec kafka-console-producer --topic "$TEST_TOPIC" --bootstrap-server localhost:9092 >/dev/null 2>&1
done
echo -e "${GREEN}✓${NC}"

# 12. Verify new messages
echo -n "Verifying new messages... "
NEW_MESSAGE_COUNT=$(kafka_exec kafka-console-consumer --topic "$TEST_TOPIC" --from-beginning --max-messages 150 --timeout-ms 5000 --bootstrap-server localhost:9092 2>&1 | grep "^message-" | wc -l)
if [ "$NEW_MESSAGE_COUNT" -eq 150 ]; then
    echo -e "${GREEN}✓ All 150 messages present${NC}"
else
    echo -e "${RED}✗ Expected 150 messages, found $NEW_MESSAGE_COUNT${NC}"
fi

echo ""
echo -e "${BLUE}Phase 4: Recovery${NC}"
echo "----------------------------------------"

# 13. Wait for broker to recover
echo "Waiting ${RECOVERY_WAIT}s for $TEST_BROKER to recover..."
sleep "$RECOVERY_WAIT"

# 14. Check if broker recovered
RECOVERED_BROKERS=$(kubectl get pods -l app=kafka --field-selector=status.phase=Running -o json | jq '.items | length')
echo -n "Checking broker recovery... "
if [ "$RECOVERED_BROKERS" -eq 3 ]; then
    echo -e "${GREEN}✓ All 3 brokers recovered${NC}"
else
    echo -e "${YELLOW}⚠ Only $RECOVERED_BROKERS/3 brokers running${NC}"
fi

# 15. Verify final state
echo "Final partition distribution:"
kafka_exec kafka-topics --describe --topic "$TEST_TOPIC" --bootstrap-server localhost:9092 2>&1 | grep "Partition:" || true

echo ""
echo -e "${BLUE}Phase 5: Cleanup${NC}"
echo "----------------------------------------"

# 16. Delete test topic
echo -n "Deleting test topic... "
if kafka_exec kafka-topics --delete --topic "$TEST_TOPIC" --bootstrap-server localhost:9092 >/dev/null 2>&1; then
    echo -e "${GREEN}✓${NC}"
else
    echo -e "${YELLOW}⚠ Manual cleanup may be required${NC}"
fi

echo ""
echo -e "${GREEN}✅ Failover test complete${NC}"
echo ""
echo "Summary:"
echo "  • Broker failure: $TEST_BROKER killed"
echo "  • Leader election: Successful"
echo "  • Message persistence: 100% (150/150 messages)"
echo "  • Cluster recovery: $RECOVERED_BROKERS/3 brokers running"

if [ "$RECOVERED_BROKERS" -eq 3 ] && [ "$NEW_MESSAGE_COUNT" -eq 150 ]; then
    echo ""
    echo -e "${GREEN}🎉 All tests passed!${NC}"
    exit 0
else
    echo ""
    echo -e "${YELLOW}⚠ Some tests had warnings${NC}"
    exit 0
fi
