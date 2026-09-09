# Signed JWT upstream authentication

Some upstreams do not hand out a token. They hand out an identifier and a signing key, and expect the client to **mint its own** short-lived JSON Web Token on every call. There is no token endpoint, nothing is exchanged, and the assertion is valid for minutes.

`auth_mode: signed_jwt` is that pattern. It is part of the shared upstream authentication policy, so it is available on every HTTP-based connection kind — `api` and `graphql` today — with the same keys and the same at-rest encryption.

## When you need it

Reach for this mode when the upstream's setup instructions read: *create an application, copy the client id and the secret (or download the private key), and sign a JWT with these claims.*

No other mode can produce that assertion:

- `bearer` sends a fixed string. A token whose lifetime is 300 seconds cannot be a fixed string.
- `oauth` exchanges credentials at a token endpoint. There is no token endpoint to exchange against.
- `api_key` and `basic` send a static credential, not a signed, time-bounded claim set.

## Configuration

| Key | Meaning |
| --- | --- |
| `jwt_algorithm` | `HS256` (default), `RS256` or `ES256`. Selects the signing method and which key field is required |
| `jwt_client_secret` | The HMAC shared secret. Required for `HS256`, refused for the asymmetric algorithms. Encrypted at rest |
| `jwt_private_key_pem` | The PEM private key (PKCS#1 or PKCS#8 for RSA, SEC 1 or PKCS#8 for ECDSA). Required for `RS256` and `ES256`, refused for `HS256`. Encrypted at rest |
| `jwt_key_id` | Emitted as the token's `kid` header when set. Upstreams holding several registered keys for one account select by it. Optional |
| `jwt_issuer` | The `iss` claim. Omitted from the token when empty |
| `jwt_subject` | The `sub` claim. Omitted from the token when empty |
| `jwt_audience` | The `aud` claim. Defaults to the connection's endpoint URL (`base_url` on an `api` connection, `endpoint_url` on a `graphql` one) |
| `jwt_token_lifetime` | `exp - iat`. Default `300s` |
| `jwt_issued_at_skew` | How far back `iat` is set to absorb clock drift. Default `30s` |

At least one of `jwt_issuer` and `jwt_subject` must be set. Which one — or both — depends on what the upstream registered: a token carrying a claim the upstream did not register is rejected as surely as one missing a claim it did.

The platform mints one assertion, reuses it until it comes within `jwt_issued_at_skew` of its own expiry, and mints a fresh one after that. The skew is therefore both the backdating applied to `iat` and the renewal margin, which is why setting it to zero also gives up the margin: a token would then be presented right up to `exp` and could expire in flight. Every request carries `Authorization: Bearer <jwt>`.

### What is refused at save time

The connection is refused, naming the key you have to fix, when:

- neither `jwt_client_secret` nor `jwt_private_key_pem` is set;
- the key material does not match `jwt_algorithm` (a secret with `RS256`, a PEM key with `HS256`);
- `jwt_private_key_pem` is not a key the chosen algorithm can use;
- `jwt_algorithm` is not one of the three;
- neither `jwt_issuer` nor `jwt_subject` is set;
- `jwt_token_lifetime` is zero or negative;
- `jwt_issued_at_skew` is negative, or at or past `jwt_token_lifetime` — backdating `iat` by the whole lifetime mints a token that is already expired.

### The audience is matched literally

`aud` is the field that most often turns out to be wrong, because upstreams in this class compare it byte for byte against what was registered. A trailing slash, `http` where the registration said `https`, an internal host name where the registration used the public one: each is a rejected token, and none of them looks wrong at a glance. When the connection's endpoint URL is not exactly the string the upstream registered, set `jwt_audience` explicitly.

## Diagnosing a rejected token

The upstream's own 401 body is the only place that says which claim was wrong, so the platform passes it through unchanged rather than replacing it with a message of its own. Whatever the upstream answered — plain text, JSON, a numeric code — arrives in the tool result's `body` with `status: 401`.

Read it before changing anything. These upstreams are specific: they distinguish a bad signature from a wrong issuer from a wrong audience, and the body says which. Changing the secret when the body said the audience was wrong wastes a round of guessing.

Neither the shared secret nor the private key appears in a log line, an audit row, or an admin API response. The admin API returns both as `[REDACTED]`; re-submitting `[REDACTED]` on a `PUT` preserves the stored value, and pasting a new value rotates it.

## Worked example: Sage X3

An X3 administrator creates a connected application under `Administration > Settings > Authentication > Connected applications`, giving it a name, the exact API URL, a token lifetime and one allowed Syracuse user. X3 returns a client id and a secret. Each request then carries an HS256 assertion whose `iss` is the client id, `sub` the allowed user, and `aud` the registered URL.

X3's API is GraphQL, so this is a `graphql` connection. It also routes by folder through a header, which is not a credential and therefore belongs in `static_headers`:

```json
{
  "config": {
    "endpoint_url": "https://x3.example.com:8124/xtrem/api",
    "auth_mode": "signed_jwt",
    "jwt_algorithm": "HS256",
    "jwt_client_secret": "<the connected application's secret>",
    "jwt_issuer": "<the connected application's client id>",
    "jwt_subject": "<the allowed Syracuse user>",
    "jwt_audience": "https://x3.example.com:8124/xtrem/api",
    "jwt_token_lifetime": "300s",
    "jwt_issued_at_skew": "30s",
    "static_headers": { "x-xtrem-endpoint": "<folder>" }
  }
}
```

X3 answers a bad token with an HTTP 401 and a plain-text body of the form `Error NN: Invalid token`. Its published codes are a reading aid for that body — the platform implements none of them, and passes the body through as it arrived:

| Code | What X3 found wrong |
| --- | --- |
| 41 | `iss` did not match the connected application |
| 42 | `sub` is not the allowed user |
| 45 | `aud` did not match the registered URL |
| 50 | the signature did not verify, or the token had expired |

Basic authentication with Syracuse credentials also reaches X3, but Sage documents it as a development convenience. Use `signed_jwt` for anything else.

## Worked example: Snowflake key-pair authentication

An `api` connection to Snowflake's SQL API: the same shape over a registered public key rather than a shared secret. The account administrator registers the public half of an RSA key pair against a Snowflake user; the client signs an RS256 assertion with the private half.

Snowflake derives both identity claims from the account and user, and its `iss` includes the fingerprint of the registered public key. Both are uppercase, and periods in an account identifier become hyphens:

- `iss`: `<account_identifier>.<user>.SHA256:<public key fingerprint>`
- `sub`: `<account_identifier>.<user>`

Snowflake honors a token for at most one hour whatever `exp` says, so a lifetime past that buys nothing.

```json
{
  "config": {
    "base_url": "https://<account_identifier>.snowflakecomputing.com",
    "auth_mode": "signed_jwt",
    "jwt_algorithm": "RS256",
    "jwt_private_key_pem": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----",
    "jwt_issuer": "MYORG-MYACCT.MYUSER.SHA256:hV0i...=",
    "jwt_subject": "MYORG-MYACCT.MYUSER",
    "jwt_audience": "https://<account_identifier>.snowflakecomputing.com",
    "jwt_token_lifetime": "3600s",
    "static_headers": { "X-Snowflake-Authorization-Token-Type": "KEYPAIR_JWT" }
  }
}
```

The token-type header is optional — Snowflake can tell from the token itself — but stating it makes the connection self-describing.

## Worked example: Apple App Store Connect

The asymmetric case that uses `jwt_key_id`. App Store Connect issues an API key as a downloadable `.p8` private key plus two identifiers: a **key ID**, which selects the key, and an **issuer ID**, which identifies the team. The assertion is ES256, the key ID travels in the token's `kid` header, and the audience is a fixed string rather than a URL.

A **team key** identifies itself with `iss`:

```json
{
  "config": {
    "base_url": "https://api.appstoreconnect.apple.com",
    "auth_mode": "signed_jwt",
    "jwt_algorithm": "ES256",
    "jwt_private_key_pem": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----",
    "jwt_key_id": "2X9R4HXF34",
    "jwt_issuer": "57246542-96fe-1a63-e053-0824d011072a",
    "jwt_audience": "appstoreconnect-v1",
    "jwt_token_lifetime": "1200s"
  }
}
```

An **individual key** identifies itself with `sub: user` and carries no `iss`, which is why the mode requires one of the two rather than the issuer specifically:

```json
{
  "jwt_subject": "user",
  "jwt_audience": "appstoreconnect-v1"
}
```

Apple honors a token for at most 20 minutes unless it carries a `scope` claim restricted to GET requests, and this mode does not emit `scope`. Keep `jwt_token_lifetime` at or under `1200s`.

## Where the first real call happens

No upstream of this class is reachable from this project, and none will be: every one of them is customer- or account-scoped. The platform's own tests run against a fixture that verifies the presented assertion and reports the claims it read, which proves what the platform puts on the wire and nothing about any vendor. The first call against a real Sage X3, Snowflake or App Store Connect happens at an install, with credentials only that operator holds.

Plan the first connection accordingly: expect to read a 401 body or two, and treat `jwt_audience` as the first thing to check.
