---
description: Diagnose and resolve common issues with mcp-data-platform. Includes error codes, debugging techniques, and step-by-step solutions.
---

# Troubleshooting

This guide helps you diagnose and resolve common issues with mcp-data-platform. If you don't find your issue here, check the [GitHub Issues](https://github.com/txn2/mcp-data-platform/issues) or open a new one.

---

## Quick Diagnosis

Start here to quickly identify your issue category.

| Symptom | Likely Cause | Jump To |
|---------|--------------|---------|
| Server exits immediately | Configuration error | [Server Won't Start](#server-wont-start) |
| 401 Unauthorized | Invalid credentials | [Authentication Issues](#authentication-issues) |
| 403 Forbidden | Persona/tool mismatch | [Persona Issues](#persona-issues) |
| No enrichment data | Injection misconfigured | [Enrichment Issues](#enrichment-issues) |
| Slow responses | Performance bottleneck | [Performance Issues](#performance-issues) |
| Connection refused | Service unreachable | [Connection Issues](#connection-issues) |

---

## Server Won't Start

### Symptom: Server exits immediately

**Check configuration syntax:**

```bash
# Validate YAML syntax
python3 -c "import yaml; yaml.safe_load(open('platform.yaml'))"

# Or with yq
yq eval platform.yaml
```

**Check for missing environment variables:**

```bash
# The server logs which variables are missing
mcp-data-platform --config platform.yaml 2>&1 | grep -i "missing\|undefined\|required"
```

**Common configuration errors:**

```yaml
# WRONG: Missing quotes around URL with special characters
toolkits:
  datahub:
    primary:
      url: https://datahub.example.com:8080  # Might be parsed incorrectly

# CORRECT: Quote URLs
toolkits:
  datahub:
    primary:
      url: "https://datahub.example.com:8080"
```

```yaml
# WRONG: Environment variable without braces
auth:
  api_keys:
    keys:
      - key: $API_KEY  # Won't expand

# CORRECT: Use ${} syntax
auth:
  api_keys:
    keys:
      - key: ${API_KEY}
```

### Symptom: Server hangs on startup

**Likely cause:** Cannot connect to a required service (DataHub, Trino, PostgreSQL).

**Debug steps:**

```bash
# Check DataHub connectivity
curl -v -H "Authorization: Bearer $DATAHUB_TOKEN" \
  https://datahub.example.com/openapi/v2/entity/dataset

# Check Trino connectivity
curl -v https://trino.example.com:443/v1/info

# Check PostgreSQL connectivity
psql $DATABASE_URL -c "SELECT 1"
```

### Symptom: Port already in use

```bash
# Find what's using the port
lsof -i :8080

# Kill the process if needed
kill -9 <PID>

# Or use a different port
mcp-data-platform --transport http --address :8081
```

---

## Connection Issues

### Cannot connect to Trino

**Symptom:** `trino_query` returns connection errors.

**Debug output example:**

```
Error: failed to connect to Trino: dial tcp: lookup trino.example.com: no such host
```

**Step 1: Verify network connectivity**

```bash
# DNS resolution
nslookup trino.example.com

# TCP connectivity
nc -zv trino.example.com 443

# HTTP connectivity
curl -v https://trino.example.com:443/v1/info
```

**Step 2: Check SSL configuration**

```yaml
toolkits:
  trino:
    primary:
      host: trino.example.com
      port: 443
      ssl: true
      ssl_verify: true  # Try false for self-signed certs

      # For custom CA certificates
      ssl_ca_file: /path/to/ca.crt
```

**Step 3: Verify credentials**

```bash
# Test with Trino CLI
trino --server https://trino.example.com:443 \
  --user $TRINO_USER \
  --password \
  --execute "SELECT 1"
```

### Cannot connect to DataHub

**Symptom:** `datahub_search` returns connection errors.

**Debug output example:**

```
Error: DataHub API error: 401 Unauthorized
```

**Step 1: Verify URL format**

```yaml
toolkits:
  datahub:
    primary:
      # CORRECT: GMS URL (metadata service)
      url: "https://datahub-gms.example.com"

      # WRONG: Frontend URL
      # url: "https://datahub.example.com"  # This is the UI
```

**Step 2: Test token validity**

```bash
# Test the token
curl -H "Authorization: Bearer $DATAHUB_TOKEN" \
  "https://datahub-gms.example.com/openapi/v2/entity/dataset?count=1"

# Check token expiration (if JWT)
echo $DATAHUB_TOKEN | cut -d. -f2 | base64 -d | jq '.exp | todate'
```

**Step 3: Verify token permissions**

DataHub tokens need these permissions:

- `Read Metadata` - Required for all operations
- `Manage Metadata` - Required for write operations (if enabled)

### Cannot connect to S3

**Symptom:** `s3_list_buckets` returns credential errors.

**Debug output example:**

```
Error: operation error S3: ListBuckets, https response error StatusCode: 403,
RequestID: ABC123, api error AccessDenied: Access Denied
```

**Step 1: Verify credentials**

```bash
# Test with AWS CLI
AWS_ACCESS_KEY_ID=$KEY AWS_SECRET_ACCESS_KEY=$SECRET \
  aws s3 ls --region us-east-1
```

**Step 2: Check endpoint configuration for MinIO/custom S3**

```yaml
toolkits:
  s3:
    minio:
      endpoint: "http://minio.local:9000"  # Include protocol
      use_path_style: true                  # Required for MinIO
      disable_ssl: true                     # For non-TLS endpoints
      region: "us-east-1"                   # Still required
```

**Step 3: Verify bucket permissions**

```bash
# Check bucket policy
aws s3api get-bucket-policy --bucket your-bucket

# Check IAM permissions
aws sts get-caller-identity
```

---

## Authentication Issues

### OIDC token rejected (401)

**Debug output example:**

```
Error: OIDC validation failed: token signature verification failed
```

**Step 1: Decode and inspect the token**

```bash
# Decode JWT payload
echo $TOKEN | cut -d. -f2 | base64 -d 2>/dev/null | jq

# Check key claims
echo $TOKEN | cut -d. -f2 | base64 -d 2>/dev/null | jq '{iss, aud, exp, sub}'
```

**Step 2: Verify issuer URL matches exactly**

```yaml
auth:
  oidc:
    # Must match token's "iss" claim EXACTLY (including trailing slash if present)
    issuer: "https://auth.example.com/realms/myrealm"

    # Common mistake: missing /realms/name for Keycloak
    # issuer: "https://auth.example.com"  # WRONG
```

**Step 3: Check token expiration and clock skew**

```yaml
auth:
  oidc:
    clock_skew_seconds: 60  # Allow 60 seconds of clock drift
```

```bash
# Check server time
date -u

# Check token expiration
echo $TOKEN | cut -d. -f2 | base64 -d 2>/dev/null | jq '.exp | todate'
```

**Step 4: Verify audience claim**

```yaml
auth:
  oidc:
    # Must match one of the token's "aud" claims
    audience: "mcp-data-platform"
```

### API key rejected (401)

**Debug output example:**

```
Error: API key validation failed: key not found
```

**Step 1: Check for invisible characters**

```bash
# Print key with visible whitespace
echo "Key: [$API_KEY_ADMIN]" | cat -A

# Remove any trailing whitespace
export API_KEY_ADMIN=$(echo "$API_KEY_ADMIN" | tr -d '[:space:]')
```

**Step 2: Verify configuration matches**

```yaml
auth:
  api_keys:
    enabled: true  # Must be enabled
    keys:
      - key: ${API_KEY_ADMIN}  # Variable name must match exactly
        name: "admin"
        roles: ["admin"]
```

**Step 3: Test the key directly**

```bash
# For HTTP transport
curl -H "Authorization: Bearer $API_KEY_ADMIN" \
  http://localhost:8080/health
```

### OAuth flow fails

**Debug output example:**

```
Error: OAuth callback failed: state mismatch
```

**Step 1: Check redirect URI configuration**

```yaml
oauth:
  clients:
    - id: "claude-desktop"
      redirect_uris:
        - "http://localhost"      # Claude Desktop uses these
        - "http://127.0.0.1"
        # Must match exactly what the client sends
```

**Step 2: Verify upstream IdP configuration**

```yaml
oauth:
  upstream:
    issuer: "https://keycloak.example.com/realms/your-realm"
    client_id: "mcp-data-platform"
    client_secret: ${KEYCLOAK_CLIENT_SECRET}

    # This must be registered as a valid redirect URI in Keycloak
    redirect_uri: "https://mcp.example.com/oauth/callback"
```

**Step 3: Check browser console for CORS errors**

If using a web-based client, check for CORS issues in the browser console.

---

## Persona Issues

### User gets wrong persona

**Debug steps:**

**Step 1: Check what roles are in the token**

```bash
# Decode token and show roles
echo $TOKEN | cut -d. -f2 | base64 -d 2>/dev/null | jq '.realm_access.roles'
```

**Step 2: Verify role_claim_path matches token structure**

```yaml
auth:
  oidc:
    # For Keycloak with realm roles
    role_claim_path: "realm_access.roles"

    # For Keycloak with client roles
    # role_claim_path: "resource_access.mcp-data-platform.roles"

    # For Auth0
    # role_claim_path: "https://example.com/roles"
```

**Step 3: Check role prefix filtering**

```yaml
auth:
  oidc:
    role_prefix: "dp_"  # Only roles starting with "dp_" are used
```

Token roles: `["dp_analyst", "user", "admin"]`
Filtered roles: `["analyst"]` (prefix stripped)

**Step 4: Verify persona role matching**

```yaml
personas:
  definitions:
    analyst:
      roles: ["analyst"]  # Must match filtered role exactly
```

### Tool denied unexpectedly (403)

**Debug output example:**

```
Error: tool access denied: trino_query not allowed for persona 'analyst'
```

**Step 1: Check allow/deny patterns**

```yaml
personas:
  definitions:
    analyst:
      tools:
        # Deny patterns are checked FIRST
        deny: ["trino_query"]  # This blocks trino_query
        allow: ["trino_*"]     # This would allow it, but deny wins
```

**Step 2: Verify wildcard pattern matching**

| Pattern | Matches | Doesn't Match |
|---------|---------|---------------|
| `trino_*` | `trino_query`, `trino_execute`, `trino_list_tables` | `datahub_search` |
| `*_delete_*` | `s3_delete_object`, `trino_delete_row` | `s3_list_buckets` |
| `*` | Everything | Nothing |

**Step 3: List available tools**

```bash
# Check what tools are registered
mcp-data-platform --config platform.yaml --list-tools
```

---

## Enrichment Issues

### No semantic context in Trino results

**Debug steps:**

**Step 1: Verify injection is enabled**

```yaml
injection:
  trino_semantic_enrichment: true  # Must be true
```

**Step 2: Check semantic provider is configured**

```yaml
semantic:
  provider: datahub
  instance: primary  # Must match a configured datahub toolkit
```

**Step 3: Verify table exists in DataHub**

```bash
# Search for the table in DataHub
curl -H "Authorization: Bearer $DATAHUB_TOKEN" \
  "https://datahub-gms.example.com/openapi/v2/search?query=your_table&entity=dataset"
```

**Step 4: Check URN format**

DataHub uses URNs to identify entities. The platform must construct the correct URN:

```
urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)
```

**Step 5: Check cache status**

```yaml
semantic:
  cache:
    enabled: true
    ttl: 5m  # Try 0 or disable to test without cache
```

### Enrichment errors in logs

**Debug output example:**

```
WARN  enrichment failed for table orders: DataHub entity not found
```

This is expected for tables that exist in Trino but aren't cataloged in DataHub. The platform returns the original result without enrichment.

To suppress these warnings:

```yaml
injection:
  suppress_enrichment_warnings: true
```

---

## Performance Issues

### Slow queries

**Step 1: Check query timeout configuration**

```yaml
toolkits:
  trino:
    primary:
      timeout: 300s  # Increase if needed
```

**Step 2: Verify row limits**

```yaml
toolkits:
  trino:
    primary:
      default_limit: 1000   # Default rows returned
      max_limit: 10000      # Maximum allowed
```

**Step 3: Profile the query in Trino**

```sql
EXPLAIN ANALYZE SELECT * FROM your_table LIMIT 1000;
```

**Step 4: Check network latency**

```bash
# Time a simple query
time curl -H "Authorization: Bearer $TOKEN" \
  "https://trino.example.com:443/v1/statement" \
  -d "SELECT 1"
```

### Slow enrichment

**Step 1: Enable and tune caching**

```yaml
semantic:
  cache:
    enabled: true
    ttl: 5m
    max_entries: 10000  # Increase for large catalogs
```

**Step 2: Check DataHub response time**

```bash
# Time a DataHub API call
time curl -H "Authorization: Bearer $DATAHUB_TOKEN" \
  "https://datahub-gms.example.com/openapi/v2/entity/dataset/urn:li:dataset:..."
```

**Step 3: Temporarily disable enrichment to isolate**

```yaml
injection:
  trino_semantic_enrichment: false  # Disable to test raw performance
```

### High memory usage

**Likely causes:**

1. Large query results being held in memory
2. Too many cached entries
3. Connection pool too large

**Solutions:**

```yaml
toolkits:
  trino:
    primary:
      max_limit: 10000  # Reduce maximum result size

semantic:
  cache:
    max_entries: 5000   # Reduce cache size
    ttl: 1m             # Shorter TTL

database:
  max_open_conns: 10    # Reduce connection pool
  max_idle_conns: 5
```

---

## Debugging Guide

### Enable verbose logging

```bash
# Set log level
export LOG_LEVEL=debug

# Run with verbose output
mcp-data-platform --config platform.yaml 2>&1 | tee debug.log
```

### Log format

```
2024-01-15T10:30:45.123Z INFO  server started address=:8080 transport=http
2024-01-15T10:30:46.456Z DEBUG auth middleware: validating token
2024-01-15T10:30:46.457Z DEBUG persona middleware: resolved persona=analyst
2024-01-15T10:30:46.458Z INFO  tool call tool=trino_query user=user@example.com persona=analyst
2024-01-15T10:30:46.789Z DEBUG enrichment: fetching semantic context table=orders
2024-01-15T10:30:47.012Z INFO  tool call complete tool=trino_query duration=554ms
```

### Request tracing

Each request has a unique ID for correlation:

```
X-Request-ID: a1b2c3d4-e5f6-7890-abcd-ef1234567890
```

Find all logs for a request:

```bash
grep "a1b2c3d4-e5f6-7890-abcd-ef1234567890" debug.log
```

### Audit log queries

If audit logging is enabled, query the database:

```sql
-- Recent tool calls
SELECT * FROM audit_logs
ORDER BY created_at DESC
LIMIT 100;

-- Failed requests
SELECT * FROM audit_logs
WHERE status = 'error'
ORDER BY created_at DESC;

-- Requests by user
SELECT tool_name, COUNT(*) as count
FROM audit_logs
WHERE user_id = 'user@example.com'
GROUP BY tool_name;
```

---

## Common Error Codes

| Code | Meaning | Solution |
|------|---------|----------|
| `AUTH_ERROR` | Authentication failed | Check credentials, token expiration |
| `AUTHZ_ERROR` | Authorization failed | Check persona tool rules |
| `TOOLKIT_ERROR` | Toolkit operation failed | Check service connectivity |
| `PROVIDER_ERROR` | Provider operation failed | Check DataHub/Trino config |
| `CONFIG_ERROR` | Configuration invalid | Validate YAML, check env vars |
| `TIMEOUT_ERROR` | Operation timed out | Increase timeout, check service |
| `RATE_LIMIT_ERROR` | Too many requests | Wait and retry, increase limits |

---

## Getting Help

1. **Search existing issues:** [GitHub Issues](https://github.com/txn2/mcp-data-platform/issues)

2. **Report a bug:** Include:
   - Platform version (`mcp-data-platform --version`)
   - Configuration (redact secrets)
   - Full error message
   - Steps to reproduce
   - Relevant logs

3. **Community support:** [GitHub Discussions](https://github.com/txn2/mcp-data-platform/discussions)

4. **Security issues:** Email security@txn2.com (do not open public issues for security vulnerabilities)
