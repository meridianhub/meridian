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
#
# NOTE: All URLs use 'cockroachdb' hostname since both services and migrations
# run inside the cluster (migrations run as Kubernetes Jobs, not local_resource).
db_urls = {
  'platform': os.getenv('PLATFORM_DATABASE_URL', 'postgres://meridian_platform_user@cockroachdb:26257/meridian_platform?sslmode=disable'),
  'control_plane': os.getenv('CONTROL_PLANE_DATABASE_URL', 'postgres://meridian_control_plane_user@cockroachdb:26257/meridian_control_plane?sslmode=disable'),
  'current_account': os.getenv('CURRENT_ACCOUNT_DATABASE_URL', 'postgres://meridian_current_account_user@cockroachdb:26257/meridian_current_account?sslmode=disable'),
  'financial_accounting': os.getenv('FINANCIAL_ACCOUNTING_DATABASE_URL', 'postgres://meridian_financial_accounting_user@cockroachdb:26257/meridian_financial_accounting?sslmode=disable'),
  'position_keeping': os.getenv('POSITION_KEEPING_DATABASE_URL', 'postgres://meridian_position_keeping_user@cockroachdb:26257/meridian_position_keeping?sslmode=disable'),
  'payment_order': os.getenv('PAYMENT_ORDER_DATABASE_URL', 'postgres://meridian_payment_order_user@cockroachdb:26257/meridian_payment_order?sslmode=disable'),
  'party': os.getenv('PARTY_DATABASE_URL', 'postgres://meridian_party_user@cockroachdb:26257/meridian_party?sslmode=disable'),
  'internal_account': os.getenv('INTERNAL_ACCOUNT_DATABASE_URL', 'postgres://meridian_internal_account_user@cockroachdb:26257/meridian_internal_account?sslmode=disable'),
  'market_information': os.getenv('MARKET_INFORMATION_DATABASE_URL', 'postgres://meridian_market_information_user@cockroachdb:26257/meridian_market_information?sslmode=disable'),
  'reconciliation': os.getenv('RECONCILIATION_DATABASE_URL', 'postgres://meridian_reconciliation_user@cockroachdb:26257/meridian_reconciliation?sslmode=disable'),
  'forecasting': os.getenv('FORECASTING_DATABASE_URL', 'postgres://meridian_forecasting_user@cockroachdb:26257/meridian_forecasting?sslmode=disable'),
  'reference_data': os.getenv('REFERENCE_DATA_DATABASE_URL', 'postgres://meridian_reference_data_user@cockroachdb:26257/meridian_reference_data?sslmode=disable'),
}

# NOTE: Migrations now run as Kubernetes Jobs inside the cluster
# They use db_urls (with 'cockroachdb' hostname) since they run in pods

# =============================================================================
# Helper Functions
# =============================================================================

def migration_job(name, service_name, db_url_key, resource_deps=[]):
    """
    Create a Kubernetes Job to run database migrations inside the cluster.
    The job runs inside the cluster where 'cockroachdb' hostname resolves.

    Creates ConfigMaps for migration files and atlas.hcl, then creates a Job
    that runs Atlas migration inside the cluster.

    Args:
        name: Migration job name (e.g., 'migrate-current-account')
        service_name: Service name for migration path (e.g., 'current-account')
        db_url_key: Key in db_urls dictionary (e.g., 'current_account')
        resource_deps: List of resource dependencies
    """
    configmap_name_migrations = '{}-migrations'.format(name)
    configmap_name_atlas = '{}-atlas-config'.format(name)

    # Create ConfigMap for migration files
    local_resource(
        '{}-create-migrations-cm'.format(name),
        cmd='kubectl create configmap {} --from-file=services/{}/migrations --dry-run=client -o yaml | kubectl apply -f -'.format(
            configmap_name_migrations, service_name
        ),
        resource_deps=resource_deps,
        labels=['database'],
        auto_init=True,
        trigger_mode=TRIGGER_MODE_MANUAL,
    )

    # Create ConfigMap for atlas.hcl
    local_resource(
        '{}-create-atlas-cm'.format(name),
        cmd='kubectl create configmap {} --from-file=atlas.hcl=services/{}/atlas/atlas.hcl --dry-run=client -o yaml | kubectl apply -f -'.format(
            configmap_name_atlas, service_name
        ),
        resource_deps=resource_deps,
        labels=['database'],
        auto_init=True,
        trigger_mode=TRIGGER_MODE_MANUAL,
    )

    # Delete any existing job before creating new one (Jobs are immutable)
    # This ensures re-runs work correctly
    local_resource(
        '{}-cleanup'.format(name),
        cmd='kubectl delete job {} --ignore-not-found=true'.format(name),
        resource_deps=['{}-create-migrations-cm'.format(name), '{}-create-atlas-cm'.format(name)] + resource_deps,
        labels=['database'],
        auto_init=True,
        trigger_mode=TRIGGER_MODE_MANUAL,
    )

    # Create Job YAML using official Atlas image (no curl|sh)
    job_yaml = '''
apiVersion: batch/v1
kind: Job
metadata:
  name: {job_name}
spec:
  ttlSecondsAfterFinished: 300
  activeDeadlineSeconds: 300
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: migrate
        image: arigaio/atlas:1.0.0-alpine
        command:
        - /bin/sh
        - -c
        - |
          cd /workspace
          atlas migrate apply \\
            --env local \\
            --config file://services/{service_name}/atlas/atlas.hcl \\
            --url "{db_url}" \\
            --allow-dirty
        env:
        - name: DATABASE_URL
          value: "{db_url}"
        resources:
          limits:
            cpu: "500m"
            memory: "256Mi"
          requests:
            cpu: "100m"
            memory: "128Mi"
        volumeMounts:
        - name: migrations
          mountPath: /workspace/services/{service_name}/migrations
        - name: atlas-config
          mountPath: /workspace/services/{service_name}/atlas
      volumes:
      - name: migrations
        configMap:
          name: {configmap_migrations}
      - name: atlas-config
        configMap:
          name: {configmap_atlas}
  backoffLimit: 5
'''.format(
        job_name=name,
        service_name=service_name,
        db_url=db_urls[db_url_key],
        configmap_migrations=configmap_name_migrations,
        configmap_atlas=configmap_name_atlas,
    )

    k8s_yaml(blob(job_yaml))

    # Track the Job resource
    k8s_resource(
        name,
        labels=['database'],
        resource_deps=['{}-cleanup'.format(name)],
        auto_init=True,
    )

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
      'cd /app && go build -o audit-worker ./services/audit-worker/cmd',
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
    'migrate-reference-data',  # Last in migration chain — ensures ALL service tables (incl. audit_outbox) exist
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
# Event Router
# =============================================================================

# Standard build args for event-router
event_router_build_args = {
  'VERSION': 'dev',
  'COMMIT': local('git rev-parse --short HEAD'),
  'BUILD_DATE': get_build_date(),
}

# Build event-router Docker image
docker_build(
  'event-router',
  context='.',
  dockerfile='services/event-router/cmd/Dockerfile',
  build_args=event_router_build_args,
)

# Deploy event-router K8s manifests
k8s_yaml('services/event-router/k8s/configmap.yaml')
k8s_yaml('services/event-router/k8s/deployment.yaml')
k8s_yaml('services/event-router/k8s/service.yaml')

# Configure event-router resource
k8s_resource(
  'event-router',
  port_forwards=[
    '8081:8080',  # HTTP/metrics (using 8081 to avoid conflict with audit-worker)
  ],
  resource_deps=[
    'generate-proto',  # Ensures proto files are generated before building
    'kafka-cluster',   # Needs Kafka to consume audit events
    'position-keeping', # Needs Position Keeping gRPC endpoint
  ],
  labels=['infrastructure'],
  objects=[
    'event-router-config:configmap',
  ],
)

# =============================================================================
# Microservices
# =============================================================================
# PORT CONFIGURATION: The canonical source for port numbers is shared/platform/ports/ports.go
# This Tiltfile uses hardcoded values because Starlark cannot import Go constants.
# If updating ports here, ensure shared/platform/ports/ports.go is also updated.
#
# Port assignments:
#   - CurrentAccount:       50051
#   - FinancialAccounting:  50052
#   - PositionKeeping:      50053
#   - PaymentOrder:         50054
#   - Party:                50055
#   - Tenant:               50056
#   - InternalAccount:  50057
#   - MarketInformation:    50058
#   - ReferenceData:        50059
#   - Reconciliation:       50060
#   - Forecasting:          50061
#   - ControlPlane:         50062
#   - Gateway (HTTP):       8080
#   - HTTPHealth:           8081
#   - HTTPMetrics:          9090

# Current-Account Service - gRPC microservice for customer and account management
grpc_microservice(
    'current-account',
    grpc_port=50051,  # ports.CurrentAccount
    resource_deps=['cockroachdb', 'migrate-current-account'],
)

# Financial-Accounting Service - gRPC microservice for ledger and booking operations
grpc_microservice(
    'financial-accounting',
    grpc_port=50052,  # ports.FinancialAccounting
    resource_deps=['cockroachdb', 'migrate-financial-accounting'],
)

# Position-Keeping Service - gRPC microservice for transaction position management
grpc_microservice(
    'position-keeping',
    grpc_port=50053,  # ports.PositionKeeping
    resource_deps=['cockroachdb', 'migrate-position-keeping'],
)

# Payment-Order Service - gRPC microservice for payment order management with saga orchestration
grpc_microservice(
    'payment-order',
    grpc_port=50054,  # ports.PaymentOrder
    resource_deps=['cockroachdb', 'kafka-cluster', 'current-account', 'migrate-payment-order'],
)

# Tenant Service - gRPC microservice for platform tenant registry
grpc_microservice(
    'tenant',
    grpc_port=50056,  # ports.Tenant
    resource_deps=['cockroachdb', 'migrate-tenant'],
)

# Party Service - gRPC microservice for party reference data management
grpc_microservice(
    'party',
    grpc_port=50055,  # ports.Party
    resource_deps=['cockroachdb', 'migrate-party'],
)

# Internal-Account Service - gRPC microservice for internal account management
grpc_microservice(
    'internal-account',
    grpc_port=50057,  # ports.InternalAccount
    resource_deps=['cockroachdb', 'migrate-internal-account', 'position-keeping', 'current-account'],
)

# Market-Information Service - gRPC microservice for price benchmarks and market data
grpc_microservice(
    'market-information',
    grpc_port=50058,  # ports.MarketInformation
    resource_deps=['cockroachdb', 'migrate-market-information'],
)

# Reconciliation Service - gRPC microservice for reconciliation processes and settlement
grpc_microservice(
    'reconciliation',
    grpc_port=50060,  # ports.Reconciliation
    resource_deps=['cockroachdb', 'migrate-reconciliation', 'position-keeping', 'current-account'],
)

# Forecasting Service - gRPC microservice for forecasting strategies and forward curves
grpc_microservice(
    'forecasting',
    grpc_port=50061,  # ports.Forecasting
    resource_deps=['cockroachdb', 'migrate-forecasting', 'market-information', 'redis'],
)

# Reference Data Service - gRPC microservice for instrument definitions, nodes, and saga definitions
grpc_microservice(
    'reference-data',
    grpc_port=50059,  # ports.ReferenceData
    resource_deps=['cockroachdb', 'migrate-reference-data'],
)

# Control Plane Service - gRPC microservice for manifest application and validation
grpc_microservice(
    'control-plane',
    grpc_port=50062,  # ports.ControlPlane
    resource_deps=['cockroachdb', 'migrate-control-plane'],
)

# =============================================================================
# Gateway Service
# =============================================================================
# HTTP gateway for tenant-aware routing via subdomain resolution.
# Routes requests to backend gRPC services using Connect protocol.

# Standard build args for gateway
gateway_build_args = {
    'VERSION': 'dev',
    'COMMIT': local('git rev-parse --short HEAD'),
    'BUILD_DATE': get_build_date(),
}

# Build gateway Docker image
docker_build(
    'gateway',
    context='.',
    dockerfile='services/api-gateway/cmd/Dockerfile',
    build_args=gateway_build_args,
)

# Deploy gateway K8s manifests
k8s_yaml('services/api-gateway/k8s/secret.yaml')
k8s_yaml('services/api-gateway/k8s/configmap.yaml')
k8s_yaml('services/api-gateway/k8s/deployment.yaml')
k8s_yaml('services/api-gateway/k8s/service.yaml')
k8s_yaml('services/api-gateway/k8s/ingress.yaml')

# Configure gateway resource
k8s_resource(
    'gateway',
    port_forwards=['8090:8080'],  # HTTP gateway (8090 to avoid conflict with audit-worker)
    resource_deps=[
        'generate-proto',       # Ensures proto files are generated before building
        'redis',                # For slug cache (optional, can use in-memory)
        'tenant',               # For tenant resolution
        'current-account',      # Backend service
        'party',                # Backend service
        'payment-order',        # Backend service
        'position-keeping',     # Backend service
    ],
    labels=['gateway'],
)

# =============================================================================
# MCP Server
# =============================================================================
# Model Context Protocol server for AI agent integration.
# Exposes Meridian's transaction engine capabilities via streamable HTTP transport.
# Connects to the gateway for all backend gRPC service calls.

# Standard build args for mcp-server
mcp_server_build_args = {
    'VERSION': 'dev',
    'COMMIT': local('git rev-parse --short HEAD'),
    'BUILD_DATE': get_build_date(),
}

# Build mcp-server Docker image
docker_build(
    'mcp-server',
    context='.',
    dockerfile='services/mcp-server/cmd/Dockerfile',
    build_args=mcp_server_build_args,
)

# Deploy mcp-server K8s manifests
k8s_yaml('services/mcp-server/k8s/secret.yaml')
k8s_yaml('services/mcp-server/k8s/configmap.yaml')
k8s_yaml('services/mcp-server/k8s/deployment.yaml')
k8s_yaml('services/mcp-server/k8s/service.yaml')

# Configure mcp-server resource
k8s_resource(
    'mcp-server',
    port_forwards=['18090:8090'],  # MCP HTTP endpoint (18090 to avoid conflict with gateway)
    resource_deps=[
        'generate-proto',   # Ensures proto files are generated before building
        'gateway',          # MCP server routes all gRPC calls through the gateway
        'control-plane',    # Wait for control-plane readiness (init container check)
    ],
    labels=['gateway'],
    objects=[
        'mcp-server-config:configmap',
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
# Migrations run as Kubernetes Jobs inside the cluster where 'cockroachdb' hostname resolves.
# This ensures migrations use the same network context as the services.
#
# Migrations run sequentially to avoid CockroachDB RETRY_SERIALIZABLE errors.
# Concurrent Atlas migrations contend on system.descriptor when creating schemas,
# causing TransactionRetryWithProtoRefreshError. Each migration is fast (~100ms)
# so sequential execution adds negligible startup time while eliminating flaky failures.
#
# Chain order preserves real FK dependencies:
#   current_account → position_keeping (FK)
#   current_account → payment_order (FK)
# Other services are independent but chained to avoid contention.
migration_job(
  'migrate-current-account',
  'current-account',
  'current_account',
  resource_deps=['init-database'],
)

migration_job(
  'migrate-position-keeping',
  'position-keeping',
  'position_keeping',
  resource_deps=['migrate-current-account'],  # FK dependency on current_account
)

migration_job(
  'migrate-financial-accounting',
  'financial-accounting',
  'financial_accounting',
  resource_deps=['migrate-position-keeping'],
)

migration_job(
  'migrate-payment-order',
  'payment-order',
  'payment_order',
  resource_deps=['migrate-financial-accounting'],  # Also has FK dep on current_account (satisfied transitively)
)

migration_job(
  'migrate-control-plane',
  'control-plane',
  'control_plane',
  resource_deps=['migrate-payment-order'],
)

migration_job(
  'migrate-tenant',
  'tenant',
  'platform',
  resource_deps=['migrate-control-plane'],
)

migration_job(
  'migrate-party',
  'party',
  'party',
  resource_deps=['migrate-tenant'],
)

migration_job(
  'migrate-internal-account',
  'internal-account',
  'internal_account',
  resource_deps=['migrate-party'],
)

migration_job(
  'migrate-market-information',
  'market-information',
  'market_information',
  resource_deps=['migrate-internal-account'],
)

migration_job(
  'migrate-reconciliation',
  'reconciliation',
  'reconciliation',
  resource_deps=['migrate-market-information'],
)

migration_job(
  'migrate-forecasting',
  'forecasting',
  'forecasting',
  resource_deps=['migrate-reconciliation'],
)

migration_job(
  'migrate-reference-data',
  'reference-data',
  'reference_data',
  resource_deps=['migrate-forecasting'],
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

# Seed dev tenant - creates a dev tenant via the gateway for local manual testing
# Idempotent: safe to re-run (exits 0 if tenant already exists)
local_resource(
  'seed-dev-tenant',
  cmd='./scripts/seed-dev-tenant.sh --grpc-addr=localhost:50056 --control-plane-addr=localhost:50062',
  resource_deps=['tenant', 'control-plane'],
  labels=['setup'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,  # Can be re-run manually via 'tilt trigger seed-dev-tenant'
)

# GetBalance smoke test - manual trigger for verifying balance query flow
local_resource(
  'smoke-test-get-balance',
  cmd='./scripts/smoke-test-get-balance.sh',
  resource_deps=['internal-account', 'position-keeping'],
  labels=['test'],
  auto_init=False,  # Run manually with 'tilt trigger smoke-test-get-balance'
  trigger_mode=TRIGGER_MODE_MANUAL,
)

# =============================================================================
# Frontend (Vite Dev Server)
# =============================================================================
# React + Vite frontend with hot module replacement.
# Connects to the gateway at localhost:8090 via Connect protocol.

# Install frontend dependencies (re-runs when package.json/lock changes)
local_resource(
  'frontend-deps',
  cmd='cd frontend && npm install',
  deps=['frontend/package.json', 'frontend/package-lock.json'],
  labels=['frontend'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,
)

# Generate TypeScript proto clients from api/proto definitions
# Uses buf + protoc-gen-es from frontend/node_modules/.bin
local_resource(
  'frontend-generate',
  cmd='cd frontend && npm run generate',
  deps=['api/proto'],
  resource_deps=['frontend-deps', 'generate-proto'],
  labels=['frontend'],
  auto_init=True,
  trigger_mode=TRIGGER_MODE_MANUAL,
)

# Start Vite dev server with HMR
local_resource(
  'frontend',
  serve_cmd='cd frontend && npm run dev -- --port 5173 --host 0.0.0.0',
  deps=['frontend/src', 'frontend/index.html', 'frontend/vite.config.ts'],
  resource_deps=['frontend-generate', 'gateway'],
  labels=['frontend'],
  links=['http://localhost:5173'],
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
  • Internal-Account  → localhost:50057 (gRPC)
  • Market-Information     → localhost:50058 (gRPC)
  • Reference-Data         → localhost:50059 (gRPC)
  • Reconciliation         → localhost:50060 (gRPC)
  • Forecasting            → localhost:50061 (gRPC)

Gateway:
  • HTTP Gateway           → localhost:8090 (subdomain routing)
    - Tenant resolution via TenantResolverMiddleware
    - Proxies to gRPC backends via Connect protocol
  • MCP Server             → localhost:18090 (streamable HTTP)
    - Model Context Protocol for AI agent integration
    - Routes all gRPC calls through the HTTP gateway

Frontend:
  • Meridian Console       → http://localhost:5173 (Vite + React)
    - Hot module replacement enabled
    - Connects to gateway at localhost:8090

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
    - meridian_control_plane  (control-plane service)
    - meridian_current_account
    - meridian_financial_accounting
    - meridian_position_keeping
    - meridian_payment_order
    - meridian_party
    - meridian_internal_account
    - meridian_market_information
    - meridian_reconciliation
    - meridian_forecasting
    - meridian_reference_data
  • Within each database: org schemas for multi-tenant isolation
  • Tables use singular, unqualified names (search_path routing)
  • See ADR-0003 for architecture details

Database Migrations:
  • Migrations run automatically on startup (12 resources):
    1. current_account → meridian_current_account (account, lien, audit tables)
    2. financial_accounting → meridian_financial_accounting (ledger, booking)
    3. position_keeping → meridian_position_keeping (positions, transactions)
    4. payment_order → meridian_payment_order (payment orders, saga state)
    5. party → meridian_party (party reference data)
    6. tenant → meridian_platform (tenant registry)
    7. internal_account → meridian_internal_account (internal accounts)
    8. market_information → meridian_market_information (price benchmarks, market data)
    9. reconciliation → meridian_reconciliation (reconciliation processes)
    10. forecasting → meridian_forecasting (forecasting strategies)
    11. reference_data → meridian_reference_data (instrument definitions, nodes, saga definitions)
  • Parallel execution: current_account + financial_accounting + party + tenant + control_plane + internal_account + market_information + reconciliation + forecasting + reference_data
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
    - tilt trigger migrate-internal-account
    - tilt trigger migrate-market-information
    - tilt trigger migrate-reconciliation
    - tilt trigger migrate-forecasting
    - tilt trigger migrate-reference-data

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
