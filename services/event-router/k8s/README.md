# Event Router - Kubernetes Deployment

This directory contains Kubernetes manifests for deploying the `event-router` service.

## Overview

The event-router is a CEL-filtered saga dispatcher that:

- Consumes domain events from Kafka topics
- Routes events to saga workflows via the control-plane
- Transforms audit events into utilization measurements for platform billing

## Manifests

- **deployment.yaml**: Deployment configuration with HorizontalPodAutoscaler
  - Single deployment consuming from multiple event topics
  - Autoscales based on Kafka consumer lag (1-5 replicas)
  - Resource limits: 500m CPU, 512Mi memory
  - Health probes: `/healthz` (liveness), `/ready` (readiness)

- **service.yaml**: ClusterIP Service
  - Exposes HTTP port 8080 for health checks and metrics
  - Prometheus scraping annotations enabled

- **configmap.yaml**: Configuration
  - Kafka bootstrap servers and consumer settings
  - Control Plane gRPC endpoint for saga triggering
  - Position Keeping gRPC endpoint for utilization metering
  - Tenant Zero ID for billing

## Deployment

### Via Tilt (Local Development)

```bash
tilt up event-router
```

### Via kubectl (Manual)

```bash
kubectl apply -f services/event-router/k8s/
```

## Monitoring

### Health Checks

```bash
# Liveness probe
kubectl exec -it deployment/event-router -- curl http://localhost:8080/healthz

# Readiness probe
kubectl exec -it deployment/event-router -- curl http://localhost:8080/ready
```

### Prometheus Metrics

```bash
kubectl port-forward svc/event-router 8080:8080
curl http://localhost:8080/metrics
```

## Dependencies

- Kafka cluster (event topics)
- Control Plane service (saga triggering via gRPC)
- Position Keeping service (utilization metering via gRPC)
- Prometheus (for metrics scraping)

## Troubleshooting

### Consumer Not Starting

```bash
kubectl logs -f deployment/event-router

# Common issues:
# 1. Kafka not reachable: Check KAFKA_BOOTSTRAP_SERVERS
# 2. Control Plane unavailable: Check CONTROL_PLANE_ENDPOINT
# 3. Missing topics: Create event topics first
```

### High Consumer Lag

```bash
kubectl get hpa event-router-hpa
kubectl exec -it deployment/event-router -- \
  curl -s http://localhost:8080/metrics | grep consumer_lag
```
