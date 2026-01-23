---
description: Deploy mcp-data-platform in development and production environments using Docker Compose or Kubernetes/Helm.
---

# Deployment Guide

This guide covers deploying mcp-data-platform in various environments, from local development to production Kubernetes clusters.

---

## Deployment Options

| Environment | Best For | Complexity |
|-------------|----------|------------|
| **Docker Compose** | Development, small teams, testing | Low |
| **Kubernetes/Helm** | Production, multi-user, enterprise | Medium |

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
    command: ["--config", "/etc/mcp/platform.yaml", "--transport", "sse", "--address", ":8080"]
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
  transport: sse
  address: ":8080"

toolkits:
  datahub:
    primary:
      url: http://datahub-gms:8080
      token: ${DATAHUB_TOKEN}

  trino:
    primary:
      host: trino
      port: 8080
      user: trino
      catalog: memory
      ssl: false

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
  definitions:
    analyst:
      display_name: "Data Analyst"
      roles: ["analyst"]
      tools:
        allow: ["trino_*", "datahub_*"]
        deny: ["*_delete_*"]
    admin:
      display_name: "Administrator"
      roles: ["admin"]
      tools:
        allow: ["*"]
  default_persona: analyst

injection:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true

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
go run ./cmd/mcp-data-platform --config platform.yaml --transport sse --address :8080
```

---

## Kubernetes/Helm (Production)

Production deployment using Helm charts with best practices for security, scaling, and monitoring.

### Prerequisites

- Kubernetes 1.28+
- Helm 3.12+
- kubectl configured for your cluster
- TLS certificates (cert-manager recommended)

### Helm Chart Structure

Create a Helm chart at `charts/mcp-data-platform/`:

```
charts/mcp-data-platform/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── _helpers.tpl
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── configmap.yaml
│   ├── secret.yaml
│   ├── ingress.yaml
│   ├── hpa.yaml
│   ├── pdb.yaml
│   └── serviceaccount.yaml
```

### Chart.yaml

```yaml
apiVersion: v2
name: mcp-data-platform
description: Semantic data platform MCP server
type: application
version: 1.0.0
appVersion: "0.1.0"
```

### values.yaml

```yaml
replicaCount: 2

image:
  repository: ghcr.io/txn2/mcp-data-platform
  pullPolicy: IfNotPresent
  tag: "latest"

serviceAccount:
  create: true
  annotations: {}
  name: ""

podSecurityContext:
  runAsNonRoot: true
  runAsUser: 65534
  runAsGroup: 65534
  fsGroup: 65534

securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop:
      - ALL

service:
  type: ClusterIP
  port: 8080

ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/proxy-body-size: "10m"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "300"
  hosts:
    - host: mcp.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: mcp-data-platform-tls
      hosts:
        - mcp.example.com

resources:
  limits:
    cpu: 1000m
    memory: 512Mi
  requests:
    cpu: 100m
    memory: 128Mi

autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 70
  targetMemoryUtilizationPercentage: 80

pdb:
  enabled: true
  minAvailable: 1

# Platform configuration
config:
  server:
    name: mcp-data-platform
    transport: sse
    address: ":8080"
    tls:
      enabled: false  # TLS terminates at ingress

  toolkits:
    datahub:
      primary:
        url: http://datahub-gms.datahub:8080
    trino:
      primary:
        host: trino.trino
        port: 8080
        user: mcp-platform
        catalog: hive
        ssl: false

  injection:
    trino_semantic_enrichment: true
    datahub_query_enrichment: true

  audit:
    enabled: true
    log_tool_calls: true

# External secrets (use external-secrets operator or sealed-secrets in production)
secrets:
  datahubToken: ""
  oauthSigningKey: ""
  keycloakClientSecret: ""
  databaseUrl: ""

# Prometheus metrics
metrics:
  enabled: true
  port: 9090
  path: /metrics

# Health checks
probes:
  liveness:
    httpGet:
      path: /health
      port: http
    initialDelaySeconds: 10
    periodSeconds: 10
  readiness:
    httpGet:
      path: /health
      port: http
    initialDelaySeconds: 5
    periodSeconds: 5
```

### templates/deployment.yaml

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "mcp-data-platform.fullname" . }}
  labels:
    {{- include "mcp-data-platform.labels" . | nindent 4 }}
spec:
  {{- if not .Values.autoscaling.enabled }}
  replicas: {{ .Values.replicaCount }}
  {{- end }}
  selector:
    matchLabels:
      {{- include "mcp-data-platform.selectorLabels" . | nindent 6 }}
  template:
    metadata:
      annotations:
        checksum/config: {{ include (print $.Template.BasePath "/configmap.yaml") . | sha256sum }}
        checksum/secret: {{ include (print $.Template.BasePath "/secret.yaml") . | sha256sum }}
      labels:
        {{- include "mcp-data-platform.selectorLabels" . | nindent 8 }}
    spec:
      serviceAccountName: {{ include "mcp-data-platform.serviceAccountName" . }}
      securityContext:
        {{- toYaml .Values.podSecurityContext | nindent 8 }}
      containers:
        - name: {{ .Chart.Name }}
          securityContext:
            {{- toYaml .Values.securityContext | nindent 12 }}
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}"
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          args:
            - --config
            - /etc/mcp/platform.yaml
            - --transport
            - sse
            - --address
            - :8080
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
            {{- if .Values.metrics.enabled }}
            - name: metrics
              containerPort: {{ .Values.metrics.port }}
              protocol: TCP
            {{- end }}
          livenessProbe:
            {{- toYaml .Values.probes.liveness | nindent 12 }}
          readinessProbe:
            {{- toYaml .Values.probes.readiness | nindent 12 }}
          resources:
            {{- toYaml .Values.resources | nindent 12 }}
          env:
            - name: DATAHUB_TOKEN
              valueFrom:
                secretKeyRef:
                  name: {{ include "mcp-data-platform.fullname" . }}
                  key: datahub-token
            - name: OAUTH_SIGNING_KEY
              valueFrom:
                secretKeyRef:
                  name: {{ include "mcp-data-platform.fullname" . }}
                  key: oauth-signing-key
            - name: KEYCLOAK_CLIENT_SECRET
              valueFrom:
                secretKeyRef:
                  name: {{ include "mcp-data-platform.fullname" . }}
                  key: keycloak-client-secret
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: {{ include "mcp-data-platform.fullname" . }}
                  key: database-url
          volumeMounts:
            - name: config
              mountPath: /etc/mcp
              readOnly: true
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: config
          configMap:
            name: {{ include "mcp-data-platform.fullname" . }}
        - name: tmp
          emptyDir: {}
```

### templates/hpa.yaml

```yaml
{{- if .Values.autoscaling.enabled }}
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: {{ include "mcp-data-platform.fullname" . }}
  labels:
    {{- include "mcp-data-platform.labels" . | nindent 4 }}
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: {{ include "mcp-data-platform.fullname" . }}
  minReplicas: {{ .Values.autoscaling.minReplicas }}
  maxReplicas: {{ .Values.autoscaling.maxReplicas }}
  metrics:
    {{- if .Values.autoscaling.targetCPUUtilizationPercentage }}
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: {{ .Values.autoscaling.targetCPUUtilizationPercentage }}
    {{- end }}
    {{- if .Values.autoscaling.targetMemoryUtilizationPercentage }}
    - type: Resource
      resource:
        name: memory
        target:
          type: Utilization
          averageUtilization: {{ .Values.autoscaling.targetMemoryUtilizationPercentage }}
    {{- end }}
{{- end }}
```

### templates/pdb.yaml

```yaml
{{- if .Values.pdb.enabled }}
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ include "mcp-data-platform.fullname" . }}
  labels:
    {{- include "mcp-data-platform.labels" . | nindent 4 }}
spec:
  minAvailable: {{ .Values.pdb.minAvailable }}
  selector:
    matchLabels:
      {{- include "mcp-data-platform.selectorLabels" . | nindent 6 }}
{{- end }}
```

### Deploy to Kubernetes

```bash
# Create namespace
kubectl create namespace mcp-data-platform

# Create secrets (use external-secrets or sealed-secrets in production)
kubectl create secret generic mcp-data-platform-secrets \
  --namespace mcp-data-platform \
  --from-literal=datahub-token="$DATAHUB_TOKEN" \
  --from-literal=oauth-signing-key="$OAUTH_SIGNING_KEY" \
  --from-literal=keycloak-client-secret="$KEYCLOAK_CLIENT_SECRET" \
  --from-literal=database-url="$DATABASE_URL"

# Install the chart
helm upgrade --install mcp-data-platform ./charts/mcp-data-platform \
  --namespace mcp-data-platform \
  --values values-production.yaml

# Verify deployment
kubectl get pods -n mcp-data-platform
kubectl get hpa -n mcp-data-platform
```

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

- **Request rate**: `sum(rate(mcp_requests_total[5m]))`
- **Error rate**: `sum(rate(mcp_requests_total{status="error"}[5m]))`
- **Latency**: `histogram_quantile(0.99, rate(mcp_request_duration_seconds_bucket[5m]))`
- **Enrichment latency**: `histogram_quantile(0.99, rate(mcp_enrichment_duration_seconds_bucket[5m]))`
- **Active connections**: `mcp_active_connections`

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
