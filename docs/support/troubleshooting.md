# Troubleshooting

Common issues and their solutions when using mcp-data-platform.

## Connection Issues

### Server doesn't start

**Symptom:** Server exits immediately or hangs.

**Possible causes:**

1. **Invalid configuration file:**
   ```bash
   # Validate YAML syntax
   python3 -c "import yaml; yaml.safe_load(open('platform.yaml'))"
   ```

2. **Missing environment variables:**
   ```bash
   # Check required variables are set
   echo $TRINO_HOST
   echo $DATAHUB_TOKEN
   ```

3. **Port already in use (SSE transport):**
   ```bash
   # Check what's using the port
   lsof -i :8080
   ```

### Cannot connect to Trino

**Symptom:** `trino_query` returns connection errors.

**Check:**

1. **Network connectivity:**
   ```bash
   curl -v https://trino.example.com:443/v1/info
   ```

2. **SSL configuration:**
   ```yaml
   toolkits:
     trino:
       primary:
         ssl: true
         ssl_verify: true  # Try false for self-signed certs
   ```

3. **Credentials:**
   - Verify username and password
   - Check if password auth is enabled on Trino

### Cannot connect to DataHub

**Symptom:** `datahub_search` returns connection errors.

**Check:**

1. **URL format:**
   ```yaml
   toolkits:
     datahub:
       primary:
         url: "https://datahub.example.com"  # GMS URL, not frontend
   ```

2. **Token validity:**
   ```bash
   curl -H "Authorization: Bearer $DATAHUB_TOKEN" \
     https://datahub.example.com/openapi/v2/entity/dataset
   ```

3. **Token permissions:**
   - Ensure token has read access to datasets

### Cannot connect to S3

**Symptom:** `s3_list_buckets` returns credential or permission errors.

**Check:**

1. **Credentials:**
   ```bash
   # Test with AWS CLI
   AWS_ACCESS_KEY_ID=$KEY AWS_SECRET_ACCESS_KEY=$SECRET aws s3 ls
   ```

2. **Region configuration:**
   ```yaml
   toolkits:
     s3:
       primary:
         region: "us-east-1"  # Must match bucket region
   ```

3. **Custom endpoint (MinIO, etc.):**
   ```yaml
   toolkits:
     s3:
       primary:
         endpoint: "http://minio.local:9000"
         use_path_style: true
         disable_ssl: true
   ```

---

## Authentication Issues

### OIDC token rejected

**Symptom:** 401 Unauthorized with OIDC tokens.

**Check:**

1. **Issuer URL:**
   ```yaml
   auth:
     oidc:
       issuer: "https://auth.example.com/realms/myrealm"  # Exact match required
   ```

2. **Token expiration:**
   - Tokens are time-sensitive
   - Check clock synchronization

3. **Audience claim:**
   ```yaml
   auth:
     oidc:
       audience: "mcp-data-platform"  # Must match token's aud claim
   ```

4. **Decode token to inspect:**
   ```bash
   # Paste token at jwt.io or:
   echo $TOKEN | cut -d. -f2 | base64 -d | jq
   ```

### API key rejected

**Symptom:** 401 Unauthorized with API keys.

**Check:**

1. **Key matches exactly:**
   - No leading/trailing whitespace
   - Case-sensitive

2. **Environment variable set:**
   ```bash
   echo "Key: [$API_KEY_ADMIN]"  # Check for invisible characters
   ```

3. **Configuration uses correct variable:**
   ```yaml
   auth:
     api_keys:
       keys:
         - key: ${API_KEY_ADMIN}  # Variable name must match
   ```

### Roles not extracted from token

**Symptom:** User gets default persona instead of expected one.

**Check:**

1. **role_claim_path matches token structure:**
   ```yaml
   auth:
     oidc:
       role_claim_path: "realm_access.roles"  # Path to roles in token
   ```

2. **Token actually contains roles:**
   ```json
   {
     "realm_access": {
       "roles": ["dp_analyst", "dp_admin"]
     }
   }
   ```

3. **role_prefix filters correctly:**
   ```yaml
   auth:
     oidc:
       role_prefix: "dp_"  # Only roles starting with dp_ are used
   ```

---

## Persona Issues

### User gets wrong persona

**Check:**

1. **Role matching:**
   - Verify user's roles match persona definition
   - Check priority ordering

2. **Role mapping configuration:**
   ```yaml
   personas:
     role_mapping:
       oidc_to_persona:
         "my_role": "analyst"  # Explicit mapping
   ```

3. **Default persona:**
   ```yaml
   personas:
     default_persona: viewer  # Used when no roles match
   ```

### Tool denied unexpectedly

**Check:**

1. **Deny takes precedence over allow:**
   ```yaml
   tools:
     allow: ["trino_*"]
     deny: ["trino_query"]  # This wins
   ```

2. **Wildcard patterns:**
   - `*` matches everything
   - `trino_*` matches trino_query, trino_explain, etc.

3. **Exact tool names:**
   ```bash
   # List available tools
   # Each tool must match an allow pattern
   ```

---

## Enrichment Issues

### No semantic context in Trino results

**Check:**

1. **Injection enabled:**
   ```yaml
   injection:
     trino_semantic_enrichment: true
   ```

2. **Semantic provider configured:**
   ```yaml
   semantic:
     provider: datahub
     instance: primary
   ```

3. **Table exists in DataHub:**
   - Search DataHub for the table
   - Check URN format matches

4. **Cache stale:**
   - Disable cache or reduce TTL to test

### No query context in DataHub results

**Check:**

1. **Injection enabled:**
   ```yaml
   injection:
     datahub_query_enrichment: true
   ```

2. **Query provider configured:**
   ```yaml
   query:
     provider: trino
     instance: primary
   ```

3. **Table exists in Trino:**
   - Try `trino_list_tables` to verify

---

## Performance Issues

### Slow queries

**Check:**

1. **Query timeout:**
   ```yaml
   toolkits:
     trino:
       primary:
         timeout: 300s  # Increase if needed
   ```

2. **Row limits:**
   ```yaml
   toolkits:
     trino:
       primary:
         default_limit: 1000
         max_limit: 10000
   ```

3. **Network latency:**
   - Check connectivity to Trino
   - Consider deploying platform closer to data

### Slow enrichment

**Check:**

1. **Enable caching:**
   ```yaml
   semantic:
     cache:
       enabled: true
       ttl: 5m
   ```

2. **DataHub response time:**
   - Test DataHub API directly
   - Check DataHub health

3. **Disable enrichment temporarily:**
   ```yaml
   injection:
     trino_semantic_enrichment: false
   ```

---

## Logging and Debugging

### Enable verbose logging

Set environment variable:
```bash
export LOG_LEVEL=debug
```

### Enable audit logging

```yaml
audit:
  enabled: true
  log_tool_calls: true
```

### Check platform status

For SSE transport:
```bash
curl http://localhost:8080/health
```

### Validate configuration

```bash
mcp-data-platform --config platform.yaml --validate
```

---

## Common Error Messages

| Error | Cause | Solution |
|-------|-------|----------|
| `connection refused` | Service not reachable | Check network, firewall, service status |
| `certificate verify failed` | SSL certificate issue | Use `ssl_verify: false` or fix certificate |
| `401 Unauthorized` | Invalid credentials | Check token/key and configuration |
| `403 Forbidden` | Tool not allowed for persona | Check persona tool rules |
| `timeout` | Query took too long | Increase timeout or optimize query |
| `rate limit exceeded` | Too many requests | Wait and retry, or increase limits |
| `table not found` | Table doesn't exist | Verify table name and catalog/schema |

---

## Getting Help

1. **Check the documentation:** Review relevant sections for your issue.

2. **Search existing issues:** [GitHub Issues](https://github.com/txn2/mcp-data-platform/issues)

3. **Report a bug:** Open an issue with:
   - Platform version
   - Configuration (redact secrets)
   - Error message
   - Steps to reproduce

4. **Community support:** [txn2 Discussions](https://github.com/txn2/mcp-data-platform/discussions)
