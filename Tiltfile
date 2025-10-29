# -*- mode: Python -*-

# Tiltfile for Meridian local development
# Fast Kubernetes development with live reload

# Load Tilt extensions
load('ext://helm_remote', 'helm_remote')
load('ext://restart_process', 'docker_build_with_restart')

# Allow Tilt to connect to local Kubernetes cluster
allow_k8s_contexts(['kind-meridian-local', 'kind-kind', 'minikube', 'docker-desktop', 'colima', 'rancher-desktop'])

# =============================================================================
# Configuration
# =============================================================================

# Docker image configuration
docker_registry = os.getenv('DOCKER_REGISTRY', 'ghcr.io/meridianhub')
image_name = 'meridian'
full_image = '{}/{}'.format(docker_registry, image_name)

# Kubernetes namespace
k8s_namespace = 'default'

# =============================================================================
# Backing Services
# =============================================================================

# CockroachDB - Single-node for local development
k8s_yaml('''
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
''')

# Redis - Default configuration
k8s_yaml('''
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
''')

# Kafka + Zookeeper using Confluent helm charts
# Uses publicly available Confluent images
helm_remote(
  'cp-helm-charts',
  repo_name='confluentinc',
  repo_url='https://confluentinc.github.io/cp-helm-charts/',
  namespace=k8s_namespace,
  values=[
    'deployments/tilt/confluent-values.yaml',
  ],
)

# =============================================================================
# Main Application
# =============================================================================

# Build Docker image with live reload
# Use simple 'meridian' name to match deployment spec
docker_build(
  'meridian',
  context='.',
  dockerfile='Dockerfile',
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
    'kafka',
    'zookeeper',
  ],
  labels=['app'],
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
)

# Redis resource
k8s_resource(
  'redis',
  port_forwards='6379:6379',
  labels=['cache'],
  resource_deps=[],
)

# Confluent Platform resources (managed by helm chart)
k8s_resource(
  'cp-helm-charts-cp-zookeeper',
  new_name='zookeeper',
  port_forwards='2181:2181',
  labels=['infrastructure', 'messaging'],
  resource_deps=[],
)

k8s_resource(
  'cp-helm-charts-cp-kafka',
  new_name='kafka',
  port_forwards='9092:9092',
  labels=['infrastructure', 'messaging'],
  resource_deps=['zookeeper'],
)

# =============================================================================
# Development Helpers
# =============================================================================

# Run tests on file changes
local_resource(
  'test',
  cmd='make test',
  deps=['./cmd', './internal', './pkg', './go.mod'],
  labels=['tests'],
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

# =============================================================================
# UI Configuration
# =============================================================================

# Tilt UI settings
update_settings(max_parallel_updates=3, k8s_upsert_timeout_secs=60)

print("""
╔══════════════════════════════════════════════════════════════╗
║                                                              ║
║  🚀 Meridian Development Environment                         ║
║                                                              ║
║  Services:                                                   ║
║    • Meridian API     → http://localhost:8080                ║
║    • Meridian gRPC    → localhost:9090                       ║
║    • CockroachDB      → localhost:26257                      ║
║    • Redis            → localhost:6379                       ║
║    • Kafka            → localhost:9092                       ║
║    • Zookeeper        → localhost:2181                       ║
║                                                              ║
║  Tilt UI              → http://localhost:10350               ║
║                                                              ║
║  Hot reload: Edit Go code and see changes in ~3 seconds     ║
║                                                              ║
╚══════════════════════════════════════════════════════════════╝
""")
