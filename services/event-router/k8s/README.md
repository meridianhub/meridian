# Utilization Metering Consumer - Kubernetes Deployment

This directory contains Kubernetes manifests for deploying the `utilization-metering-consumer` service.

## Overview

The utilization-metering-consumer is a centralized Kafka consumer that:

- Consumes audit events from all 6 Meridian services
- Transforms audit events into utilization measurements
- Records measurements to Position Keeping's tenant-zero for billing

## Manifests

- **deployment.yaml**: Deployment configuration with HorizontalPodAutoscaler
  - Single deployment consuming from all service audit topics
  - Autoscales based on Kafka consumer lag (1-5 replicas)
  - Resource limits: 500m CPU, 512Mi memory
  - Health probes: `/healthz` (liveness), `/ready` (readiness)

- **service.yaml**: ClusterIP Service
  - Exposes HTTP port 8080 for health checks and metrics
  - Prometheus scraping annotations enabled

- **configmap.yaml**: Configuration
  - Kafka bootstrap servers and consumer settings
  - Position Keeping gRPC endpoint
  - Tenant Zero ID for billing
  - Metrics collection interval

## Deployment

### Via Tilt (Local Development)

```bash
tilt up utilization-metering-consumer
```

The Tiltfile automatically:

- Builds the Docker image
- Deploys all manifests
- Port-forwards 8081:8080 for metrics access

### Via kubectl (Manual)

```bash
kubectl apply -f services/utilization-metering-consumer/k8s/
```

## Monitoring

### Health Checks

```bash
# Liveness probe
kubectl exec -it deployment/utilization-metering-consumer -- curl http://localhost:8080/healthz

# Readiness probe
kubectl exec -it deployment/utilization-metering-consumer -- curl http://localhost:8080/ready
```

### Prometheus Metrics

```bash
# Access metrics endpoint
kubectl port-forward svc/utilization-metering-consumer 8080:8080
curl http://localhost:8080/metrics
```

### Key Metrics

- `meridian_utilization_metering_events_consumed_total` - Events consumed by service/topic
- `meridian_utilization_metering_measurements_recorded_total` - Measurements recorded by service/asset
- `meridian_utilization_metering_transformation_errors_total` - Transformation errors by type
- `meridian_utilization_metering_position_keeping_api_errors_total` - API errors by type
- `meridian_utilization_metering_kafka_consumer_lag_messages` - Consumer lag by topic/partition
- `meridian_utilization_metering_event_processing_duration_seconds` - Processing latency histogram

### Alerting Rules (Recommended)

1. **High Consumer Lag**

   ```yaml
   alert: UtilizationMeteringHighLag
   expr: meridian_utilization_metering_kafka_consumer_lag_messages > 5000
   for: 5m
   ```

2. **Transformation Error Rate**

   ```yaml
   alert: UtilizationMeteringTransformationErrors
   expr: rate(meridian_utilization_metering_transformation_errors_total[5m]) > 0.1
   for: 2m
   ```

3. **Position Keeping API Errors**

   ```yaml
   alert: UtilizationMeteringAPIErrors
   expr: rate(meridian_utilization_metering_position_keeping_api_errors_total[5m]) > 0.05
   for: 2m
   ```

## Autoscaling

The HPA scales based on:

1. Kafka consumer lag (primary trigger)
2. CPU utilization (70% threshold)
3. Memory utilization (80% threshold)

**Scaling behavior:**

- Scale up: After 1 minute of sustained load (100% increase per minute)
- Scale down: After 5 minutes of reduced load (50% decrease per minute)

## Configuration

Edit `configmap.yaml` to adjust:

- Kafka bootstrap servers
- Consumer group ID
- Position Keeping endpoint
- Tenant Zero ID
- Metrics collection interval

**Note:** For production, move `TENANT_ZERO_ID` to a Secret instead of ConfigMap.

## Dependencies

- Kafka cluster (topics: `*.audit.events`)
- Position Keeping service (gRPC endpoint)
- Prometheus (for metrics scraping)

## Troubleshooting

### Consumer Not Starting

```bash
# Check logs
kubectl logs -f deployment/utilization-metering-consumer

# Common issues:
# 1. Kafka not reachable: Check KAFKA_BOOTSTRAP_SERVERS
# 2. Position Keeping unavailable: Check POSITION_KEEPING_ENDPOINT
# 3. Missing topics: Create audit event topics first
```

### High Consumer Lag

```bash
# Check HPA status
kubectl get hpa utilization-metering-consumer-hpa

# Check current lag
kubectl exec -it deployment/utilization-metering-consumer -- \
  curl -s http://localhost:8080/metrics | grep consumer_lag
```

### Transformation Errors

```bash
# View error breakdown
kubectl exec -it deployment/utilization-metering-consumer -- \
  curl -s http://localhost:8080/metrics | grep transformation_errors
```

## Architecture Notes

Unlike per-service audit-consumer deployments, this consumer:

- Runs as a **single deployment** (not one per service)
- Consumes from **multiple topics** (all 6 services)
- Writes to **tenant-zero** in Position Keeping (platform billing)
- Uses **HPA** for scaling based on aggregate load

This centralized design simplifies utilization tracking while maintaining isolation through tenant-zero position-keeping.
