import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

import { SignedJWTAuthFields } from "./SignedJWTAuthFields";

// The algorithm decides which key material the upstream expects, and the
// server refuses a connection carrying the wrong one: a shared secret with
// RS256, or a PEM key with HS256. The form has to show only the control the
// selected algorithm can use, or an operator fills in a field whose save is
// rejected.
describe("SignedJWTAuthFields — the key material the algorithm selects", () => {
  it("offers the shared secret and no signing key under HS256", () => {
    render(
      <SignedJWTAuthFields
        config={{ jwt_algorithm: "HS256" }}
        onChange={vi.fn()}
      />,
    );

    expect(screen.getByLabelText(/client secret/i)).toBeInTheDocument();
    expect(screen.queryByLabelText(/signing key/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/key id/i)).not.toBeInTheDocument();
  });

  it("defaults to HS256 when the connection states no algorithm", () => {
    render(<SignedJWTAuthFields config={{}} onChange={vi.fn()} />);

    expect(screen.getByLabelText(/client secret/i)).toBeInTheDocument();
  });

  it.each(["RS256", "ES256"])(
    "offers the signing key and its id under %s, and no shared secret",
    (algorithm) => {
      render(
        <SignedJWTAuthFields
          config={{ jwt_algorithm: algorithm }}
          onChange={vi.fn()}
        />,
      );

      expect(screen.getByLabelText(/signing key/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/key id/i)).toBeInTheDocument();
      expect(screen.queryByLabelText(/client secret/i)).not.toBeInTheDocument();
    },
  );
});

// Every claim is a value the upstream registered and compares literally, so
// the form has to render what is stored and write what is typed under the key
// the server reads.
describe("SignedJWTAuthFields — the claims", () => {
  it("renders the stored claims", () => {
    render(
      <SignedJWTAuthFields
        config={{
          jwt_issuer: "CLIENTID-4f21ab",
          jwt_subject: "svc-integration",
          jwt_audience: "https://erp.example.com/api",
          jwt_token_lifetime: "300s",
          jwt_issued_at_skew: "30s",
        }}
        onChange={vi.fn()}
      />,
    );

    expect(screen.getByLabelText(/issuer/i)).toHaveValue("CLIENTID-4f21ab");
    expect(screen.getByLabelText(/subject/i)).toHaveValue("svc-integration");
    expect(screen.getByLabelText(/audience/i)).toHaveValue(
      "https://erp.example.com/api",
    );
    expect(screen.getByLabelText(/token lifetime/i)).toHaveValue("300s");
    expect(screen.getByLabelText(/issued-at skew/i)).toHaveValue("30s");
  });

  it("writes an edited claim under the key the server reads", () => {
    const onChange = vi.fn();
    render(
      <SignedJWTAuthFields
        config={{ jwt_issuer: "old" }}
        onChange={onChange}
      />,
    );

    fireEvent.change(screen.getByLabelText(/audience/i), {
      target: { value: "https://erp.example.com/api1/service" },
    });

    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        jwt_issuer: "old",
        jwt_audience: "https://erp.example.com/api1/service",
      }),
    );
  });

  it("leaves the audience empty so the server defaults it to the endpoint URL", () => {
    render(<SignedJWTAuthFields config={{}} onChange={vi.fn()} />);

    expect(screen.getByLabelText(/audience/i)).toHaveValue("");
  });
});

// Under oauth_grant=jwt_bearer the same block states the grant's defaults:
// RS256, the token URL as the audience, and issuer and subject required.
describe("SignedJWTAuthFields — the jwt_bearer variant", () => {
  it("defaults to RS256 when the connection states no algorithm", () => {
    render(
      <SignedJWTAuthFields config={{}} onChange={vi.fn()} variant="jwt_bearer" />,
    );

    expect(screen.getByLabelText(/signing key/i)).toBeInTheDocument();
    expect(screen.queryByLabelText(/client secret/i)).not.toBeInTheDocument();
  });

  it("still signs with a shared secret when HS256 is chosen", () => {
    render(
      <SignedJWTAuthFields
        config={{ jwt_algorithm: "HS256" }}
        onChange={vi.fn()}
        variant="jwt_bearer"
      />,
    );

    expect(screen.getByLabelText(/client secret/i)).toBeInTheDocument();
  });

  it("names the token URL as the audience default and marks the identity claims required", () => {
    render(
      <SignedJWTAuthFields config={{}} onChange={vi.fn()} variant="jwt_bearer" />,
    );

    expect(screen.getByLabelText(/audience/i)).toHaveAttribute(
      "placeholder",
      "(the token URL)",
    );
    expect(screen.getByLabelText(/issuer/i)).toHaveAccessibleDescription(
      /^Required\./,
    );
    expect(screen.getByLabelText(/subject/i)).toHaveAccessibleDescription(
      /^Required\./,
    );
  });
});
