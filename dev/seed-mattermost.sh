#!/usr/bin/env bash
# Seed the dev Mattermost with the account, team and channel a notification
# channel of the mattermost kind posts to (#1720).
#
# The preview image starts with no users. The first user created through the
# API becomes the system administrator, which is the only way in without a
# console: from there this creates a team, a public channel, and a personal
# access token, and writes the three values the acceptance run and the dev
# platform config read.
#
# The token is a personal access token on the seeded admin rather than a bot
# account's. A bot needs ENABLE_BOT_ACCOUNT_CREATION and an admin session to
# turn it on, which is two more round trips for a value that posts to the same
# API either way; what the platform holds is a bearer token for
# POST /api/v4/posts, and both kinds are exactly that.
#
# SAFETY: this only ever writes to a LOOPBACK Mattermost. It creates an
# administrator and hands out a token, which against a real server is an
# incident rather than an inconvenience, so a non-local address is refused.
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info() { echo -e "  ${YELLOW}…${NC} $1"; }
ok()   { echo -e "  ${GREEN}✓${NC} $1"; }
warn() { echo -e "  ${YELLOW}!${NC} $1"; }
fail() { echo -e "${RED}FAIL: $1${NC}" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PORT="${DEV_MATTERMOST_PORT:-8065}"
BASE="http://127.0.0.1:${PORT}"
ENV_FILE="$SCRIPT_DIR/.mattermost-env"

# The seeded identity. These are dev credentials for a loopback server and are
# deliberately fixed, so a developer can sign in at $BASE and see what the
# platform posted.
ADMIN_EMAIL="admin@example.com"
ADMIN_USER="devadmin"
ADMIN_PASS="Dev-Password-1"
TEAM_NAME="platform"
CHANNEL_NAME="ops-alerts"

case "$BASE" in
  http://127.0.0.1:*|http://localhost:*) ;;
  *) fail "refusing to seed a non-loopback Mattermost at $BASE" ;;
esac

# api POSTs a JSON body and prints the response. It never sets -f: a 4xx here
# is usually "already seeded", which the callers read rather than abort on.
api() {
  local method="$1" path="$2" body="${3:-}" token="${4:-}"
  local args=(-sS -X "$method" "$BASE$path" -H "Content-Type: application/json")
  [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
  [ -n "$body" ] && args+=(-d "$body")
  curl "${args[@]}"
}

# field reads one string field out of a JSON object on stdin.
field() {
  python3 -c "import sys,json
try:
    print(json.load(sys.stdin).get('$1',''))
except Exception:
    print('')"
}

# resource_id reads the id of a created or fetched resource, and prints
# nothing for an error.
#
# Mattermost error bodies carry an "id" of their own -- the i18n key, such as
# api.context.session_expired.app_error -- so reading "id" off whatever came
# back accepts a failure as a resource and writes it into the fixture as a
# channel id, which is exactly what this script did on its first real run. An
# error also carries status_code, which no resource does, and that is what
# this refuses on.
resource_id() {
  python3 -c "import sys,json
try:
    body = json.load(sys.stdin)
except Exception:
    sys.exit(0)
if not isinstance(body, dict) or 'status_code' in body:
    sys.exit(0)
print(body.get('id',''))"
}

info "waiting for Mattermost at $BASE"
for _ in $(seq 1 60); do
  if curl -sf "$BASE/api/v4/system/ping" >/dev/null 2>&1; then break; fi
  sleep 2
done
curl -sf "$BASE/api/v4/system/ping" >/dev/null 2>&1 || {
  warn "Mattermost did not answer at $BASE; skipping the chat fixture."
  warn "The mattermost acceptance criterion needs it: start it with 'docker compose -f dev/docker-compose.yml up -d mattermost'."
  exit 0
}
ok "Mattermost is up"

# The first user created becomes the system administrator. A second run gets
# an error here and logs in below instead, which is what makes this idempotent.
api POST /api/v4/users \
  "{\"email\":\"$ADMIN_EMAIL\",\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" >/dev/null 2>&1 || true

# Log in for the session token every call below is made with. The token is a
# response HEADER, not a body field, which is why this call is made with -i.
LOGIN_HEADERS=$(curl -sS -i -X POST "$BASE/api/v4/users/login" \
  -H "Content-Type: application/json" \
  -d "{\"login_id\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}")
SESSION=$(printf '%s' "$LOGIN_HEADERS" | tr -d '\r' | awk '/^[Tt]oken:/ {print $2}' | head -1)
[ -n "$SESSION" ] || fail "could not sign in to the dev Mattermost as $ADMIN_EMAIL"
USER_ID=$(printf '%s' "$LOGIN_HEADERS" | tail -1 | resource_id)
[ -n "$USER_ID" ] || fail "sign-in returned no user id"
ok "Signed in as $ADMIN_USER"

# Team. An existing one is found by name rather than created again.
TEAM_ID=$(api POST /api/v4/teams \
  "{\"name\":\"$TEAM_NAME\",\"display_name\":\"Platform\",\"type\":\"O\"}" "$SESSION" | resource_id)
if [ -z "$TEAM_ID" ]; then
  TEAM_ID=$(curl -sS "$BASE/api/v4/teams/name/$TEAM_NAME" -H "Authorization: Bearer $SESSION" | resource_id)
fi
[ -n "$TEAM_ID" ] || fail "could not create or find the '$TEAM_NAME' team"

# Channel, likewise.
CHANNEL_ID=$(api POST /api/v4/channels \
  "{\"team_id\":\"$TEAM_ID\",\"name\":\"$CHANNEL_NAME\",\"display_name\":\"Ops Alerts\",\"type\":\"O\"}" "$SESSION" | resource_id)
if [ -z "$CHANNEL_ID" ]; then
  CHANNEL_ID=$(curl -sS "$BASE/api/v4/teams/$TEAM_ID/channels/name/$CHANNEL_NAME" \
    -H "Authorization: Bearer $SESSION" | resource_id)
fi
[ -n "$CHANNEL_ID" ] || fail "could not create or find the '$CHANNEL_NAME' channel"
ok "Team '$TEAM_NAME' and channel '$CHANNEL_NAME' ready"

# Personal access tokens are off by default. Turning them on needs the whole
# config object back, edited and PUT: Mattermost has no partial config update.
CONFIG=$(curl -sS "$BASE/api/v4/config" -H "Authorization: Bearer $SESSION")
printf '%s' "$CONFIG" | python3 -c "
import json, sys
cfg = json.load(sys.stdin)
cfg.setdefault('ServiceSettings', {})['EnableUserAccessTokens'] = True
json.dump(cfg, sys.stdout)" > /tmp/mm-config.json
curl -sS -X PUT "$BASE/api/v4/config" \
  -H "Authorization: Bearer $SESSION" -H "Content-Type: application/json" \
  --data-binary @/tmp/mm-config.json >/dev/null
rm -f /tmp/mm-config.json

TOKEN=$(api POST "/api/v4/users/$USER_ID/tokens" \
  '{"description":"mcp-data-platform dev notification channel"}' "$SESSION" | field token)
if [ -z "$TOKEN" ]; then
  # The session token posts to the same API, so a server that refuses to mint
  # a personal token still yields a working fixture rather than no fixture.
  warn "personal access tokens are unavailable; using the session token instead"
  TOKEN="$SESSION"
fi

cat > "$ENV_FILE" <<EOF
# Written by dev/seed-mattermost.sh. Read by the acceptance suite and by
# anything that wants to post to the dev chat fixture. Not a secret: it is a
# loopback dev server seeded with a fixed password.
export MATTERMOST_URL="$BASE"
export MATTERMOST_TOKEN="$TOKEN"
export MATTERMOST_CHANNEL_ID="$CHANNEL_ID"
export MATTERMOST_TEAM_ID="$TEAM_ID"
EOF
ok "Mattermost fixture seeded ($ENV_FILE)"
info "sign in at $BASE as $ADMIN_EMAIL / $ADMIN_PASS to read what the platform posts"
