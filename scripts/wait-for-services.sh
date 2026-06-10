#!/bin/bash
# Wait for E2E test services to be ready

set -e

TIMEOUT=${TIMEOUT:-120}
INTERVAL=${INTERVAL:-5}

wait_for_service() {
    local name="$1"
    local check_cmd="$2"
    local elapsed=0

    echo "Waiting for $name..."

    while ! eval "$check_cmd" > /dev/null 2>&1; do
        if [ "$elapsed" -ge "$TIMEOUT" ]; then
            echo "ERROR: $name failed to become ready within ${TIMEOUT}s"
            return 1
        fi
        sleep "$INTERVAL"
        elapsed=$((elapsed + INTERVAL))
        echo "  Still waiting for $name... (${elapsed}s)"
    done

    echo "$name is ready!"
}

# Wait for PostgreSQL
wait_for_service "PostgreSQL" \
    "docker exec e2e-postgres pg_isready -U platform -d mcp_platform"

# Wait for Trino
wait_for_service "Trino" \
    "docker exec e2e-trino trino --execute 'SELECT 1'"

# Wait for SeaweedFS (S3-compatible gateway on :9000). A plain connectivity
# probe: the gateway answers with an HTTP status (200/403) once up, so a
# successful connection — not a 2xx — is the readiness signal.
wait_for_service "SeaweedFS (S3)" \
    "curl -s -o /dev/null http://localhost:9000"

# Wait for DataHub (if using datahub quickstart)
if docker ps --format '{{.Names}}' | grep -q "datahub-gms"; then
    wait_for_service "DataHub GMS" \
        "curl -sf http://localhost:8080/health"
    echo "DataHub detected and ready!"
else
    echo "DataHub not detected - skipping (use 'datahub docker quickstart' to start)"
fi

echo ""
echo "All services are ready!"
