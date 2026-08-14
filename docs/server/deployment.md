---
description: Deploy mcp-data-platform in development and production environments using Docker Compose or plain Kubernetes manifests.
---

# Deployment Guide

This guide covers deploying mcp-data-platform in various environments, from local development to production Kubernetes clusters.

---

## Deployment Options

| Environment | Best For | Complexity |
|-------------|----------|------------|
| **Docker Compose** | Development, small teams, testing | Low |
| **Kubernetes (plain manifests)** | Production, multi-user, enterprise | Medium |

---

## Docker Compose (Development/Small Teams)

A complete full-stack deployment including DataHub, Trino, mcp-data-platform, Keycloak, and PostgreSQL.

### Prerequisites

- Docker 24.0+
- Docker Compose 2.20+
- 16GB RAM minimum (DataHub requires significant memory)
- 20GB free disk space

### Full-Stack Example

Create a `docker-compose.yml`:

```yaml
services:
  # PostgreSQL for metadata storage
  postgres:
    image: postgres:16-alpine@sha256:acf5271bce6b4b62e352341e3b175c2b1e9e0b6f6e3f2e7e3b7f8c9d0e1f2a3b
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-postgres}
      POSTGRES_MULTIPLE_DATABASES: datahub,keycloak,audit
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./init-multiple-dbs.sh:/docker-entrypoint-initdb.d/init-multiple-dbs.sh
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 10s
      timeout: 5s
      retries: 5

  # Keycloak for authentication
  keycloak:
    image: quay.io/keycloak/keycloak:24.0@sha256:b3c4a5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4
    command: start-dev --import-realm
    environment:
      KC_DB: postgres
      KC_DB_URL: jdbc:postgresql://postgres:5432/keycloak
      KC_DB_USERNAME: postgres
      KC_DB_PASSWORD: ${POSTGRES_PASSWORD:-postgres}
      KEYCLOAK_ADMIN: admin
      KEYCLOAK_ADMIN_PASSWORD: ${KEYCLOAK_ADMIN_PASSWORD:-admin}
    volumes:
      - ./keycloak-realm.json:/opt/keycloak/data/import/realm.json
    ports:
      - "8180:8080"
    depends_on:
      postgres:
        condition: service_healthy

  # DataHub GMS (Metadata Service)
  datahub-gms:
    image: acryldata/datahub-gms:v0.13.0@sha256:c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2
    environment:
      DATAHUB_GMS_HOST: datahub-gms
      DATAHUB_GMS_PORT: 8080
      EBEAN_DATASOURCE_HOST: postgres:5432
      EBEAN_DATASOURCE_USERNAME: postgres
      EBEAN_DATASOURCE_PASSWORD: ${POSTGRES_PASSWORD:-postgres}
      ELASTICSEARCH_HOST: elasticsearch
      ELASTICSEARCH_PORT: 9200
      KAFKA_BOOTSTRAP_SERVER: kafka:9092
      KAFKA_SCHEMAREGISTRY_URL: http://schema-registry:8081
    depends_on:
      postgres:
        condition: service_healthy
      elasticsearch:
        condition: service_healthy
      kafka:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 5

  # Elasticsearch for DataHub search
  elasticsearch:
    image: elasticsearch:7.17.18@sha256:a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2
    environment:
      - discovery.type=single-node
      - xpack.security.enabled=false
      - ES_JAVA_OPTS=-Xms512m -Xmx512m
    volumes:
      - elasticsearch_data:/usr/share/elasticsearch/data
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:9200/_cluster/health"]
      interval: 10s
      timeout: 5s
      retries: 10

  # Kafka for DataHub events
  kafka:
    image: confluentinc/cp-kafka:7.6.0@sha256:b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2
    environment:
      KAFKA_BROKER_ID: 1
      KAFKA_ZOOKEEPER_CONNECT: zookeeper:2181
      KAFKA_ADVERTISED_LISTENERS: PLAINTEXT://kafka:9092
      KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR: 1
    depends_on:
      - zookeeper
    healthcheck:
      test: ["CMD", "kafka-topics", "--bootstrap-server", "kafka:9092", "--list"]
      interval: 30s
      timeout: 10s
      retries: 5

  # Zookeeper for Kafka
  zookeeper:
    image: confluentinc/cp-zookeeper:7.6.0@sha256:a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3
    environment:
      ZOOKEEPER_CLIENT_PORT: 2181
      ZOOKEEPER_TICK_TIME: 2000

  # Schema Registry for Kafka
  schema-registry:
    image: confluentinc/cp-schema-registry:7.6.0@sha256:c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4
    environment:
      SCHEMA_REGISTRY_HOST_NAME: schema-registry
      SCHEMA_REGISTRY_KAFKASTORE_BOOTSTRAP_SERVERS: kafka:9092
    depends_on:
      kafka:
        condition: service_healthy

  # Trino for SQL queries
  trino:
    image: trinodb/trino:440@sha256:d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5
    ports:
      - "8081:8080"
    volumes:
      - ./trino-catalog:/etc/trino/catalog
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/v1/info"]
      interval: 10s
      timeout: 5s
      retries: 10

  # MCP Data Platform
  mcp-data-platform:
    image: ghcr.io/txn2/mcp-data-platform:latest
    environment:
      DATAHUB_TOKEN: ${DATAHUB_TOKEN}
      DATABASE_URL: postgres://postgres:${POSTGRES_PASSWORD:-postgres}@postgres:5432/audit
      OAUTH_SIGNING_KEY: ${OAUTH_SIGNING_KEY}
      KEYCLOAK_CLIENT_SECRET: ${KEYCLOAK_CLIENT_SECRET}
    volumes:
      - ./platform.yaml:/etc/mcp/platform.yaml:ro
    command: ["--config", "/etc/mcp/platform.yaml", "--transport", "http", "--address", ":8080"]
    ports:
      - "8080:8080"
    depends_on:
      datahub-gms:
        condition: service_healthy
      trino:
        condition: service_healthy
      keycloak:
        condition: service_started

volumes:
  postgres_data:
  elasticsearch_data:
```

### Platform Configuration

Create `platform.yaml`:

```yaml
server:
  name: mcp-data-platform
  transport: http
  address: ":8080"

toolkits:
  datahub:
    enabled: true
    instances:
      primary:
        url: http://datahub-gms:8080
        token: ${DATAHUB_TOKEN}
    default: primary

  trino:
    enabled: true
    instances:
      primary:
        host: trino
        port: 8080
        user: trino
        catalog: memory
        ssl: false
    default: primary

oauth:
  enabled: true
  issuer: "http://localhost:8080"
  signing_key: ${OAUTH_SIGNING_KEY}
  clients:
    - id: "claude-desktop"
      secret: "claude-secret"
      redirect_uris:
        - "http://localhost"
        - "http://127.0.0.1"
  upstream:
    issuer: "http://keycloak:8080/realms/mcp"
    client_id: "mcp-data-platform"
    client_secret: ${KEYCLOAK_CLIENT_SECRET}
    redirect_uri: "http://localhost:8080/oauth/callback"

personas:
  analyst:
    display_name: "Data Analyst"
    roles: ["analyst"]
    tools:
      allow: ["*"]
      deny: ["*_delete_*"]
    connections:
      allow: ["*"]
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]

enrichment:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true
  column_context_filtering: true   # Only enrich columns referenced in SQL (default: true)

audit:
  enabled: true
  log_tool_calls: true

database:
  dsn: ${DATABASE_URL}
```

### Start the Stack

```bash
# Generate secrets
export POSTGRES_PASSWORD=$(openssl rand -base64 32)
export OAUTH_SIGNING_KEY=$(openssl rand -base64 32)
export KEYCLOAK_CLIENT_SECRET=$(openssl rand -base64 32)
export DATAHUB_TOKEN="your-datahub-token"

# Start all services
docker compose up -d

# Wait for services to be healthy
docker compose ps

# View logs
docker compose logs -f mcp-data-platform
```

### Local Development Workflow

For rapid iteration during development:

```bash
# Start dependencies only
docker compose up -d postgres elasticsearch kafka zookeeper schema-registry datahub-gms trino keycloak

# Run mcp-data-platform locally
go run ./cmd/mcp-data-platform --config platform.yaml --transport http --address :8080
```

---

## Kubernetes (Production)

Production deployment is plain Kubernetes manifests applied with `kubectl`.
This repository ships no Helm chart and no operator, and the observability
manifests under
[`deployments/observability/`](https://github.com/txn2/mcp-data-platform/tree/main/deployments/observability)
follow the same shape. The manifests below are complete as written: save them
into a directory, change the image tag, host names, and resource figures, and
apply the directory.

### Prerequisites

- Kubernetes 1.28+
- `kubectl` configured for the target cluster
- TLS certificates for the ingress (cert-manager recommended)
- PostgreSQL reachable from the cluster. It backs audit, portal, knowledge,
  memory, and the OAuth/PKCE state that multi-replica deployments share

### Namespace and secrets

```bash
kubectl create namespace mcp-data-platform

# Use external-secrets, sealed-secrets, or a secrets manager in production.
kubectl create secret generic mcp-data-platform-secrets \
  --namespace mcp-data-platform \
  --from-literal=datahub-token="$DATAHUB_TOKEN" \
  --from-literal=oauth-signing-key="$OAUTH_SIGNING_KEY" \
  --from-literal=keycloak-client-secret="$KEYCLOAK_CLIENT_SECRET" \
  --from-literal=encryption-key="$ENCRYPTION_KEY" \
  --from-literal=database-url="$DATABASE_URL"
```

`ENCRYPTION_KEY` is 32 bytes of key material (64 hex characters, 44-character
base64, or 32 raw bytes) and encrypts stored connection credentials, gateway
OAuth tokens, and PKCE state at rest. Without it the platform logs a warning at
startup and stores those values in plaintext.

### ServiceAccount

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: mcp-data-platform
  namespace: mcp-data-platform
  labels:
    app.kubernetes.io/name: mcp-data-platform
# The platform never calls the Kubernetes API; do not mount a token for it.
automountServiceAccountToken: false
```

### ConfigMap

The platform reads its YAML from a file and expands `${VAR}` from the process
environment, so credentials stay in the Secret and never enter the ConfigMap.
The full schema is in the [Configuration reference](configuration.md).

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: mcp-data-platform-config
  namespace: mcp-data-platform
  labels:
    app.kubernetes.io/name: mcp-data-platform
data:
  platform.yaml: |
    server:
      name: mcp-data-platform
      transport: http
      address: ":8080"
      tls:
        enabled: false   # TLS terminates at the ingress

    database:
      dsn: ${DATABASE_URL}

    toolkits:
      datahub:
        enabled: true
        instances:
          primary:
            url: http://datahub-gms.datahub:8080
            token: ${DATAHUB_TOKEN}
        default: primary
      trino:
        enabled: true
        instances:
          primary:
            host: trino.trino
            port: 8080
            user: mcp-platform
            catalog: hive
            ssl: false
        default: primary

    semantic:
      provider: datahub
      instance: primary

    enrichment:
      trino_semantic_enrichment: true
      datahub_query_enrichment: true
      column_context_filtering: true   # Only enrich columns referenced in SQL (default: true)

    oauth:
      enabled: true
      issuer: "https://mcp.example.com"
      signing_key: ${OAUTH_SIGNING_KEY}
      # MCP clients either pre-register under `clients:` or register
      # themselves through DCR; a deployment with neither admits no client.
      # Constrain the redirect URIs a self-registering client may claim.
      dcr:
        enabled: true
        allowed_redirect_patterns:
          - "^http://localhost.*"
          - "^http://127.0.0.1.*"
      upstream:
        issuer: "https://auth.example.com/realms/mcp"
        client_id: "mcp-data-platform"
        client_secret: ${KEYCLOAK_CLIENT_SECRET}
        redirect_uri: "https://mcp.example.com/oauth/callback"

    personas:
      analyst:
        display_name: "Data Analyst"
        roles: ["analyst"]
        tools:
          allow: ["*"]
          deny: ["*_delete_*"]
        connections:
          allow: ["*"]
      admin:
        display_name: "Administrator"
        roles: ["admin"]
        tools:
          allow: ["*"]
        connections:
          allow: ["*"]

    audit:
      enabled: true
      log_tool_calls: true
```

A ConfigMap change does not restart the pods on its own. Roll them after
editing it:

```bash
kubectl rollout restart deployment/mcp-data-platform -n mcp-data-platform
```

### Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-data-platform
  namespace: mcp-data-platform
  labels:
    app.kubernetes.io/name: mcp-data-platform
spec:
  replicas: 2
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  selector:
    matchLabels:
      app.kubernetes.io/name: mcp-data-platform
  template:
    metadata:
      labels:
        app.kubernetes.io/name: mcp-data-platform
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9090"
        prometheus.io/path: "/metrics"
    spec:
      serviceAccountName: mcp-data-platform
      automountServiceAccountToken: false
      # Defaults total ~40s (2s pre-shutdown + 25s drain + 10s lifecycle stop
      # + a few seconds of close). See the Tuning and Scaling guide.
      terminationGracePeriodSeconds: 60
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        runAsGroup: 1000
        fsGroup: 1000
        seccompProfile:
          type: RuntimeDefault
      affinity:
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              podAffinityTerm:
                labelSelector:
                  matchLabels:
                    app.kubernetes.io/name: mcp-data-platform
                topologyKey: kubernetes.io/hostname
      containers:
        - name: mcp-data-platform
          image: ghcr.io/txn2/mcp-data-platform:v1.120.0
          imagePullPolicy: IfNotPresent
          args:
            - --config
            - /etc/mcp-data-platform/platform.yaml
            - --transport
            - http
            - --address
            - :8080
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop:
                - ALL
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
            - name: metrics
              containerPort: 9090
              protocol: TCP
          env:
            # The Go runtime is not cgroup-aware; match it to the limits below.
            - name: GOMEMLIMIT
              value: "450MiB"   # ~90% of limits.memory
            - name: GOMAXPROCS
              value: "1"        # limits.cpu rounded up
            - name: LOG_LEVEL
              value: "info"
            - name: OTEL_METRICS_ENABLED
              value: "true"
            - name: OTEL_METRICS_ADDR
              value: ":9090"
            - name: DATAHUB_TOKEN
              valueFrom:
                secretKeyRef:
                  name: mcp-data-platform-secrets
                  key: datahub-token
            - name: OAUTH_SIGNING_KEY
              valueFrom:
                secretKeyRef:
                  name: mcp-data-platform-secrets
                  key: oauth-signing-key
            - name: KEYCLOAK_CLIENT_SECRET
              valueFrom:
                secretKeyRef:
                  name: mcp-data-platform-secrets
                  key: keycloak-client-secret
            - name: ENCRYPTION_KEY
              valueFrom:
                secretKeyRef:
                  name: mcp-data-platform-secrets
                  key: encryption-key
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: mcp-data-platform-secrets
                  key: database-url
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
            timeoutSeconds: 3
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 10
            periodSeconds: 30
            timeoutSeconds: 3
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: 1000m
              memory: 512Mi
          volumeMounts:
            - name: config
              mountPath: /etc/mcp-data-platform
              readOnly: true
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: config
          configMap:
            name: mcp-data-platform-config
        - name: tmp
          emptyDir: {}
```

`/readyz` reports `draining` (503) as soon as SIGTERM arrives, so the load
balancer stops routing to a terminating pod before the drain begins;
`/healthz` stays 200 for as long as the process is alive. Size the probe and
resource figures from the
[Tuning and Scaling guide](../reference/tuning-and-scaling.md), which covers
`GOMEMLIMIT`/`GOMAXPROCS` selection, the four-stage shutdown budget, and
measured per-replica throughput.

### Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: mcp-data-platform
  namespace: mcp-data-platform
  labels:
    app.kubernetes.io/name: mcp-data-platform
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: mcp-data-platform
  ports:
    - name: http
      port: 8080
      targetPort: http
      protocol: TCP
    - name: metrics
      port: 9090
      targetPort: metrics
      protocol: TCP
```

### Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: mcp-data-platform
  namespace: mcp-data-platform
  labels:
    app.kubernetes.io/name: mcp-data-platform
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/proxy-body-size: "10m"
    # SSE and streamable HTTP hold the response open; a short read timeout
    # cuts live MCP sessions.
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
spec:
  ingressClassName: nginx
  tls:
    - secretName: mcp-data-platform-tls
      hosts:
        - mcp.example.com
  rules:
    - host: mcp.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: mcp-data-platform
                port:
                  name: http
```

The metrics port is deliberately not routed through the ingress. It carries no
authentication of its own, and keeping it cluster-internal is what makes that
acceptable; see [Observability](observability.md).

### HorizontalPodAutoscaler

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: mcp-data-platform
  namespace: mcp-data-platform
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: mcp-data-platform
  minReplicas: 2
  maxReplicas: 10
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 70
```

CPU-driven autoscaling is only meaningful once `GOMAXPROCS` matches the CPU
limit; without it the runtime sizes itself from the node's core count and the
utilization figure is not comparable across nodes.

### PodDisruptionBudget

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: mcp-data-platform
  namespace: mcp-data-platform
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: mcp-data-platform
```

### Apply

```bash
kubectl apply -f ./manifests -n mcp-data-platform

kubectl rollout status deployment/mcp-data-platform -n mcp-data-platform
kubectl get pods,hpa -n mcp-data-platform
```

### Split deployment: portal and script workers

Every replica of the deployment above serves traffic, executes queued
[managed scripts](../scripts/running.md), and turns their due schedules into
runs. That is the right shape until scripts start doing real work: the Starlark interpreter has no hard per-script memory
cap, so a heavy approved script pushes on the memory of a pod that agents are
also talking to, and execution capacity is tied to serving capacity even though
the two scale on different signals.

Splitting them changes one configuration key and adds one Deployment. Both use
the same image and the same ConfigMap; nothing else about the platform differs
between them. Schedules follow the worker: the serving pods still accept a
schedule being set, and the worker pods are what fire it, so a split deployment
with no worker replicas running stores schedules that nothing materializes.

Make the switch an environment variable in the shared ConfigMap, so each
deployment sets its own value:

```yaml
# In the ConfigMap's platform.yaml, alongside the other blocks.
scripts:
  worker:
    # Serving pods set SCRIPTS_WORKER_ENABLED=false; worker pods set it true.
    # Unset means enabled, which keeps the single-binary deployment unchanged.
    enabled: ${SCRIPTS_WORKER_ENABLED:-true}
```

Then add the variable to the serving Deployment's container `env`:

```yaml
            - name: SCRIPTS_WORKER_ENABLED
              value: "false"
```

And apply a second Deployment for the workers. It is the serving manifest with
four changes: the name and labels, `SCRIPTS_WORKER_ENABLED=true`, more memory
(a run holds its result set in the interpreter's heap), and no HPA, Service, or
Ingress. It is the same binary and still starts the HTTP listener, so its
health and metrics endpoints work as they do everywhere else; it takes its work
from the database queue rather than from a request, so nothing needs to route
to it.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-data-platform-scripts
  namespace: mcp-data-platform
  labels:
    app.kubernetes.io/name: mcp-data-platform
    app.kubernetes.io/component: script-worker
spec:
  # A replica executes one run at a time, so concurrency is the replica count.
  replicas: 2
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  selector:
    matchLabels:
      app.kubernetes.io/name: mcp-data-platform
      app.kubernetes.io/component: script-worker
  template:
    metadata:
      labels:
        app.kubernetes.io/name: mcp-data-platform
        app.kubernetes.io/component: script-worker
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9090"
        prometheus.io/path: "/metrics"
    spec:
      serviceAccountName: mcp-data-platform
      automountServiceAccountToken: false
      terminationGracePeriodSeconds: 60
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        runAsGroup: 1000
        fsGroup: 1000
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: mcp-data-platform
          image: ghcr.io/txn2/mcp-data-platform:v1.120.0
          imagePullPolicy: IfNotPresent
          args:
            - --config
            - /etc/mcp-data-platform/platform.yaml
            - --transport
            - http
            - --address
            - :8080
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop:
                - ALL
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
            - name: metrics
              containerPort: 9090
              protocol: TCP
          env:
            - name: SCRIPTS_WORKER_ENABLED
              value: "true"
            # A script's result set lives in the interpreter's heap, which has
            # no hard cap, so give the worker room and tell the Go runtime
            # where the ceiling is. GOMEMLIMIT is what makes the collector work
            # against the limit instead of discovering it.
            - name: GOMEMLIMIT
              value: "900MiB"   # ~90% of limits.memory
            - name: GOMAXPROCS
              value: "1"
            - name: LOG_LEVEL
              value: "info"
            - name: OTEL_METRICS_ENABLED
              value: "true"
            - name: OTEL_METRICS_ADDR
              value: ":9090"
            - name: DATAHUB_TOKEN
              valueFrom:
                secretKeyRef:
                  name: mcp-data-platform-secrets
                  key: datahub-token
            - name: OAUTH_SIGNING_KEY
              valueFrom:
                secretKeyRef:
                  name: mcp-data-platform-secrets
                  key: oauth-signing-key
            - name: KEYCLOAK_CLIENT_SECRET
              valueFrom:
                secretKeyRef:
                  name: mcp-data-platform-secrets
                  key: keycloak-client-secret
            - name: ENCRYPTION_KEY
              valueFrom:
                secretKeyRef:
                  name: mcp-data-platform-secrets
                  key: encryption-key
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: mcp-data-platform-secrets
                  key: database-url
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
            timeoutSeconds: 3
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 10
            periodSeconds: 30
            timeoutSeconds: 3
          resources:
            requests:
              cpu: 200m
              memory: 256Mi
            limits:
              cpu: 1000m
              memory: 1Gi
          volumeMounts:
            - name: config
              mountPath: /etc/mcp-data-platform
              readOnly: true
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: config
          configMap:
            name: mcp-data-platform-config
        - name: tmp
          emptyDir: {}
```

Capacity, briefly. A worker executes one run at a time, so concurrent runs equal
worker replicas: two replicas is enough for on-demand runs and a handful of
schedules, and the signal to add more is queue wait — runs sitting `pending`
while workers are busy — rather than CPU. Memory is the figure to set from
measurement: give a worker the largest result set its approved scripts hold plus
headroom, keep `GOMEMLIMIT` at about 90% of the limit, and remember that the
approval gate is what decides how large that can get.

The worker pods still expose `/healthz`, `/readyz`, and `/metrics`, which is what
the probes and the Prometheus scrape above use. Rolling them is safe at any
time: a draining worker stops claiming immediately, finishes the run it holds if
the grace period allows, and otherwise releases it back onto the queue for
another replica to pick up at once.

---

## Production Checklist

### Security

- [ ] TLS enabled for all external endpoints
- [ ] Secrets stored in external secrets manager (Vault, AWS Secrets Manager)
- [ ] Network policies restrict pod-to-pod communication
- [ ] Pod security context configured (non-root, read-only filesystem)
- [ ] Resource limits set for all containers
- [ ] OIDC configured with production identity provider
- [ ] API keys rotated regularly

### High Availability

- [ ] Multiple replicas deployed (minimum 2)
- [ ] PodDisruptionBudget configured
- [ ] Anti-affinity rules spread pods across nodes
- [ ] Health checks configured for liveness and readiness
- [ ] HPA configured for automatic scaling

### Monitoring

- [ ] Prometheus metrics enabled and scraped
- [ ] Grafana dashboards deployed
- [ ] Alerting rules configured
- [ ] Log aggregation set up (ELK, Loki)
- [ ] Distributed tracing enabled (Jaeger, Zipkin)

### Operations

- [ ] Backup strategy for PostgreSQL audit logs
- [ ] Disaster recovery plan documented
- [ ] Runbooks for common issues
- [ ] On-call rotation established

### MCP gateway (if enabled)

The [gateway toolkit](gateway.md) (kind `mcp`) has additional production
requirements:

- [ ] **`ENCRYPTION_KEY` is set** (32 bytes of key material; accepted
      as 64 hex characters, 44-character base64, or 32 raw bytes).
      Required for at-rest encryption of stored credentials, OAuth
      access and refresh tokens (`gateway_oauth_tokens`), and PKCE
      state (`oauth_pkce_states.code_verifier`). Without it the
      platform logs a warning and stores those values in plaintext —
      not acceptable in production.
- [ ] **PostgreSQL is reachable from every replica** and shared.
      Multi-replica deployments rely on the Postgres-backed PKCE state
      store so an `oauth-start` on replica A and the redirect callback
      on replica B can find each other. The platform automatically
      uses Postgres when `database.dsn` is set.
- [ ] **OAuth callback path** (`/api/v1/admin/oauth/callback`) is
      reachable on the public-facing URL of the platform. The upstream
      OAuth provider redirects the operator's browser here after
      sign-in; the path is intentionally public (state token
      authenticates the callback) and must be allowed through any
      reverse-proxy auth.
- [ ] **External Client App / OAuth client registration on each
      upstream** lists the platform's `/api/v1/admin/oauth/callback`
      URL as an allowed redirect URI. Required for `authorization_code`
      grants (e.g. Salesforce Hosted MCP).
- [ ] **`ENCRYPTION_KEY` rotation plan**. Rotating the key invalidates
      every encrypted value in `connection_instances`, `gateway_oauth_tokens`,
      and `oauth_pkce_states` — gateway connections will lose their
      stored credentials and authorization_code connections will need
      to be re-Connected through the portal. Plan accordingly.

---

## Upgrades and connected agents

The platform ships frequently, and each upgrade can change the tool contract (new
tools, new parameters, updated descriptions). How a connected agent picks up the new
contract depends on its client, because MCP delivers a changed tool list in-band only
on a live session; a binary upgrade is a new process, so the agent must reconnect to
re-handshake (`initialize` + `tools/list`) against the new build.

### What the server does on shutdown

On `SIGTERM` (a rolling deploy), the server:

1. Marks readiness draining so the load balancer stops routing new connections, then
   waits `server.shutdown.pre_shutdown_delay` for deregistration.
2. Drains in-flight HTTP requests, and after a short settle **closes live MCP
   sessions**. Long-lived SSE and streamable-HTTP streams never go idle on their own,
   so until the session is closed the agent stays on the old build. Closing it drops
   the stream so the client reconnects to a new pod and re-fetches the tool list. The
   close is graceful: an idle session drops immediately, a session with an in-flight
   tool call is allowed to finish, bounded by the grace period (after which process
   exit drops what remains).

Relevant settings under `server.shutdown` are `pre_shutdown_delay` and
`grace_period`; size them so the full sequence fits inside the pod's
`terminationGracePeriodSeconds`.

### Per-client behavior

| Client | On upgrade |
| --- | --- |
| **Claude Code** | Automatic. It honors `notifications/tools/list_changed` and auto-reconnects HTTP/SSE servers (exponential backoff). When the old session is closed it reconnects to the new build and re-fetches the tool list with no user action. |
| **Claude Desktop** | Requires a full app restart to pick up a changed tool list; it has no in-session refresh or reconnect action today. |
| **claude.ai managed web connector** | Caches the tool schema at the connector level; a connector re-sync (remove and re-add, or the workspace refresh) is needed to pick up changes. |

### Keep upgrades safe

Because a client may still be running on a cached contract briefly after a deploy,
keep tool/schema changes **additive**: adding a new optional parameter or a new tool
is safe (a cached client simply does not see it until it refreshes). Renaming or
removing a parameter, or removing a tool, breaks a client mid-session; deprecate
across a release before removing.

---

## Monitoring Setup

### Prometheus ServiceMonitor

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: mcp-data-platform
  namespace: mcp-data-platform
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: mcp-data-platform
  endpoints:
    - port: metrics
      interval: 30s
      path: /metrics
```

### Grafana Dashboard

Key metrics to monitor:

- **Tool-call rate**: `sum(rate(mcp_tool_calls_total[5m]))`
- **Error rate**: `sum(rate(mcp_tool_calls_total{status_category!="ok"}[5m])) / sum(rate(mcp_tool_calls_total[5m]))`
- **Latency**: `histogram_quantile(0.99, sum by (le) (rate(mcp_tool_call_duration_seconds_bucket[5m])))`
- **Enrichment overhead**: `rate(mcp_enrichment_bytes_total[5m]) / rate(mcp_tool_calls_total[5m])`
- **In-flight tool calls**: `mcp_inflight_tool_calls`
- **Dropped audit events**: `rate(audit_events_dropped_total[5m])`

Starter recording and alert rules covering these, as ConfigMaps that load
without the Prometheus Operator, ship in
[`deployments/observability/`](https://github.com/txn2/mcp-data-platform/tree/main/deployments/observability).
The full metric and label reference is in [Observability](observability.md).

---

## Scaling Considerations

### Horizontal Scaling

mcp-data-platform is stateless and scales horizontally. Key considerations:

- **Connection pooling**: Each replica maintains its own connections to DataHub/Trino
- **Cache coordination**: Semantic cache is per-instance; consider Redis for shared caching at scale
- **Load balancing**: Use sticky sessions for SSE connections

### Vertical Scaling

Increase resources for:

- **High query volume**: More CPU for request processing
- **Large result sets**: More memory for enrichment processing
- **Many concurrent connections**: More memory for connection state

### Bottleneck Analysis

Common bottlenecks and solutions:

| Bottleneck | Symptom | Solution |
|------------|---------|----------|
| DataHub API | High enrichment latency | Enable caching, increase DataHub resources |
| Trino queries | Timeout errors | Tune Trino cluster, add query limits |
| PostgreSQL audit | Write latency | Use async writes, add replicas |
| Network | Connection timeouts | Deploy closer to data sources |
