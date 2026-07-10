-- Hash opaque bearer credentials at rest. Refresh tokens and authorization
-- codes were previously stored as plaintext, so anyone with read access to
-- the database (backup, replica, operator) held live credentials. The store
-- now persists only the hex-encoded SHA-256 digest and hashes the presented
-- value on lookup; converting existing rows in place keeps live sessions
-- valid across the upgrade. Column names are unchanged: only the value
-- representation changes.
--
-- convert_to(.., 'UTF8') is the byte-exact counterpart of Go's
-- sha256([]byte(value)); a plain ::bytea cast would run the text through
-- bytea's input parser, which mis-parses or rejects backslashes.
--
-- The WHERE guard skips values that are already 64-char lowercase hex, so
-- re-running the statement (e.g. dirty-migration recovery, or an operator
-- sweeping up stragglers) never double-hashes a digest. Issued raw
-- credentials are 43-char base64url strings and can never match the guard.
--
-- Deployment note for multi-replica rolling upgrades: replicas still running
-- the previous binary compare raw values against the now-hashed rows, so
-- refresh grants they serve fail until the rollout completes, and any token
-- such a replica issues after this migration ran is stored plaintext and is
-- invalid under the new binary. Affected clients recover with one interactive
-- re-authorization. Complete the rollout promptly to minimize the window.
UPDATE oauth_refresh_tokens
SET token = encode(sha256(convert_to(token, 'UTF8')), 'hex')
WHERE token !~ '^[0-9a-f]{64}$';
UPDATE oauth_authorization_codes
SET code = encode(sha256(convert_to(code, 'UTF8')), 'hex')
WHERE code !~ '^[0-9a-f]{64}$';
