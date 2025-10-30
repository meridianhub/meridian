#!/bin/bash
# Kafka Event Watcher - Monitor Kafka topics in real-time

set -e

KAFKA_POD=$(kubectl get pods -l app=kafka -o jsonpath='{.items[0].metadata.name}')
if [ -z "$KAFKA_POD" ]; then
  echo "Error: Kafka pod not found. Ensure Kafka is running and accessible."
  exit 1
fi

echo "Watching Kafka topics (Ctrl+C to stop)..."
echo ""

# Watch both topics in parallel
{
  echo "=== current-account.deposits ==="
  kubectl exec -it $KAFKA_POD -- kafka-console-consumer.sh \
    --bootstrap-server localhost:9092 \
    --topic current-account.deposits \
    --from-beginning
} &

{
  echo "=== financial-accounting.postings ==="
  kubectl exec -it $KAFKA_POD -- kafka-console-consumer.sh \
    --bootstrap-server localhost:9092 \
    --topic financial-accounting.postings \
    --from-beginning
} &

wait
