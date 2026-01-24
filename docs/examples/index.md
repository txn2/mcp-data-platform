---
description: Real-world configuration examples for enterprise data governance, data democratization, and AI/ML workflows.
---

# Examples Gallery

Practical configurations and patterns for common use cases. Each example includes complete YAML configurations and explanations of the design decisions.

---

## Enterprise Data Governance

### Compliance-Ready Audit Configuration

Full audit logging for regulatory compliance (SOC 2, HIPAA, GDPR).

```yaml
server:
  name: mcp-data-platform
  transport: sse
  address: ":8443"
  tls:
    enabled: true
    cert_file: /etc/tls/server.crt
    key_file: /etc/tls/server.key

auth:
  allow_anonymous: false
  oidc:
    enabled: true
    issuer: "https://auth.example.com/realms/enterprise"
    client_id: "mcp-data-platform"
    audience: "mcp-data-platform"
    role_claim_path: "realm_access.roles"
    required_claims:
      - sub
      - exp
      - email  # Required for audit attribution

audit:
  enabled: true
  log_tool_calls: true
  log_parameters: true      # Log query parameters (redact PII)
  log_results: false        # Don't log result data
  retention_days: 2555      # 7 years for compliance

  # Async write to avoid blocking requests
  async_writes: true
  buffer_size: 1000
  flush_interval: 5s

database:
  dsn: ${DATABASE_URL}
  max_open_conns: 25
  max_idle_conns: 5
  conn_max_lifetime: 5m
```

**Key decisions:**

- TLS required for all connections
- Email claim required for audit attribution
- Parameters logged (for query reconstruction) but results not logged (privacy)
- 7-year retention matches common compliance requirements
- Async writes prevent audit logging from blocking user requests

### PII Detection and Acknowledgment

Configure the platform to surface PII warnings prominently.

```yaml
toolkits:
  datahub:
    primary:
      url: https://datahub.example.com
      token: ${DATAHUB_TOKEN}

  trino:
    primary:
      host: trino.example.com
      port: 443
      ssl: true
      read_only: true  # No write operations

injection:
  trino_semantic_enrichment: true

  # PII handling configuration
  enrichment:
    pii_tags:
      - pii
      - pii-email
      - pii-phone
      - pii-ssn
      - gdpr-personal-data

    # Require acknowledgment for PII queries
    pii_acknowledgment:
      enabled: true
      message: |
        This dataset contains PII. By proceeding, you acknowledge:
        - Data will be handled per company privacy policy
        - Access is logged for compliance
        - Data must not be exported without approval

personas:
  definitions:
    analyst:
      display_name: "Data Analyst"
      roles: ["analyst"]
      tools:
        allow: ["trino_query", "trino_describe_*", "datahub_*"]
        deny: ["*_delete_*", "*_drop_*"]

      # Additional PII handling
      restrictions:
        max_rows: 10000
        allowed_schemas:
          - analytics
          - aggregated
        denied_tags:
          - restricted
          - executive-only
```

### Role-Based Access with Keycloak

Complete Keycloak integration with multiple persona tiers.

```yaml
auth:
  oidc:
    enabled: true
    issuer: "https://keycloak.example.com/realms/data-platform"
    client_id: "mcp-data-platform"
    audience: "mcp-data-platform"

    # Keycloak-specific configuration
    role_claim_path: "realm_access.roles"
    role_prefix: "dp_"  # Only roles starting with dp_ are considered

    # Security settings
    clock_skew_seconds: 30
    max_token_age: 8h
    refresh_enabled: true

personas:
  definitions:
    # Tier 1: Read-only analysts
    viewer:
      display_name: "Data Viewer"
      roles: ["dp_viewer"]
      tools:
        allow: ["datahub_search", "datahub_get_*"]
        deny: ["*"]

    # Tier 2: Query-capable analysts
    analyst:
      display_name: "Data Analyst"
      roles: ["dp_analyst"]
      tools:
        allow: ["trino_query", "trino_list_*", "trino_describe_*", "datahub_*"]
        deny: ["*_delete_*", "*_drop_*", "*_put_*"]

    # Tier 3: Data engineers with write access
    engineer:
      display_name: "Data Engineer"
      roles: ["dp_engineer"]
      tools:
        allow: ["trino_*", "datahub_*", "s3_*"]
        deny: ["*_delete_*"]

    # Tier 4: Administrators
    admin:
      display_name: "Administrator"
      roles: ["dp_admin"]
      tools:
        allow: ["*"]

  # Mapping for legacy role names
  role_mapping:
    oidc_to_persona:
      "legacy_readonly": "viewer"
      "legacy_analyst": "analyst"

  default_persona: viewer
```

### Read-Only Mode Enforcement

Lock down production data access to read-only operations.

```yaml
toolkits:
  trino:
    production:
      host: trino-prod.example.com
      port: 443
      ssl: true
      ssl_verify: true

      # Enforce read-only at the toolkit level
      read_only: true

      # Additional query restrictions
      default_limit: 1000
      max_limit: 50000
      timeout: 300s

      # Blocked SQL patterns (defense in depth)
      blocked_patterns:
        - "INSERT"
        - "UPDATE"
        - "DELETE"
        - "DROP"
        - "CREATE"
        - "ALTER"
        - "TRUNCATE"

  s3:
    data_lake:
      region: us-east-1

      # Read-only S3 access
      read_only: true

      # Restrict to specific buckets
      allowed_buckets:
        - data-lake-prod
        - analytics-exports
```

---

## Data Democratization

### Self-Service Setup for Business Analysts

Configuration optimized for business users exploring data through AI.

```yaml
server:
  name: analytics-assistant
  transport: sse
  address: ":8080"

toolkits:
  datahub:
    primary:
      url: https://datahub.example.com
      token: ${DATAHUB_TOKEN}

      # Higher limits for exploration
      default_limit: 25
      max_limit: 100

  trino:
    analytics:
      host: trino-analytics.example.com
      port: 443
      ssl: true
      catalog: analytics
      schema: curated

      # Reasonable limits for interactive use
      default_limit: 100
      max_limit: 10000
      timeout: 60s
      read_only: true

# Enable all enrichment for maximum context
injection:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true

semantic:
  provider: datahub
  instance: primary

  # Aggressive caching for faster exploration
  cache:
    enabled: true
    ttl: 15m
    max_entries: 10000

personas:
  definitions:
    business_analyst:
      display_name: "Business Analyst"
      roles: ["analyst", "business"]
      tools:
        allow:
          - "datahub_search"
          - "datahub_get_entity"
          - "datahub_get_schema"
          - "datahub_get_lineage"
          - "datahub_get_glossary_term"
          - "datahub_list_domains"
          - "trino_query"
          - "trino_list_*"
          - "trino_describe_*"
        deny:
          - "*_delete_*"
          - "*_drop_*"

      # User-friendly system prompt
      prompts:
        system_prefix: |
          You are helping a business analyst explore and understand data.
          Always explain what tables contain in business terms.
          When showing query results, explain what the data means.
          If data quality is below 80%, mention this to the user.
          If a table is deprecated, always suggest the replacement.

  default_persona: business_analyst
```

### Cross-Team Data Discovery

Multi-Trino cluster setup for organization-wide data discovery.

```yaml
toolkits:
  datahub:
    # Central metadata catalog
    central:
      url: https://datahub.example.com
      token: ${DATAHUB_TOKEN}

  trino:
    # Marketing team's cluster
    marketing:
      host: trino-marketing.example.com
      port: 443
      ssl: true
      catalog: marketing
      default_limit: 1000
      read_only: true

    # Sales team's cluster
    sales:
      host: trino-sales.example.com
      port: 443
      ssl: true
      catalog: sales
      default_limit: 1000
      read_only: true

    # Finance team's cluster (restricted)
    finance:
      host: trino-finance.example.com
      port: 443
      ssl: true
      catalog: finance
      default_limit: 500
      read_only: true

# Cross-injection from central DataHub to all Trino clusters
injection:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true

semantic:
  provider: datahub
  instance: central

# Query provider maps DataHub URNs to correct Trino cluster
query:
  provider: trino
  mappings:
    "urn:li:dataset:(urn:li:dataPlatform:trino,marketing.*,PROD)": marketing
    "urn:li:dataset:(urn:li:dataPlatform:trino,sales.*,PROD)": sales
    "urn:li:dataset:(urn:li:dataPlatform:trino,finance.*,PROD)": finance

personas:
  definitions:
    cross_team_analyst:
      display_name: "Cross-Team Analyst"
      roles: ["cross_team"]
      tools:
        allow:
          - "datahub_*"
          - "trino_query:marketing"      # Explicit cluster access
          - "trino_query:sales"
          - "trino_list_*"
          - "trino_describe_*"
        deny:
          - "trino_query:finance"        # No finance access
          - "*_delete_*"

    finance_analyst:
      display_name: "Finance Analyst"
      roles: ["finance"]
      tools:
        allow:
          - "datahub_*"
          - "trino_*"                    # All clusters including finance
        deny:
          - "*_delete_*"
```

### New Employee Onboarding Workflow

Configuration with helpful prompts for new team members.

```yaml
personas:
  definitions:
    new_hire:
      display_name: "New Team Member"
      roles: ["new_hire", "onboarding"]
      tools:
        allow:
          - "datahub_search"
          - "datahub_get_entity"
          - "datahub_get_schema"
          - "datahub_get_lineage"
          - "datahub_get_glossary_term"
          - "datahub_list_domains"
          - "datahub_list_tags"
          - "trino_list_*"
          - "trino_describe_*"
          # No direct query access yet
        deny:
          - "trino_query"
          - "*_delete_*"

      prompts:
        system_prefix: |
          You are onboarding a new team member to our data platform.

          When they ask about data:
          1. Start with the domain (Sales, Marketing, Finance, etc.)
          2. Explain what the domain contains
          3. Show key tables and their purposes
          4. Point out data owners they can contact
          5. Highlight any data quality concerns

          Always recommend they review the glossary terms for unfamiliar concepts.
          If they want to query data, explain they need to complete onboarding first.

        onboarding_resources: |
          Useful resources for new team members:
          - Data Glossary: /glossary
          - Domain Owners: /domains
          - Data Quality Dashboard: /quality
          - Request Access: /access-request
```

---

## AI/ML Workflows

### AI Agent Exploring Unfamiliar Datasets

Configuration for autonomous AI agents discovering data.

```yaml
server:
  name: ml-data-explorer
  transport: sse
  address: ":8080"

toolkits:
  datahub:
    primary:
      url: https://datahub.example.com
      token: ${DATAHUB_TOKEN}
      default_limit: 50  # More results for exploration

  trino:
    ml_cluster:
      host: trino-ml.example.com
      port: 443
      ssl: true
      catalog: feature_store
      read_only: true
      default_limit: 100
      max_limit: 10000

injection:
  trino_semantic_enrichment: true
  datahub_query_enrichment: true

# Full lineage depth for understanding data provenance
semantic:
  provider: datahub
  instance: primary
  lineage:
    max_depth: 5
    include_column_lineage: true

personas:
  definitions:
    ml_agent:
      display_name: "ML Data Agent"
      roles: ["ml_agent", "automated"]
      tools:
        allow:
          - "datahub_search"
          - "datahub_get_entity"
          - "datahub_get_schema"
          - "datahub_get_lineage"
          - "datahub_get_queries"        # See popular queries
          - "datahub_list_data_products"
          - "trino_query"
          - "trino_list_*"
          - "trino_describe_*"
          - "trino_explain"              # Query planning
        deny:
          - "*_delete_*"
          - "*_put_*"

      # Rate limiting for automated access
      rate_limit:
        requests_per_minute: 60
        requests_per_hour: 1000

      prompts:
        system_prefix: |
          You are an ML data exploration agent. Your goal is to discover
          and evaluate datasets for machine learning use cases.

          When exploring:
          1. Check data quality scores (reject < 70%)
          2. Verify data freshness (check last_updated)
          3. Trace lineage to understand transformations
          4. Look for feature-ready columns (numeric, categorical)
          5. Note any PII tags that require handling

          Always document your findings with URNs for reproducibility.
```

### Feature Store Integration

Connecting to a feature store with quality gates.

```yaml
toolkits:
  trino:
    feature_store:
      host: trino-features.example.com
      port: 443
      ssl: true
      catalog: feature_store
      schema: production
      read_only: true

injection:
  trino_semantic_enrichment: true

  # Quality gates for feature selection
  quality_gates:
    enabled: true
    minimum_quality_score: 0.75
    maximum_null_percentage: 10
    require_documentation: true

    # Warn but don't block
    soft_gates:
      - name: freshness
        max_age_days: 7
        message: "Feature data is over 7 days old"
      - name: coverage
        min_coverage: 0.90
        message: "Feature coverage is below 90%"

    # Block features that don't meet criteria
    hard_gates:
      - name: deprecated
        message: "Cannot use deprecated features"
      - name: pii
        message: "PII features require explicit approval"

personas:
  definitions:
    ml_engineer:
      display_name: "ML Engineer"
      roles: ["ml_engineer"]
      tools:
        allow:
          - "datahub_*"
          - "trino_*"
        deny:
          - "*_delete_*"

      prompts:
        system_prefix: |
          You are helping an ML engineer select features from the feature store.

          For each feature, report:
          - Quality score and null percentage
          - Last update time
          - Upstream dependencies (lineage)
          - Any quality gate warnings

          Reject features that fail hard quality gates.
          Flag features with soft gate warnings but allow their use.
```

### Pipeline Lineage Exploration

Configuration for understanding data pipeline provenance.

```yaml
semantic:
  provider: datahub
  instance: primary

  lineage:
    max_depth: 10                    # Deep lineage traversal
    include_column_lineage: true     # Column-level lineage
    include_process_lineage: true    # Show transformation steps

  cache:
    enabled: true
    ttl: 5m
    # Separate cache for lineage queries
    lineage_ttl: 1m  # Shorter TTL for lineage

injection:
  trino_semantic_enrichment: true

  # Include full lineage in enrichment
  enrichment:
    include_lineage: true
    lineage_depth: 3
    lineage_direction: both

personas:
  definitions:
    data_engineer:
      display_name: "Data Engineer"
      roles: ["data_engineer"]
      tools:
        allow:
          - "datahub_get_lineage"
          - "datahub_get_entity"
          - "datahub_search"
          - "trino_*"
        deny:
          - "*_delete_*"

      prompts:
        system_prefix: |
          You are helping a data engineer understand data lineage.

          When showing lineage:
          1. Start with the requested entity
          2. Show immediate upstream sources
          3. Show immediate downstream consumers
          4. Highlight any transformation steps
          5. Note data quality changes through the pipeline

          Use URNs consistently for reference.
```

---

## Integration Patterns

### Multi-Provider Configuration

Connect multiple instances of each service type.

```yaml
toolkits:
  datahub:
    # Production DataHub
    production:
      url: https://datahub.example.com
      token: ${DATAHUB_TOKEN_PROD}

    # Development DataHub
    development:
      url: https://datahub-dev.example.com
      token: ${DATAHUB_TOKEN_DEV}

  trino:
    # Production Trino (read-only)
    production:
      host: trino.example.com
      port: 443
      ssl: true
      read_only: true

    # Development Trino (read-write)
    development:
      host: trino-dev.example.com
      port: 8080
      ssl: false
      read_only: false

    # Analytics Trino
    analytics:
      host: trino-analytics.example.com
      port: 443
      ssl: true
      read_only: true

  s3:
    # AWS S3
    aws:
      region: us-east-1
      access_key_id: ${AWS_ACCESS_KEY_ID}
      secret_access_key: ${AWS_SECRET_ACCESS_KEY}

    # MinIO (on-prem)
    minio:
      endpoint: http://minio.local:9000
      use_path_style: true
      disable_ssl: true
      access_key_id: ${MINIO_ACCESS_KEY}
      secret_access_key: ${MINIO_SECRET_KEY}

# Configure which instances to use for enrichment
semantic:
  provider: datahub
  instance: production  # Use production DataHub for metadata

query:
  provider: trino
  instance: production  # Use production Trino for availability checks

storage:
  provider: s3
  instance: aws  # Use AWS S3 for storage checks
```

### Custom Toolkit Development

Example structure for adding a custom toolkit.

```go
package custom

import (
    "context"

    "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/txn2/mcp-data-platform/pkg/semantic"
    "github.com/txn2/mcp-data-platform/pkg/query"
)

// Toolkit implements the registry.Toolkit interface
type Toolkit struct {
    name             string
    config           Config
    semanticProvider semantic.Provider
    queryProvider    query.Provider
}

func (t *Toolkit) Kind() string { return "custom" }
func (t *Toolkit) Name() string { return t.name }

func (t *Toolkit) Tools() []string {
    return []string{
        "custom_operation_one",
        "custom_operation_two",
    }
}

func (t *Toolkit) RegisterTools(s *mcp.Server) {
    s.AddTool(mcp.Tool{
        Name:        "custom_operation_one",
        Description: "Perform custom operation one",
        InputSchema: mcp.ToolInputSchema{
            Type: "object",
            Properties: map[string]any{
                "input": map[string]any{
                    "type":        "string",
                    "description": "Input value",
                },
            },
            Required: []string{"input"},
        },
    }, t.handleOperationOne)
}

func (t *Toolkit) SetSemanticProvider(p semantic.Provider) {
    t.semanticProvider = p
}

func (t *Toolkit) SetQueryProvider(p query.Provider) {
    t.queryProvider = p
}

func (t *Toolkit) Close() error {
    return nil
}
```

Register the custom toolkit in configuration:

```yaml
toolkits:
  custom:
    my_custom:
      # Custom toolkit configuration
      api_endpoint: https://custom-api.example.com
      api_key: ${CUSTOM_API_KEY}
```

---

## Next Steps

- [Configuration Reference](../reference/configuration.md) - Full configuration schema
- [Tools API](../reference/tools-api.md) - Complete tool documentation
- [Troubleshooting](../support/troubleshooting.md) - Common issues and solutions
