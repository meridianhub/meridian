# -*- mode: Python -*-

# Tiltfile for Meridian local development
# Fast Kubernetes development with live reload

# Load Tilt extensions
load('ext://restart_process', 'docker_build_with_restart')

# Allow Tilt to connect to local Kubernetes cluster
allow_k8s_contexts(['kind-meridian-local', 'kind-kind', 'minikube', 'docker-desktop', 'colima', 'rancher-desktop'])

# =============================================================================
# Configuration
# =============================================================================

# Detect and use local registry if available (created by ctlptl)
# This speeds up image builds by avoiding remote registry pushes
if k8s_context() == 'kind-meridian-local':
    # Allow registry name to be configured via environment variable
    # Default: ctlptl-registry (matches ctlptl's default behavior)
    registry_name = os.getenv('TILT_REGISTRY_NAME', 'ctlptl-registry')

    # Registry URL that Tilt expects (host:port format)
    # ctlptl configures the registry to be accessible at localhost:5000
    registry_url = os.getenv('TILT_REGISTRY_URL', 'localhost:5000')

    # Validate that registry container exists
    registry_check = str(local('docker ps --filter name=%s --format "{{.Names}}" 2>/dev/null || true' % registry_name, quiet=True)).strip()

    if registry_check == registry_name:
        default_registry(registry_url)
        print('✓ Using local registry: %s (%s)' % (registry_name, registry_url))
    else:
        print('⚠️  Warning: Local registry "%s" not found' % registry_name)
        print('   Images will be loaded via "kind load" (slower)')
        print('   To create cluster with registry:')
        print('   ctlptl create cluster kind --registry=%s --name=kind-meridian-local' % registry_name)

# Docker image configuration
docker_registry = os.getenv('DOCKER_REGISTRY', 'ghcr.io/meridianhub')
image_name = 'meridian'
full_image = '{}/{}'.format(docker_registry, image_name)

# Kubernetes namespace
k8s_namespace = 'default'

# =============================================================================
# Backing Services
# =============================================================================
# NOTE: These configurations are optimized for LOCAL DEVELOPMENT ONLY.
# Production deployments require:
# - TLS/SSL encryption
# - Authentication and authorization
# - Persistent volumes and StatefulSets
# - Multi-node clusters with replication
# - Resource limits appropriate for production workloads

# CockroachDB - Single-node for local development
k8s_yaml(blob('''
apiVersion: v1
kind: Service
metadata:
  name: cockroachdb
  labels:
    app: cockroachdb
spec:
  type: ClusterIP
  ports:
  - name: grpc
    port: 26257
    targetPort: 26257
  - name: http
    port: 8080
    targetPort: 8080
  selector:
    app: cockroachdb
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: cockroachdb-pvc
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: cockroachdb
spec:
  serviceName: cockroachdb
  replicas: 1
  selector:
    matchLabels:
      app: cockroachdb
  template:
    metadata:
      labels:
        app: cockroachdb
    spec:
      containers:
      - name: cockroachdb
        image: cockroachdb/cockroach:v23.1.11
        ports:
        - containerPort: 26257
          name: grpc
        - containerPort: 8080
          name: http
        command:
        - /cockroach/cockroach
        - start-single-node
        - --insecure
        - --store=path=/cockroach/cockroach-data
        - --advertise-addr=cockroachdb.default.svc.cluster.local
        volumeMounts:
        - name: datadir
          mountPath: /cockroach/cockroach-data
        resources:
          requests:
            cpu: 500m
            memory: 1Gi
          limits:
            cpu: 2000m
            memory: 4Gi
      volumes:
      - name: datadir
        persistentVolumeClaim:
          claimName: cockroachdb-pvc
'''))

# Redis - Default configuration
k8s_yaml(blob('''
apiVersion: v1
kind: Service
metadata:
  name: redis
  labels:
    app: redis
spec:
  type: ClusterIP
  ports:
  - name: redis
    port: 6379
    targetPort: 6379
  selector:
    app: redis
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
spec:
  replicas: 1
  selector:
    matchLabels:
      app: redis
  template:
    metadata:
      labels:
        app: redis
    spec:
      containers:
      - name: redis
        image: redis:7-alpine
        ports:
        - containerPort: 6379
          name: redis
        command:
        - redis-server
        - --appendonly
        - "yes"
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
'''))

# Kafka - 3-broker cluster with KRaft mode for local development
# KRaft (Kafka Raft) replaces Zookeeper for metadata management
# Multi-broker setup enables testing of:
# - Partition replication and failover
# - Leader election
# - Quorum consensus
# - Production-like scenarios in local dev
#
# Architecture:
# - 3 brokers (kafka-0, kafka-1, kafka-2) each acting as both broker and controller
# - KRaft quorum across all 3 nodes for metadata consensus
# - Replication factor 2 for topics (allows 1 broker failure)
# - Headless service for StatefulSet pod discovery
# - Client service exposing kafka-0 as default endpoint
#
# Testing Failover:
# 1. Create topic with RF=2: kubectl exec kafka-0 -- kafka-topics --create --topic test --partitions 3 --replication-factor 2 --bootstrap-server localhost:9092
# 2. Describe topic: kubectl exec kafka-0 -- kafka-topics --describe --topic test --bootstrap-server localhost:9092
# 3. Kill a broker: kubectl delete pod kafka-1
# 4. Verify leadership transfer: kubectl exec kafka-0 -- kafka-topics --describe --topic test --bootstrap-server localhost:9092
# 5. Produce/consume messages to verify data persists
#
# Resource Usage: ~1.5GB total (512MB per broker)
#
# Resource-Constrained Development (8GB RAM machines):
# For machines with limited RAM, you can reduce to single-broker mode by:
# 1. Change replicas: 3 → 1
# 2. Change KAFKA_DEFAULT_REPLICATION_FACTOR: "2" → "1"
# 3. Change KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR: "2" → "1"
# 4. Reduce memory per broker: 384Mi → 256Mi
# This reduces Kafka memory from ~1.5GB to ~512MB
k8s_yaml(blob('''
apiVersion: v1
kind: Service
metadata:
  name: kafka
  labels:
    app: kafka
spec:
  type: ClusterIP
  ports:
  - name: broker
    port: 9092
    targetPort: 9092
  selector:
    app: kafka
    statefulset.kubernetes.io/pod-name: kafka-0
---
apiVersion: v1
kind: Service
metadata:
  name: kafka-headless
  labels:
    app: kafka
spec:
  clusterIP: None
  publishNotReadyAddresses: true
  ports:
  - name: broker
    port: 9092
    targetPort: 9092
  - name: controller
    port: 9093
    targetPort: 9093
  selector:
    app: kafka
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: kafka
spec:
  serviceName: kafka-headless
  replicas: 3
  podManagementPolicy: Parallel
  selector:
    matchLabels:
      app: kafka
  template:
    metadata:
      labels:
        app: kafka
    spec:
      containers:
      - name: kafka
        image: apache/kafka:3.9.1
        ports:
        - containerPort: 9092
          name: broker
        - containerPort: 9093
          name: controller
        env:
        # JVM heap settings for broker memory management
        - name: KAFKA_HEAP_OPTS
          value: "-Xms384M -Xmx384M"
        # Cluster ID - must be consistent across all brokers in the KRaft cluster
        # Generated once and shared by all nodes for cluster membership
        - name: CLUSTER_ID
          value: "MkU3OEVBNTcwNTJENDM2Qk"
        # Pod name used to derive node ID and configure per-pod settings
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        command:
        - sh
        - -c
        - |
          # Extract node ID from pod name (kafka-0 -> 1, kafka-1 -> 2, kafka-2 -> 3)
          NODE_ID=$((${POD_NAME##*-} + 1))

          # Define paths
          LOG_DIRS=/tmp/kraft-combined-logs
          CONFIG_FILE=/tmp/server-${POD_NAME}.properties

          echo "Generating server.properties for node ${NODE_ID} (${POD_NAME})..."

          # Generate per-pod server.properties with correct configuration
          cat > ${CONFIG_FILE} << EOF
          # Node Configuration
          node.id=${NODE_ID}
          process.roles=broker,controller

          # Listeners
          listeners=PLAINTEXT://:9092,CONTROLLER://:9093
          advertised.listeners=PLAINTEXT://${POD_NAME}.kafka-headless:9092
          controller.listener.names=CONTROLLER
          listener.security.protocol.map=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT

          # KRaft Quorum
          controller.quorum.voters=1@kafka-0.kafka-headless:9093,2@kafka-1.kafka-headless:9093,3@kafka-2.kafka-headless:9093

          # Storage
          log.dirs=${LOG_DIRS}

          # Replication
          offsets.topic.replication.factor=2
          transaction.state.log.replication.factor=2
          transaction.state.log.min.isr=1
          default.replication.factor=2
          min.insync.replicas=1

          # Auto-create topics
          auto.create.topics.enable=true

          # Performance
          num.network.threads=3
          num.io.threads=8
          socket.send.buffer.bytes=102400
          socket.receive.buffer.bytes=102400
          socket.request.max.bytes=104857600

          # Log Retention
          log.retention.hours=168
          log.segment.bytes=1073741824
          log.retention.check.interval.ms=300000
          EOF

          # Format KRaft storage if not already formatted
          if [ ! -f ${LOG_DIRS}/meta.properties ]; then
            echo "Formatting KRaft storage for node ${NODE_ID}..."
            /opt/kafka/bin/kafka-storage.sh format \
              -t ${CLUSTER_ID} \
              -c ${CONFIG_FILE}
          else
            echo "KRaft storage already formatted for node ${NODE_ID}"
          fi

          # Start Kafka with per-pod configuration
          echo "Starting Kafka with configuration from ${CONFIG_FILE}..."
          exec /opt/kafka/bin/kafka-server-start.sh ${CONFIG_FILE}
        readinessProbe:
          tcpSocket:
            port: 9092
          initialDelaySeconds: 15
          periodSeconds: 5
          timeoutSeconds: 3
          failureThreshold: 3
        livenessProbe:
          tcpSocket:
            port: 9092
          initialDelaySeconds: 30
          periodSeconds: 10
          timeoutSeconds: 3
          failureThreshold: 3
        resources:
          requests:
            cpu: 200m
            memory: 384Mi
          limits:
            cpu: 500m
            memory: 512Mi
'''))

# Keycloak - Identity and Access Management
# Provides OAuth 2.0 / OpenID Connect authentication for local development
# Pre-configured with:
# - Realm: meridian
# - Admin user: admin/admin
# - Client ID: meridian-service
# - JWKS endpoint: http://keycloak:8080/realms/meridian/protocol/openid-connect/certs
k8s_yaml(blob('''
apiVersion: v1
kind: Service
metadata:
  name: keycloak
  labels:
    app: keycloak
spec:
  type: ClusterIP
  ports:
  - name: http
    port: 8080
    targetPort: 8080
  selector:
    app: keycloak
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: keycloak
spec:
  replicas: 1
  selector:
    matchLabels:
      app: keycloak
  template:
    metadata:
      labels:
        app: keycloak
    spec:
      containers:
      - name: keycloak
        image: quay.io/keycloak/keycloak:26.0
        args:
        - start-dev
        - --http-port=8080
        ports:
        - containerPort: 8080
          name: http
        env:
        - name: KEYCLOAK_ADMIN
          value: admin
        - name: KEYCLOAK_ADMIN_PASSWORD
          value: admin
        - name: KC_HTTP_RELATIVE_PATH
          value: /
        - name: KC_HOSTNAME_STRICT
          value: "false"
        - name: KC_HOSTNAME_STRICT_HTTPS
          value: "false"
        - name: KC_HEALTH_ENABLED
          value: "true"
        readinessProbe:
          httpGet:
            path: /health/ready
            port: 8080
          initialDelaySeconds: 30
          periodSeconds: 10
          timeoutSeconds: 3
          failureThreshold: 3
        livenessProbe:
          httpGet:
            path: /health/live
            port: 8080
          initialDelaySeconds: 60
          periodSeconds: 30
          timeoutSeconds: 3
          failureThreshold: 3
        resources:
          requests:
            cpu: 200m
            memory: 512Mi
          limits:
            cpu: 1000m
            memory: 1Gi
'''))

# =============================================================================
# Observability Stack (Grafana, Loki, Tempo, Prometheus, Alloy)
# =============================================================================
# NOTE: This configuration provides a complete observability stack for local development.
# The stack includes:
# - Grafana Alloy: OpenTelemetry collector (receives OTLP traces/metrics)
# - Grafana Tempo: Distributed tracing backend
# - Grafana Loki: Log aggregation and storage
# - Prometheus: Metrics collection and storage
# - Grafana: Visualization and dashboards
#
# Grafana is accessible at http://localhost:3000 (no login required in dev mode)
# All services are configured with anonymous access for easy local development

k8s_yaml('deployments/k8s/observability/grafana-stack.yaml')

# Grafana resources
k8s_resource(
  'grafana',
  port_forwards='3000:3000',
  labels=['observability'],
  resource_deps=['tempo', 'loki', 'prometheus'],
)

# Tempo (traces)
k8s_resource(
  'tempo',
  labels=['observability'],
  resource_deps=[],
)

# Loki (logs)
k8s_resource(
  'loki',
  labels=['observability'],
  resource_deps=[],
)

# Prometheus (metrics)
k8s_resource(
  'prometheus',
  port_forwards='9090:9090',
  labels=['observability'],
  resource_deps=[],
)

# Alloy (collector)
k8s_resource(
  'alloy',
  labels=['observability'],
  resource_deps=['tempo', 'prometheus'],
)

# =============================================================================
# Main Application
# =============================================================================

# Build Docker image with live reload
# Use Dockerfile.dev for local development (has tar/rm for Tilt)
# Use Dockerfile for production builds (distroless)
docker_build(
  'meridian',
  context='.',
  dockerfile='Dockerfile.dev',
  build_args={
    'VERSION': 'dev',
    'COMMIT': local('git rev-parse --short HEAD'),
    'BUILD_DATE': local('date -u +"%Y-%m-%dT%H:%M:%SZ"'),
  },
  live_update=[
    # Sync Go source code
    sync('./cmd', '/app/cmd'),
    sync('./internal', '/app/internal'),
    sync('./pkg', '/app/pkg'),
    sync('./go.mod', '/app/go.mod'),
    sync('./go.sum', '/app/go.sum'),

    # Rebuild binary on changes (fast incremental builds)
    run(
      'cd /app && go build -o meridian ./cmd/meridian',
      trigger=['./cmd', './internal', './pkg'],
    ),

    # Restart the service using HUP signal
    run('kill -HUP 1', trigger=['./cmd', './internal', './pkg']),
  ],
)

# Deploy Kubernetes manifests
k8s_yaml(kustomize('deployments/k8s/base'))

# Set resource dependencies
k8s_resource(
  'meridian',
  port_forwards=[
    '8080:8080',  # HTTP API
    '9090:9090',  # gRPC API
  ],
  resource_deps=[
    'cockroachdb',
    'redis',
    'kafka-cluster',
    'keycloak',
  ],
  labels=['app'],
  # Group RBAC and config resources under the main app
  objects=[
    'meridian:serviceaccount',
    'meridian:role',
    'meridian:rolebinding',
    'meridian-config:configmap',
    # Note: meridian-version ConfigMap omitted (Kustomize hash suffix changes with content)
    # Tilt will still deploy it via kustomize, just not explicitly tracked here
  ],
)

# =============================================================================
# Resource Configuration
# =============================================================================

# CockroachDB resource
k8s_resource(
  'cockroachdb',
  port_forwards='26257:26257',  # SQL port
  labels=['database'],
  resource_deps=[],
  objects=['cockroachdb-pvc:persistentvolumeclaim'],
)

# Redis resource
k8s_resource(
  'redis',
  port_forwards='6379:6379',
  labels=['cache'],
  resource_deps=[],
)

# Kafka cluster resources (3-broker KRaft cluster - no Zookeeper dependency)
# Port forwarding to kafka-0 for client access
# Individual pods visible in Tilt UI for monitoring cluster health
k8s_resource(
  'kafka',
  new_name='kafka-cluster',
  port_forwards='9092:9092',
  labels=['messaging'],
  resource_deps=[],
  pod_readiness='wait',  # Wait for all 3 pods to be ready
)

# Label standalone kafka client service (defined via blob() earlier)
# Groups it with messaging resources to prevent appearing as "uncategorized"
k8s_resource(
  new_name='kafka-client-service',
  objects=['kafka:service'],
  labels=['messaging'],
  resource_deps=[],
)

# Keycloak resource
k8s_resource(
  'keycloak',
  port_forwards='18080:8080',  # Admin console on port 18080 to avoid conflict with app
  labels=['auth'],
  resource_deps=[],
)

# Note: meridian-version ConfigMap remains "uncategorized" due to kustomize hash suffix
# The hash changes on every build, so we can't reference it statically in objects=[]
# This is acceptable - it's a single small ConfigMap with build metadata
# Alternative: Move to dedicated meridian-config if this becomes an issue

# =============================================================================
# Development Helpers
# =============================================================================

# Run tests on file changes
local_resource(
  'test',
  cmd='make test',
  deps=['./cmd', './internal', './pkg', './go.mod'],
  labels=['quality'],
  allow_parallel=True,
)

# Run linters on file changes
local_resource(
  'lint',
  cmd='make lint',
  deps=['./cmd', './internal', './pkg', './go.mod'],
  labels=['quality'],
  allow_parallel=True,
  auto_init=False,  # Run manually with 'tilt trigger lint'
)

# Run database migrations on startup - uses Atlas to apply schema changes
# Each service has its own business schema and audit schema
# Migrations are applied in order:
# 1. shared (audit factory infrastructure)
# 2. current_account (customers, accounts, current_account_audit)
# 3. position_keeping (transactions, position_keeping_audit) - depends on current_account for FKs
local_resource(
  'migrate-shared',
  cmd='atlas migrate apply --env local --config file://atlas.shared.hcl --url "postgres://root@localhost:26257/defaultdb?sslmode=disable"',
  resource_deps=['cockroachdb'],
  labels=['database'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,
)

local_resource(
  'migrate-current-account',
  cmd='atlas migrate apply --env local --config file://atlas.current_account.hcl --url "postgres://root@localhost:26257/defaultdb?sslmode=disable"',
  resource_deps=['migrate-shared'],
  labels=['database'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,
)

local_resource(
  'migrate-position-keeping',
  cmd='atlas migrate apply --env local --config file://atlas.position_keeping.hcl --url "postgres://root@localhost:26257/defaultdb?sslmode=disable"',
  resource_deps=['migrate-current-account'],  # Depends on current_account being migrated first
  labels=['database'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,
)

# Kafka cluster health check - runs automatically after kafka-cluster is ready
local_resource(
  'kafka-health',
  cmd='./scripts/kafka-tests/cluster-health.sh',
  resource_deps=['kafka-cluster'],
  labels=['messaging'],
  auto_init=True,  # Runs automatically on Tilt startup
  trigger_mode=TRIGGER_MODE_MANUAL,  # Can be re-run manually via 'tilt trigger kafka-health'
)

# Kafka failover test - manual trigger for testing broker failure scenarios
local_resource(
  'kafka-failover',
  cmd='./scripts/kafka-tests/failover-test.sh',
  resource_deps=['kafka-cluster'],
  labels=['messaging'],
  auto_init=False,  # Run manually with 'tilt trigger kafka-failover'
  trigger_mode=TRIGGER_MODE_MANUAL,
)

# Keycloak setup - runs automatically after keycloak is ready
# Configures realm, client, and test user
local_resource(
  'keycloak-setup',
  cmd='./scripts/keycloak-setup.sh',
  resource_deps=['keycloak'],
  labels=['auth'],
  auto_init=True,  # Runs automatically on Tilt startup
  trigger_mode=TRIGGER_MODE_MANUAL,  # Can be re-run manually via 'tilt trigger keycloak-setup'
)

# =============================================================================
# UI Configuration
# =============================================================================

# Tilt UI settings
update_settings(max_parallel_updates=3, k8s_upsert_timeout_secs=60)

print("""
========================================
🚀 Meridian Development Environment
========================================

Services:
  • Meridian API     → http://localhost:8080
  • Meridian gRPC    → localhost:9090
  • CockroachDB      → localhost:26257
  • Redis            → localhost:6379
  • Kafka Cluster    → localhost:9092
    - 3 brokers with KRaft quorum (kafka-0, kafka-1, kafka-2)
    - Replication factor: 2 (tolerates 1 broker failure)
  • Keycloak         → http://localhost:18080
    - Admin console: admin/admin
    - Realm: meridian (create manually)
    - JWKS: http://localhost:18080/realms/meridian/protocol/openid-connect/certs

Observability Stack:
  • Grafana          → http://localhost:3000 (dashboards, traces, logs, metrics)
  • Prometheus       → http://localhost:9090 (metrics queries)
  • Tempo            → traces via Alloy OTLP endpoint (alloy:4317)
  • Loki             → logs aggregation
  • Alloy            → OpenTelemetry collector

Tilt UI              → http://localhost:10350

Hot reload: Edit Go code and see changes in ~3 seconds

Database Migrations:
  • Migrations run automatically on startup (2 services):
    1. current_account (customers, accounts, current_account_audit)
    2. position_keeping (transactions, position_keeping_audit)
  • Each service has its own audit schema for isolation
  • Manual triggers:
    - tilt trigger migrate-current-account
    - tilt trigger migrate-position-keeping
  • Check status:
    - make migrate-status-all (requires DATABASE_URL env var)

Testing Kafka Failover:
  kubectl delete pod kafka-1  # Kill broker
  kubectl exec kafka-0 -- kafka-topics --describe --topic <topic> --bootstrap-server localhost:9092

========================================
""")
