#!/bin/bash
# MCP Apps Development Script
#
# Quick start for MCP Apps development. Starts Trino and runs MCP server.
#
# Usage:
#   ./scripts/dev-mcpapps.sh         # Start everything
#   ./scripts/dev-mcpapps.sh stop    # Stop Trino container
#   ./scripts/dev-mcpapps.sh logs    # Show Trino logs

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

case "${1:-start}" in
  start)
    echo -e "${GREEN}Starting MCP Apps Development Environment${NC}"
    echo ""

    # Start Trino
    echo "Starting Trino..."
    docker compose -f "$PROJECT_ROOT/docker-compose.dev.yml" up -d

    # Wait for Trino to be ready
    echo "Waiting for Trino to be ready..."
    for i in {1..30}; do
      if docker exec mcp-dev-trino trino --execute "SELECT 1" &>/dev/null; then
        echo -e "${GREEN}Trino is ready!${NC}"
        break
      fi
      if [ $i -eq 30 ]; then
        echo "Trino failed to start. Check logs: docker logs mcp-dev-trino"
        exit 1
      fi
      sleep 2
    done

    echo ""
    echo -e "${GREEN}Starting MCP Server...${NC}"
    echo ""
    echo "Apps are served from: $PROJECT_ROOT/apps/"
    echo "Edit HTML/JS/CSS files and refresh to see changes."
    echo ""
    echo "Test with MCP Inspector:"
    echo "  npx @anthropics/mcp-inspector http://localhost:3001"
    echo ""
    echo "Example query (trino_query tool):"
    echo '  {"sql": "SELECT 1 as id, '\''Product A'\'' as name, 15000.50 as revenue"}'
    echo ""

    # Set environment variable and run server
    export MCP_APPS_PATH="$PROJECT_ROOT/apps"
    exec go run "$PROJECT_ROOT/cmd/mcp-data-platform" --config "$PROJECT_ROOT/configs/mcpapps-dev.yaml"
    ;;

  stop)
    echo "Stopping development environment..."
    docker compose -f "$PROJECT_ROOT/docker-compose.dev.yml" down
    echo -e "${GREEN}Stopped.${NC}"
    ;;

  logs)
    docker logs -f mcp-dev-trino
    ;;

  *)
    echo "Usage: $0 {start|stop|logs}"
    exit 1
    ;;
esac
