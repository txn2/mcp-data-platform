import { describe, it, expect } from "vitest";

import {
  canonicalizeOAuthConfig,
  shadowedOAuthKeys,
  storedOAuthGrant,
} from "./oauthVocabulary";

// A connection's OAuth configuration reaches the editor in whichever spelling
// it was stored in. The editor speaks one, so it folds the other onto it as it
// loads, and what it folds has to match what the server reads: the canonical
// key wins, because that is the value the connection authenticates with.

describe("canonicalizeOAuthConfig", () => {
  it("moves every legacy key onto its canonical sibling", () => {
    expect(
      canonicalizeOAuthConfig({
        base_url: "https://api.example.com",
        auth_mode: "oauth2_authorization_code",
        oauth2_token_url: "https://idp.example.com/token",
        oauth2_authorization_url: "https://idp.example.com/auth",
        oauth2_client_id: "platform-client",
        oauth2_client_secret: "[REDACTED]",
        oauth2_scopes: ["read:users", "write:orders"],
        oauth2_prompt: "consent",
        oauth2_endpoint_auth_style: "params",
      }),
    ).toEqual({
      base_url: "https://api.example.com",
      auth_mode: "oauth",
      oauth_grant: "authorization_code",
      oauth_token_url: "https://idp.example.com/token",
      oauth_authorization_url: "https://idp.example.com/auth",
      oauth_client_id: "platform-client",
      oauth_client_secret: "[REDACTED]",
      oauth_scope: "read:users write:orders",
      oauth_prompt: "consent",
      oauth_endpoint_auth_style: "params",
    });
  });

  it("derives client_credentials from the other legacy mode", () => {
    const got = canonicalizeOAuthConfig({
      auth_mode: "oauth2_client_credentials",
    });
    expect(got.auth_mode).toBe("oauth");
    expect(got.oauth_grant).toBe("client_credentials");
  });

  it("keeps the canonical value and drops the shadowed legacy one", () => {
    // This is the incident shape: a real client id the operator typed under
    // the legacy key, a placeholder under the canonical one, and the
    // placeholder is what the platform authenticates with. The editor has to
    // show the placeholder, or the operator corrects the wrong field.
    expect(
      canonicalizeOAuthConfig({
        auth_mode: "oauth",
        oauth_grant: "authorization_code",
        oauth_client_id: "PENDING-REPLACE-ME",
        oauth2_client_id: "986495125425.apps.googleusercontent.com",
      }),
    ).toEqual({
      auth_mode: "oauth",
      oauth_grant: "authorization_code",
      oauth_client_id: "PENDING-REPLACE-ME",
    });
  });

  it("leaves a canonical config and a non-OAuth config alone", () => {
    const canonical = {
      auth_mode: "oauth",
      oauth_grant: "client_credentials",
      oauth_token_url: "https://idp.example.com/token",
    };
    expect(canonicalizeOAuthConfig(canonical)).toEqual(canonical);

    const bearer = { base_url: "https://api.example.com", auth_mode: "bearer" };
    expect(canonicalizeOAuthConfig(bearer)).toEqual(bearer);
  });

  it("does not modify the config it was given", () => {
    const stored = {
      auth_mode: "oauth2_client_credentials",
      oauth2_client_id: "c",
    };
    canonicalizeOAuthConfig(stored);
    expect(stored).toEqual({
      auth_mode: "oauth2_client_credentials",
      oauth2_client_id: "c",
    });
  });

  it("reads a legacy scope already stored as a string", () => {
    expect(
      canonicalizeOAuthConfig({ oauth2_scopes: "read write" }).oauth_scope,
    ).toBe("read write");
  });
});

describe("shadowedOAuthKeys", () => {
  it("names the legacy keys whose canonical sibling is set", () => {
    expect(
      shadowedOAuthKeys({
        auth_mode: "oauth2_authorization_code",
        oauth_grant: "authorization_code",
        oauth_client_id: "canonical",
        oauth2_client_id: "ignored",
        oauth_scope: "read",
        oauth2_scopes: ["read"],
        oauth2_token_url: "https://idp.example.com/token",
      }),
    ).toEqual([
      // oauth2_token_url has no canonical sibling here, so it is read.
      "oauth2_client_id",
      "oauth2_scopes",
      "auth_mode",
    ]);
  });

  it("names nothing on a single-vocabulary config", () => {
    expect(
      shadowedOAuthKeys({ auth_mode: "oauth", oauth_client_id: "c" }),
    ).toEqual([]);
    expect(
      shadowedOAuthKeys({
        auth_mode: "oauth2_client_credentials",
        oauth2_client_id: "c",
      }),
    ).toEqual([]);
  });
});

describe("storedOAuthGrant", () => {
  it("reads the grant in every spelling a stored connection carries", () => {
    expect(storedOAuthGrant({ auth_mode: "oauth", oauth_grant: "jwt_bearer" })).toBe(
      "jwt_bearer",
    );
    expect(
      storedOAuthGrant({ auth_mode: "oauth", oauth: { grant: "authorization_code" } }),
    ).toBe("authorization_code");
    expect(storedOAuthGrant({ auth_mode: "oauth2_authorization_code" })).toBe(
      "authorization_code",
    );
    expect(
      storedOAuthGrant({
        auth_mode: "oauth",
        oauth_grant: "client_credentials",
        oauth: { grant: "authorization_code" },
      }),
    ).toBe("client_credentials");
  });

  it("is empty when nothing states a grant", () => {
    expect(storedOAuthGrant(undefined)).toBe("");
    expect(storedOAuthGrant({ auth_mode: "oauth" })).toBe("");
    expect(storedOAuthGrant({ auth_mode: "oauth", oauth: ["not", "a", "block"] })).toBe("");
    expect(storedOAuthGrant({ auth_mode: "bearer", oauth_grant: "" })).toBe("");
  });
});
