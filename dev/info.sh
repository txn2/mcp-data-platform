#!/usr/bin/env bash
#
# Prints how to reach and sign in to the local dev environment.
#
# Run any time (even after `make dev`'s startup banner has scrolled away)
# via `make dev-info`. The values below are the dev defaults; the source
# of truth is dev/platform.yaml (api_keys) and the dev Keycloak realm.
set -euo pipefail

# Colors only when stdout is a terminal, so `make dev-info | pbcopy` and
# log redirects stay clean.
if [ -t 1 ]; then
  CYAN='\033[0;36m'; BOLD='\033[1m'; GREEN='\033[0;32m'; NC='\033[0m'
else
  CYAN=''; BOLD=''; GREEN=''; NC=''
fi

echo -e "${BOLD}${GREEN}Local dev login${NC}"
echo ""
echo -e "  Portal UI:    ${CYAN}http://localhost:5173/portal/${NC}"
echo -e "  Go API:       ${CYAN}http://localhost:8080${NC}"
echo -e "  API key:      ${CYAN}acme-dev-key-2024${NC}   (send as ${BOLD}X-API-Key${NC} header)"
echo ""
echo -e "  ${BOLD}Portal sign-in (Keycloak OIDC)${NC}"
echo -e "    ${CYAN}admin@example.com${NC}   / ${CYAN}admin-password${NC}    (dp_admin)"
echo -e "    ${CYAN}analyst@example.com${NC} / ${CYAN}analyst-password${NC}  (dp_analyst)"
echo -e "  Keycloak admin console: ${CYAN}http://localhost:9090/admin${NC}  (admin / admin)"
echo ""
