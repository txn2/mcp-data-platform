import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";

vi.mock("@/api/admin/hooks", () => ({
  useStartConnectionOAuth: () => ({ mutate: vi.fn(), isPending: false }),
  useSecrets: () => ({
    data: { secrets: [{ name: "dv360-key" }, { name: "ga-key" }] },
  }),
}));

import { ApiGatewayAuthFields } from "./ApiGatewayAuthFields";
import {
  GoogleServiceAccountFields,
  readKeyFile,
} from "./GoogleServiceAccountFields";

// #2061: a Google service account is a key file and its scopes. The form
// writes the key and the scopes and leaves the mode and grant for the server
// to derive from the key.

const keyFile = JSON.stringify({
  type: "service_account",
  project_id: "acme-analytics",
  private_key_id: "3f2a9c",
  private_key: "-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----\n",
  client_email: "reporting@acme-analytics.iam.gserviceaccount.com",
  token_uri: "https://oauth2.googleapis.com/token",
});

function upload(text: string) {
  const input = screen.getByLabelText("Key file", { selector: "input" });
  const file = new File([text], "key.json", { type: "application/json" });
  Object.defineProperty(file, "text", { value: () => Promise.resolve(text) });
  fireEvent.change(input, { target: { files: [file] } });
}

describe("readKeyFile", () => {
  it("returns the identity of a service account key and refuses anything else without repeating it", () => {
    expect(readKeyFile(keyFile)).toEqual({
      client_email: "reporting@acme-analytics.iam.gserviceaccount.com",
      project_id: "acme-analytics",
      private_key_id: "3f2a9c",
    });
    expect(readKeyFile("not json")).toBe("This is not a JSON key file.");
    expect(readKeyFile(JSON.stringify({ type: "authorized_user" }))).toMatch(
      /not for a service account/,
    );
    const { private_key: _drop, ...noKey } = JSON.parse(keyFile) as Record<
      string,
      unknown
    >;
    expect(readKeyFile(JSON.stringify(noKey))).toBe(
      "This key file has no private_key.",
    );
  });
});

describe("ApiGatewayAuthFields — Google service account", () => {
  function renderAuth(config: Record<string, unknown>) {
    const onChange = vi.fn();
    const utils = render(
      <ApiGatewayAuthFields
        config={config}
        onChange={onChange}
        kind="api"
        connectionName="dv360"
        isCreate
        onOpenHelp={vi.fn()}
      />,
    );
    return { onChange, ...utils };
  }

  it("shows a stored connection as a Google service account with the account it uses", () => {
    renderAuth({
      auth_mode: "oauth",
      google_service_account_json: "[REDACTED]",
      google_service_account_identity: {
        client_email: "reporting@acme-analytics.iam.gserviceaccount.com",
        project_id: "acme-analytics",
        private_key_id: "3f2a9c",
      },
      oauth_scope: "https://www.googleapis.com/auth/display-video",
    });
    expect(
      screen.getByText("Google service account", {
        selector: "[data-slot='card-title'], h3, div",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("reporting@acme-analytics.iam.gserviceaccount.com"),
    ).toBeInTheDocument();
    expect(screen.getByText("3f2a9c")).toBeInTheDocument();
    expect(
      screen.getByText("https://www.googleapis.com/auth/display-video"),
    ).toBeInTheDocument();
    expect(screen.queryByText("PRIVATE KEY")).not.toBeInTheDocument();
  });

  it("drops the values another mode left behind when switched to a Google service account", () => {
    const { onChange } = renderAuth({
      base_url: "https://displayvideo.googleapis.com",
      auth_mode: "oauth",
      oauth_grant: "client_credentials",
      oauth_client_id: "abc",
      oauth_client_secret: "[REDACTED]",
      oauth_token_url: "https://login.example.com/token",
    });
    fireEvent.click(screen.getByRole("combobox", { name: /Auth mode/i }));
    fireEvent.click(
      screen.getByRole("option", { name: "Google service account" }),
    );
    expect(onChange).toHaveBeenLastCalledWith({
      base_url: "https://displayvideo.googleapis.com",
    });
  });
});

describe("GoogleServiceAccountFields", () => {
  it("takes an uploaded key file, shows whose it is, and clears a stored secret", async () => {
    const onChange = vi.fn();
    render(<GoogleServiceAccountFields config={{}} onChange={onChange} />);
    upload(keyFile);
    await waitFor(() => expect(onChange).toHaveBeenCalled());
    expect(onChange).toHaveBeenLastCalledWith({
      google_service_account_json: keyFile,
    });
    expect(
      await screen.findByText(
        "reporting@acme-analytics.iam.gserviceaccount.com",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("acme-analytics")).toBeInTheDocument();
  });

  it("refuses a file that is not a service account key, naming why", async () => {
    const onChange = vi.fn();
    render(<GoogleServiceAccountFields config={{}} onChange={onChange} />);
    upload(
      JSON.stringify({ type: "authorized_user", client_secret: "s3cret" }),
    );
    expect(
      await screen.findByText(/not for a service account/),
    ).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.queryByText(/s3cret/)).not.toBeInTheDocument();
  });

  it("names a stored secret in place of the file", () => {
    const onChange = vi.fn();
    render(
      <GoogleServiceAccountFields
        config={{ google_service_account_json: "[REDACTED]" }}
        onChange={onChange}
      />,
    );
    fireEvent.click(screen.getByRole("radio", { name: "Stored secret" }));
    fireEvent.click(screen.getByRole("combobox", { name: /Stored secret/i }));
    fireEvent.click(screen.getByRole("option", { name: "dv360-key" }));
    expect(onChange).toHaveBeenLastCalledWith({
      google_service_account_secret: "dv360-key",
    });
  });

  it("adds and removes scopes, kept as the space-delimited oauth_scope", () => {
    const onChange = vi.fn();
    const { rerender } = render(
      <GoogleServiceAccountFields
        config={{
          oauth_scope: "https://www.googleapis.com/auth/display-video",
        }}
        onChange={onChange}
      />,
    );
    fireEvent.change(screen.getByLabelText("Scopes"), {
      target: {
        value: "https://www.googleapis.com/auth/doubleclickbidmanager",
      },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(onChange).toHaveBeenLastCalledWith({
      oauth_scope:
        "https://www.googleapis.com/auth/display-video https://www.googleapis.com/auth/doubleclickbidmanager",
    });
    rerender(
      <GoogleServiceAccountFields
        config={{
          oauth_scope: "https://www.googleapis.com/auth/display-video",
        }}
        onChange={onChange}
      />,
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Remove https://www.googleapis.com/auth/display-video",
      }),
    );
    expect(onChange).toHaveBeenLastCalledWith({});
  });

  it("writes the delegated user as the assertion subject", () => {
    const onChange = vi.fn();
    render(<GoogleServiceAccountFields config={{}} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Delegated user"), {
      target: { value: "analyst@example.com" },
    });
    expect(onChange).toHaveBeenLastCalledWith({
      jwt_subject: "analyst@example.com",
    });
  });
});
