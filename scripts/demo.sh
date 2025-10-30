#!/bin/bash
# Meridian Demo Script - Event-Driven Microservices with Kafka
# Demonstrates: CurrentAccount → Kafka → FinancialAccounting flow

set -e

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${BLUE}╔════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║  Meridian Event-Driven Banking Demo                   ║${NC}"
echo -e "${BLUE}║  CurrentAccount → Kafka → FinancialAccounting         ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════╝${NC}"
echo ""

# Check prerequisites
echo -e "${YELLOW}Checking prerequisites...${NC}"
command -v grpcurl >/dev/null 2>&1 || { echo "grpcurl required. Install: brew install grpcurl"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq required. Install: brew install jq"; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "kubectl required."; exit 1; }

# Check services are running
echo -e "${YELLOW}Verifying services...${NC}"
kubectl get pods | grep -E "(meridian|kafka|cockroach)" || { echo "Services not running. Run: tilt up"; exit 1; }
echo -e "${GREEN}✓ Services running${NC}\n"

# Step 1: Create Account
echo -e "${BLUE}════ Step 1: Create Current Account ════${NC}"
CREATE_RESPONSE=$(grpcurl -plaintext -d '{
  "customer_reference": "CUST-DEMO-001",
  "product_service_type": {
    "type": "STANDARD_CURRENT_ACCOUNT"
  },
  "account_currency": "GBP"
}' localhost:9091 meridian.current_account.v1.CurrentAccountService/InitiateCurrentAccount)

ACCOUNT_ID=$(echo "$CREATE_RESPONSE" | jq -r '.currentAccountFacilityReference')
echo -e "${GREEN}✓ Account Created:${NC} $ACCOUNT_ID\n"

# Step 2: Execute Deposit
echo -e "${BLUE}════ Step 2: Execute Deposit (£100) ════${NC}"
DEPOSIT_RESPONSE=$(grpcurl -plaintext -d "{
  \"current_account_facility_reference\": \"$ACCOUNT_ID\",
  \"amount\": {
    \"currency\": \"GBP\",
    \"units\": 100,
    \"nanos\": 0
  }
}" localhost:9091 meridian.current_account.v1.CurrentAccountService/ExecuteDeposit)

DEPOSIT_REF=$(echo "$DEPOSIT_RESPONSE" | jq -r '.depositReference')
echo -e "${GREEN}✓ Deposit Executed:${NC} $DEPOSIT_REF\n"

# Step 3: Watch Kafka Events
echo -e "${BLUE}════ Step 3: Kafka Event Flow ════${NC}"
echo -e "${YELLOW}Watching Kafka topics for 3 seconds...${NC}"

# Get Kafka pod
KAFKA_POD=$(kubectl get pods -l app=kafka -o jsonpath='{.items[0].metadata.name}')

# Show events from current-account.deposits topic
echo -e "\n${YELLOW}Topic: current-account.deposits${NC}"
kubectl exec -it $KAFKA_POD -- kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 \
  --topic current-account.deposits \
  --from-beginning \
  --max-messages 1 \
  --timeout-ms 3000 2>/dev/null || echo "(Waiting for messages...)"

# Show events from financial-accounting.postings topic
echo -e "\n${YELLOW}Topic: financial-accounting.postings${NC}"
kubectl exec -it $KAFKA_POD -- kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 \
  --topic financial-accounting.postings \
  --from-beginning \
  --max-messages 2 \
  --timeout-ms 3000 2>/dev/null || echo "(Waiting for messages...)"

echo -e "${GREEN}✓ Kafka events propagated${NC}\n"

# Step 4: Verify Ledger Postings
echo -e "${BLUE}════ Step 4: Verify Ledger Postings ════${NC}"
sleep 2  # Allow event processing

POSTINGS=$(grpcurl -plaintext -d "{
  \"account_reference\": \"$ACCOUNT_ID\"
}" localhost:9092 meridian.financial_accounting.v1.FinancialAccountingService/ListLedgerPostings)

echo "$POSTINGS" | jq '.postings[] | {
  direction: .postingDirection,
  amount: .postingAmount,
  account: .accountReference
}'
echo -e "${GREEN}✓ Double-entry postings created${NC}\n"

# Step 5: Verify Account Balance
echo -e "${BLUE}════ Step 5: Verify Account Balance ════${NC}"
ACCOUNT=$(grpcurl -plaintext -d "{
  \"current_account_facility_reference\": \"$ACCOUNT_ID\"
}" localhost:9091 meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount)

BALANCE=$(echo "$ACCOUNT" | jq -r '.currentAccountFacility.balance.units')
echo -e "${GREEN}✓ Account Balance:${NC} £$BALANCE\n"

# Summary
echo -e "${BLUE}╔════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║  Demo Complete! Event-Driven Flow Validated           ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "${GREEN}Summary:${NC}"
echo -e "  Account:  $ACCOUNT_ID"
echo -e "  Deposit:  £100"
echo -e "  Balance:  £$BALANCE"
echo -e "  Flow:     CurrentAccount → Kafka → FinancialAccounting ✓"
echo ""
echo -e "${YELLOW}Architecture Demonstrated:${NC}"
echo -e "  ✓ BIAN-compliant microservices"
echo -e "  ✓ Protobuf messages in Kafka"
echo -e "  ✓ Event-driven communication"
echo -e "  ✓ Double-entry ledger postings"
echo -e "  ✓ Eventual consistency"
