import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

// The Connect button's only dependency is the hook that starts the flow, and
// which kind it starts it on is the assertion at the bottom of this file.
const startOAuth = vi.fn();
const useStartConnectionOAuth = vi.fn((_kind: string) => ({
  mutate: startOAuth,
  isPending: false,
}));
vi.mock("@/api/admin/hooks", () => ({
  useStartConnectionOAuth: (kind: string) => useStartConnectionOAuth(kind),
}));

import { ApiGatewayAuthFields } from "./ApiGatewayAuthFields";

// Migration 000050 rewrote every api-kind OAuth connection onto auth_mode
// "oauth" plus oauth_grant, and this editor kept offering only the two legacy
// modes and reading only the legacy keys. The result was an OAuth connection
// rendering with no auth configuration at all: the mode select matched nothing
// and the whole OAuth block was skipped (#1681). These cases are that
// connection, loaded.

function renderFields(config: Record<string, unknown>, kind = "api") {
  const onChange = vi.fn();
  render(
    <ApiGatewayAuthFields
      config={config}
      onChange={onChange}
      kind={kind}
      connectionName="google-analytics"
      isCreate={false}
      onOpenHelp={vi.fn()}
    />,
  );
  return onChange;
}

// The canonical connection from the ticket.
const canonical = {
  auth_mode: "oauth",
  oauth_grant: "authorization_code",
  base_url: "https://analyticsdata.googleapis.com",
  oauth_authorization_url: "https://accounts.google.com/o/oauth2/v2/auth",
  oauth_token_url: "https://oauth2.googleapis.com/token",
  oauth_client_id: "986495125425.apps.googleusercontent.com",
  oauth_client_secret: "[REDACTED]",
  oauth_scope: "https://www.googleapis.com/auth/analytics.readonly",
  oauth_endpoint_auth_style: "params",
  oauth_prompt: "consent",
};

describe("ApiGatewayAuthFields — a canonical OAuth connection", () => {
  it("renders the OAuth block with every stored value in its field", () => {
    renderFields(canonical);

    expect(screen.getByLabelText(/^Token URL$/i)).toHaveValue(
      "https://oauth2.googleapis.com/token",
    );
    expect(screen.getByLabelText(/^Authorization URL$/i)).toHaveValue(
      "https://accounts.google.com/o/oauth2/v2/auth",
    );
    expect(screen.getByLabelText(/^Client ID$/i)).toHaveValue(
      "986495125425.apps.googleusercontent.com",
    );
    expect(screen.getByLabelText(/^Client Secret$/i)).toHaveValue("[REDACTED]");
    expect(screen.getByLabelText(/^Scope$/i)).toHaveValue(
      "https://www.googleapis.com/auth/analytics.readonly",
    );
  });

  it("offers the browser sign-in affordance the grant needs", () => {
    renderFields(canonical);

    expect(screen.getByRole("button", { name: /^Connect$/i })).toBeEnabled();
  });

  it("hides the authorization URL when the grant is client_credentials", () => {
    renderFields({
      auth_mode: "oauth",
      oauth_grant: "client_credentials",
      oauth_token_url: "https://idp.example.com/token",
    });

    expect(screen.getByLabelText(/^Token URL$/i)).toBeInTheDocument();
    expect(
      screen.queryByLabelText(/^Authorization URL$/i),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /^Connect$/i }),
    ).not.toBeInTheDocument();
  });

  it("writes the canonical keys, and the scope as one string", () => {
    const onChange = renderFields(canonical);

    fireEvent.change(screen.getByLabelText(/^Client ID$/i), {
      target: { value: "new-client" },
    });
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ oauth_client_id: "new-client" }),
    );

    fireEvent.change(screen.getByLabelText(/^Scope$/i), {
      target: { value: "openid profile" },
    });
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ oauth_scope: "openid profile" }),
    );
    // The legacy keys are not written, at all: a save that added them back
    // beside the canonical ones is what created the collision in #1682.
    for (const call of onChange.mock.calls) {
      expect(Object.keys(call[0] as object)).not.toContain("oauth2_client_id");
      expect(Object.keys(call[0] as object)).not.toContain("oauth2_scopes");
    }
  });
});

describe("ApiGatewayAuthFields — the auth mode picker", () => {
  it("offers OAuth 2.1 without the legacy mode names", () => {
    renderFields({ auth_mode: "none" });

    fireEvent.click(screen.getByRole("combobox", { name: /auth mode/i }));
    expect(
      screen.getByRole("option", { name: "OAuth 2.1" }),
    ).toBeInTheDocument();
    for (const legacy of [
      "OAuth 2.1 client_credentials",
      "OAuth 2.1 authorization_code (browser sign-in)",
    ]) {
      expect(
        screen.queryByRole("option", { name: legacy }),
      ).not.toBeInTheDocument();
    }
  });
});

// Every HTTP-based kind renders this block, and the browser flow is resolved
// by (kind, name): starting it on the api kind for a graphql connection asks
// the server for a connection that does not exist.
describe("ApiGatewayAuthFields — the browser sign-in flow", () => {
  it("starts the flow on the kind the editor is editing", () => {
    useStartConnectionOAuth.mockClear();
    renderFields(canonical, "graphql");

    expect(useStartConnectionOAuth).toHaveBeenCalledWith("graphql");
    expect(useStartConnectionOAuth).not.toHaveBeenCalledWith("api");
  });
});

// #1734: the HTTP-based kinds sign an RFC 7523 assertion and exchange it at the
// token endpoint. The grant carries no browser flow and needs the signing
// fields the server requires, and the client credential becomes optional.
describe("ApiGatewayAuthFields — the jwt_bearer grant", () => {
  const jwtBearer = {
    auth_mode: "oauth",
    oauth_grant: "jwt_bearer",
    oauth_token_url: "https://login.example.com/services/oauth2/token",
    jwt_private_key_pem: "[REDACTED]",
    jwt_issuer: "3MVG9-consumer-key",
    jwt_subject: "integration@example.com",
  };

  it("is offered by the grant picker", () => {
    renderFields({ auth_mode: "oauth" });

    fireEvent.click(screen.getByRole("combobox", { name: /grant type/i }));
    expect(
      screen.getByRole("option", { name: /^jwt_bearer/ }),
    ).toBeInTheDocument();
  });

  it("renders the signing fields with the RS256 default and no browser flow", () => {
    renderFields(jwtBearer);

    expect(screen.getByText("Signed assertion (RFC 7523)")).toBeInTheDocument();
    expect(screen.getByLabelText(/signing key/i)).toHaveValue("[REDACTED]");
    expect(screen.queryByLabelText(/^client secret$/)).not.toBeInTheDocument();
    expect(screen.getByLabelText(/issuer/i)).toHaveValue("3MVG9-consumer-key");
    expect(screen.getByLabelText(/subject/i)).toHaveValue(
      "integration@example.com",
    );
    expect(screen.getByLabelText(/audience/i)).toHaveAttribute(
      "placeholder",
      "(the token URL)",
    );
    expect(
      screen.queryByLabelText(/^Authorization URL$/i),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /^Connect$/i }),
    ).not.toBeInTheDocument();
    expect(screen.getByLabelText(/^Client ID$/i)).toHaveAccessibleDescription(
      /optional/i,
    );
  });

  it("writes the signing fields under the keys the server reads", () => {
    const onChange = renderFields(jwtBearer);

    fireEvent.change(screen.getByLabelText(/subject/i), {
      target: { value: "svc-reporting@example.com" },
    });
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        oauth_grant: "jwt_bearer",
        jwt_subject: "svc-reporting@example.com",
      }),
    );
  });
});
