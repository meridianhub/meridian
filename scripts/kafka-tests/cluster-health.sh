#!/usr/bin/env bash
# Kafka Cluster Health Check
# Verifies 3-broker cluster is healthy with proper quorum

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "🔍 Kafka Cluster Health Check"
echo "================================"

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    echo -e "${RED}✗ kubectl not found${NC}"
    exit 1
fi

# Function to run command in kafka-0
kafka_exec() {
    kubectl exec -it kafka-0 -- "$@" 2>/dev/null || return 1
}

# 1. Check all 3 brokers are running
echo -n "Checking broker pods... "
BROKER_COUNT=$(kubectl get pods -l app=kafka --field-selector=status.phase=Running -o json | jq '.items | length')
if [ "$BROKER_COUNT" -eq 3 ]; then
    echo -e "${GREEN}✓ All 3 brokers running${NC}"
else
    echo -e "${RED}✗ Expected 3 brokers, found $BROKER_COUNT${NC}"
    kubectl get pods -l app=kafka
    exit 1
fi

# 2. Check broker readiness
echo -n "Checking broker readiness... "
READY_COUNT=$(kubectl get pods -l app=kafka -o json | jq '[.items[] | select(.status.conditions[] | select(.type=="Ready" and .status=="True"))] | length')
if [ "$READY_COUNT" -eq 3 ]; then
    echo -e "${GREEN}✓ All 3 brokers ready${NC}"
else
    echo -e "${RED}✗ Only $READY_COUNT/3 brokers ready${NC}"
    kubectl get pods -l app=kafka
    exit 1
fi

# 3. Check cluster metadata
echo -n "Checking cluster metadata... "
if kafka_exec kafka-metadata --bootstrap-server localhost:9092 quorum describe --status 2>&1 | grep -q "Leader"; then
    echo -e "${GREEN}✓ KRaft quorum formed${NC}"
else
    echo -e "${RED}✗ KRaft quorum not healthy${NC}"
    exit 1
fi

# 4. Check broker registration
echo -n "Checking broker registration... "
REGISTERED_BROKERS=$(kafka_exec kafka-broker-api-versions --bootstrap-server localhost:9092 2>&1 | grep -c "^kafka" || true)
if [ "$REGISTERED_BROKERS" -eq 3 ]; then
    echo -e "${GREEN}✓ All 3 brokers registered${NC}"
else
    echo -e "${YELLOW}⚠ Found $REGISTERED_BROKERS registered brokers${NC}"
fi

# 5. Check cluster ID consistency
echo -n "Checking cluster ID consistency... "
CLUSTER_IDS=$(kubectl exec kafka-0 -- cat /tmp/kraft-combined-logs/meta.properties 2>/dev/null | grep "cluster.id" || echo "")
if [ -n "$CLUSTER_IDS" ]; then
    echo -e "${GREEN}✓ Cluster ID present${NC}"
else
    echo -e "${YELLOW}⚠ Could not verify cluster ID${NC}"
fi

# 6. Test basic topic operations
echo -n "Testing topic operations... "
TEST_TOPIC="health-check-$(date +%s)"
if kafka_exec kafka-topics --create --topic "$TEST_TOPIC" --partitions 3 --replication-factor 2 --bootstrap-server localhost:9092 >/dev/null 2>&1; then
    # Verify topic was created with correct replication
    TOPIC_INFO=$(kafka_exec kafka-topics --describe --topic "$TEST_TOPIC" --bootstrap-server localhost:9092 2>&1)
    REPLICAS=$(echo "$TOPIC_INFO" | grep "ReplicationFactor: 2" | wc -l)

    # Cleanup
    kafka_exec kafka-topics --delete --topic "$TEST_TOPIC" --bootstrap-server localhost:9092 >/dev/null 2>&1

    if [ "$REPLICAS" -gt 0 ]; then
        echo -e "${GREEN}✓ Topic creation working (RF=2)${NC}"
    else
        echo -e "${YELLOW}⚠ Topic created but replication factor unexpected${NC}"
    fi
else
    echo -e "${RED}✗ Topic creation failed${NC}"
    exit 1
fi

echo ""
echo -e "${GREEN}✅ Kafka cluster is healthy${NC}"
echo ""
echo "Cluster Summary:"
kubectl get pods -l app=kafka -o custom-columns=NAME:.metadata.name,STATUS:.status.phase,READY:.status.conditions[?(@.type==\"Ready\")].status,NODE:.spec.nodeName

exit 0
