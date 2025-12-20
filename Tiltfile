# -*- mode: Python -*-

# Tiltfile for Meridian local development
# Fast Kubernetes development with live reload
#
# Offline Development:
# Once all Docker images are cached locally, this entire stack runs offline.
# No external network access required after initial setup.
#
# To ensure offline readiness, pre-pull all required images:
#   docker pull cockroachdb/cockroach:v23.1.11
#   docker pull redis:7-alpine
#   docker pull apache/kafka:3.9.1
#   docker pull quay.io/keycloak/keycloak:26.0
#
# Verify images are cached: docker images
# Then run: tilt up (works completely offline)

# Build date helper (requires POSIX-compliant shell)
def get_build_date():
    """Returns current UTC datetime in ISO 8601 format (macOS/Linux/WSL)"""
    # Use shell command instead of Python datetime (Starlark doesn't support datetime)
    # Note: Requires POSIX 'date' command (available on macOS, Linux, WSL, Git Bash)
    return str(local('date -u +"%Y-%m-%dT%H:%M:%SZ"')).strip()

# Allow Tilt to connect to local Kubernetes cluster
allow_k8s_contexts(['kind-meridian-local', 'kind-kind', 'minikube', 'docker-desktop', 'colima', 'rancher-desktop'])

# =============================================================================
# Configuration
# =============================================================================

# Fast startup mode - skip tests on initial load for faster development iteration
# Default: true (fast startup enabled) - tests can be manually triggered via 'tilt trigger test'
# Set TILT_FAST_STARTUP=false to run tests automatically on startup
fast_startup = os.getenv('TILT_FAST_STARTUP', 'true').lower() == 'true'

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

# Database configuration
# SECURITY: Never commit credentials to version control
# For local development with CockroachDB (insecure mode, no password required)
#
# Database-per-service architecture:
# Each service has its own database with a dedicated user for isolation.
# Connection URLs use service-specific databases (e.g., meridian_party, meridian_current_account)
# with corresponding users (e.g., meridian_party_user, meridian_current_account_user).
#
# See init-database.sh for database/user creation and ADR-0003 for architecture details.
db_urls = {
  'platform': os.getenv('PLATFORM_DATABASE_URL', 'postgres://meridian_platform_user@cockroachdb:26257/meridian_platform?sslmode=disable'),
  'current_account': os.getenv('CURRENT_ACCOUNT_DATABASE_URL', 'postgres://meridian_current_account_user@cockroachdb:26257/meridian_current_account?sslmode=disable'),
  'financial_accounting': os.getenv('FINANCIAL_ACCOUNTING_DATABASE_URL', 'postgres://meridian_financial_accounting_user@cockroachdb:26257/meridian_financial_accounting?sslmode=disable'),
  'position_keeping': os.getenv('POSITION_KEEPING_DATABASE_URL', 'postgres://meridian_position_keeping_user@cockroachdb:26257/meridian_position_keeping?sslmode=disable'),
  'payment_order': os.getenv('PAYMENT_ORDER_DATABASE_URL', 'postgres://meridian_payment_order_user@cockroachdb:26257/meridian_payment_order?sslmode=disable'),
  'party': os.getenv('PARTY_DATABASE_URL', 'postgres://meridian_party_user@cockroachdb:26257/meridian_party?sslmode=disable'),
}

# =============================================================================
# Helper Functions
# =============================================================================

def grpc_microservice(name, grpc_port, resource_deps=[], extra_k8s_objects=[]):
    """
    Define a gRPC microservice with standard build/deploy pattern.

    Args:
        name: Service name (e.g., 'current-account')
        grpc_port: gRPC port for port_forwards (e.g., 50051)
        resource_deps: List of dependencies beyond the defaults (e.g., ['migrate-current-account'])
        extra_k8s_objects: Additional k8s objects to track (e.g., ['current-account:serviceaccount'])
    """
    # Standard build args
    build_args = {
        'VERSION': 'dev',
        'COMMIT': local('git rev-parse --short HEAD'),
        'BUILD_DATE': get_build_date(),
    }

    # Docker build
    docker_build(
        name,
        context='.',
        dockerfile='services/{}/cmd/Dockerfile'.format(name),
        build_args=build_args,
    )

    # K8s manifests (standard pattern: secret, configmap, deployment, service)
    k8s_yaml('services/{}/k8s/secret.yaml'.format(name))
    k8s_yaml('services/{}/k8s/configmap.yaml'.format(name))
    k8s_yaml('services/{}/k8s/deployment.yaml'.format(name))
    k8s_yaml('services/{}/k8s/service.yaml'.format(name))

    # K8s resource configuration
    # All microservices depend on generate-proto
    all_deps = ['generate-proto'] + resource_deps
    k8s_resource(
        name,
        port_forwards=['{}:{}'.format(grpc_port, grpc_port)],
        resource_deps=all_deps,
        labels=['microservices'],
        objects=extra_k8s_objects,
    )

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
k8s_yaml('deployments/k8s/local/cockroachdb.yaml')

# Redis - Default configuration
k8s_yaml('deployments/k8s/local/redis.yaml')

# Kafka - 3-broker cluster with KRaft mode
k8s_yaml('deployments/k8s/local/kafka.yaml')

# Keycloak - Identity and Access Management
k8s_yaml('deployments/k8s/local/keycloak.yaml')

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
  'audit-worker',
  context='.',
  dockerfile='Dockerfile.dev',
  build_args={
    'VERSION': 'dev',
    'COMMIT': local('git rev-parse --short HEAD'),
    'BUILD_DATE': get_build_date(),
  },
  live_update=[
    # Sync Go source code
    sync('./services', '/app/services'),
    sync('./shared', '/app/shared'),
    sync('./go.mod', '/app/go.mod'),
    sync('./go.sum', '/app/go.sum'),

    # Rebuild binary on changes (fast incremental builds)
    run(
      'cd /app && go build -o audit-worker ./services/audit-worker',
      trigger=['./services', './shared'],
    ),

    # Restart the service using HUP signal
    run('kill -HUP 1', trigger=['./services', './shared']),
  ],
)

# Deploy Kubernetes manifests
k8s_yaml(kustomize('deployments/k8s/base'))

# audit-worker DB Secret - for local development only
# Uses the same CockroachDB credentials as other services
# NOTE: Production deployments must use External Secrets Operator or Sealed Secrets
secret_path = 'deployments/k8s/base/secret.yaml'
if os.path.exists(secret_path):
  # Load custom secret if manually created via scripts/setup-local-secrets.sh
  k8s_yaml(secret_path)
else:
  # Generate default development secret (consistent with current-account, financial-accounting, position-keeping)
  k8s_yaml(blob('''
apiVersion: v1
kind: Secret
metadata:
  name: audit-worker-db
  labels:
    app: audit-worker
type: Opaque
stringData:
  # Local development connection string - CockroachDB in insecure mode
  DATABASE_URL: "postgres://meridian_platform_user@cockroachdb:26257/meridian_platform?sslmode=disable"
'''))

# Set resource dependencies
k8s_resource(
  'audit-worker',
  port_forwards=[
    '8080:8080',  # HTTP API
  ],
  resource_deps=[
    'generate-proto',  # Ensures proto files are generated before building
    'init-database',  # Ensures database and user are created before app starts
    'redis',
    'kafka-cluster',
    'keycloak',
  ],
  labels=['infrastructure'],
  # Group RBAC and config resources under the main app
  objects=[
    'audit-worker:serviceaccount',
    'audit-worker:role',
    'audit-worker:rolebinding',
    'audit-worker-config:configmap',
    # Note: audit-worker-version ConfigMap omitted (Kustomize hash suffix changes with content)
    # Tilt will still deploy it via kustomize, just not explicitly tracked here
  ],
)

# =============================================================================
# Microservices
# =============================================================================

# Current-Account Service - gRPC microservice for customer and account management
grpc_microservice(
    'current-account',
    grpc_port=50051,
    resource_deps=['cockroachdb', 'migrate-current-account'],
)

# Financial-Accounting Service - gRPC microservice for ledger and booking operations
grpc_microservice(
    'financial-accounting',
    grpc_port=50052,
    resource_deps=['cockroachdb', 'migrate-financial-accounting'],
)

# Position-Keeping Service - gRPC microservice for transaction position management
grpc_microservice(
    'position-keeping',
    grpc_port=50053,
    resource_deps=['cockroachdb', 'migrate-position-keeping'],
)

# Payment-Order Service - gRPC microservice for payment order management with saga orchestration
grpc_microservice(
    'payment-order',
    grpc_port=50054,
    resource_deps=['cockroachdb', 'kafka-cluster', 'current-account', 'migrate-payment-order'],
)

# Tenant Service - gRPC microservice for platform tenant registry
grpc_microservice(
    'tenant',
    grpc_port=50056,
    resource_deps=['cockroachdb', 'migrate-tenant'],
)

# Party Service - gRPC microservice for party reference data management
grpc_microservice(
    'party',
    grpc_port=50055,
    resource_deps=['cockroachdb', 'migrate-party'],
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

# Label standalone kafka client service (defined in kafka.yaml)
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
# In fast startup mode, tests are disabled on initial load but can be manually triggered
local_resource(
  'test',
  cmd='make test',
  deps=['./services', './shared', './utilities', './go.mod'],
  resource_deps=['generate-proto'],
  labels=['quality'],
  allow_parallel=True,
  auto_init=False if fast_startup else True,  # Skip on startup in fast mode
)

# Run linters on file changes
local_resource(
  'lint',
  cmd='make lint',
  deps=['./services', './shared', './utilities', './go.mod'],
  labels=['quality'],
  allow_parallel=True,
  auto_init=False,  # Run manually with 'tilt trigger lint'
)

# Generate protobuf files - runs once on Tilt startup
# Ensures all *.pb.go files exist before building Go code
# Manual re-trigger: tilt trigger generate-proto
local_resource(
  'generate-proto',
  cmd='make proto',
  labels=['build'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,  # Manual re-trigger only; auto_init runs once on startup
  deps=['api/proto'],
)

# Initialize CockroachDB database and user - runs automatically after CockroachDB is ready
# Creates the meridian database and user required for the application
# Uses dedicated script with pod readiness check and verification
local_resource(
  'init-database',
  cmd='./scripts/init-database.sh',
  resource_deps=['cockroachdb'],
  labels=['database'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,  # Manual re-trigger; auto_init runs it on startup
)

# Run database migrations on startup - uses Atlas to apply schema changes
# Database-per-service architecture:
# - Each service has its own database (e.g., meridian_party, meridian_current_account)
# - Within each database, org schemas are created for multi-tenant isolation
# - Tables use singular, unqualified names (relies on search_path for routing)
#
# Migrations execute in parallel where possible:
# - Parallel: current_account + financial_accounting + party + tenant (all independent)
# - Sequential: position_keeping and payment_order wait for current_account (Account FK reference)
# This minimizes total migration time while respecting schema dependencies
local_resource(
  'migrate-current-account',
  cmd='atlas migrate apply --env local --config file://services/current-account/atlas/atlas.hcl --url "{}" --allow-dirty'.format(db_urls['current_account']),
  resource_deps=['init-database'],  # Database and user must exist before migrations
  labels=['database'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,
)

local_resource(
  'migrate-position-keeping',
  cmd='atlas migrate apply --env local --config file://services/position-keeping/atlas/atlas.hcl --url "{}" --allow-dirty'.format(db_urls['position_keeping']),
  resource_deps=['migrate-current-account'],  # Depends on current_account being migrated first
  labels=['database'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,
)

local_resource(
  'migrate-financial-accounting',
  cmd='atlas migrate apply --env local --config file://services/financial-accounting/atlas/atlas.hcl --url "{}" --allow-dirty'.format(db_urls['financial_accounting']),
  resource_deps=['init-database'],  # Independent database, only needs init to complete
  labels=['database'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,
)

local_resource(
  'migrate-payment-order',
  cmd='atlas migrate apply --env local --config file://services/payment-order/atlas/atlas.hcl --url "{}" --allow-dirty'.format(db_urls['payment_order']),
  resource_deps=['migrate-current-account'],  # Depends on current_account for account FK reference
  labels=['database'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,
)

local_resource(
  'migrate-tenant',
  cmd='atlas migrate apply --env local --config file://services/tenant/atlas/atlas.hcl --url "{}" --allow-dirty'.format(db_urls['platform']),
  resource_deps=['init-database'],  # Independent database (platform), only needs init to complete
  labels=['database'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,
)

local_resource(
  'migrate-party',
  cmd='atlas migrate apply --env local --config file://services/party/atlas/atlas.hcl --url "{}" --allow-dirty'.format(db_urls['party']),
  resource_deps=['init-database'],  # Independent database, only needs init to complete
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

fast_startup_msg = """
⚡ FAST STARTUP MODE ENABLED
   Tests skipped on initial load (trigger manually: 'tilt trigger test')
""" if fast_startup else ""

print("""
========================================
🚀 Meridian Development Environment
========================================
{}
Services:
  • Meridian API           → http://localhost:8080
  • Meridian gRPC          → localhost:9090

Microservices:
  • Current-Account        → localhost:50051 (gRPC)
  • Financial-Accounting   → localhost:50052 (gRPC)
  • Position-Keeping       → localhost:50053 (gRPC)
  • Payment-Order          → localhost:50054 (gRPC)
  • Party                  → localhost:50055 (gRPC)
  • Tenant                 → localhost:50056 (gRPC)

Backing Services:
  • CockroachDB            → localhost:26257
  • Redis                  → localhost:6379
  • Kafka Cluster          → localhost:9092
    - 3 brokers with KRaft quorum (kafka-0, kafka-1, kafka-2)
    - Replication factor: 2 (tolerates 1 broker failure)
  • Keycloak               → http://localhost:18080
    - Admin console: admin/admin
    - Realm: meridian (create manually)
    - JWKS: http://localhost:18080/realms/meridian/protocol/openid-connect/certs

Observability Stack:
  • Grafana                → http://localhost:3000 (dashboards, traces, logs, metrics)
  • Prometheus             → http://localhost:9090 (metrics queries)
  • Tempo                  → traces via Alloy OTLP endpoint (alloy:4317)
  • Loki                   → logs aggregation
  • Alloy                  → OpenTelemetry collector

Tilt UI                    → http://localhost:10350

Hot reload: Edit Go code and see changes in ~3 seconds

Database Architecture (database-per-service):
  • Each service has its own database with dedicated user:
    - meridian_platform       (tenant service)
    - meridian_current_account
    - meridian_financial_accounting
    - meridian_position_keeping
    - meridian_payment_order
    - meridian_party
  • Within each database: org schemas for multi-tenant isolation
  • Tables use singular, unqualified names (search_path routing)
  • See ADR-0003 for architecture details

Database Migrations:
  • Migrations run automatically on startup (6 resources):
    1. current_account → meridian_current_account (account, lien, audit tables)
    2. financial_accounting → meridian_financial_accounting (ledger, booking)
    3. position_keeping → meridian_position_keeping (positions, transactions)
    4. payment_order → meridian_payment_order (payment orders, saga state)
    5. party → meridian_party (party reference data)
    6. tenant → meridian_platform (tenant registry)
  • Parallel execution: current_account + financial_accounting + party + tenant
  • Sequential dependencies:
    - position_keeping waits for current_account (Account FK)
    - payment_order waits for current_account (Account FK)
  • Manual triggers:
    - tilt trigger migrate-current-account
    - tilt trigger migrate-financial-accounting
    - tilt trigger migrate-position-keeping
    - tilt trigger migrate-payment-order
    - tilt trigger migrate-party
    - tilt trigger migrate-tenant

Testing Kafka Failover:
  kubectl delete pod kafka-1  # Kill broker
  kubectl exec kafka-0 -- kafka-topics --describe --topic <topic> --bootstrap-server localhost:9092

Fast Startup Mode (default):
  • Tests are skipped on startup for faster iteration
  • Manually trigger tests: tilt trigger test
  • Disable: export TILT_FAST_STARTUP=false && tilt up

Storage Limits (prevent disk exhaustion):
  • Observability stack: ~7Gi max (Tempo 2Gi, Loki 2Gi, Prometheus 3Gi)
  • Kafka: ~1.5Gi per broker (512MB/partition, 24h retention)
  • Docker daemon logs: Configure in ~/.docker/daemon.json:
    {{"log-driver": "json-file", "log-opts": {{"max-size": "10m", "max-file": "3"}}}}
  • Kind cluster: Restart Kind to reclaim emptyDir space

========================================
""".format(fast_startup_msg))
