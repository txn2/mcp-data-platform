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

# Wait for MinIO
wait_for_service "MinIO" \
    "docker exec e2e-minio mc ready local 2>/dev/null || curl -sf http://localhost:9000/minio/health/live"

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
