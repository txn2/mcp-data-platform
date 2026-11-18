import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

import { OAuthFields } from "./OAuthFields";

// The mcp kind's editor renders this block without jwtBearer, and its server
// side refuses the grant on save (#1734). Offering it there would be a field
// whose save always fails.
describe("OAuthFields — the jwt_bearer grant is offered only where it is honored", () => {
  it("is absent from the mcp editor's grant picker", () => {
    render(<OAuthFields config={{ auth_mode: "oauth" }} onChange={vi.fn()} />);

    fireEvent.click(screen.getByRole("combobox", { name: /grant type/i }));
    expect(
      screen.getByRole("option", { name: /^client_credentials/ }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("option", { name: /^jwt_bearer/ }),
    ).not.toBeInTheDocument();
  });

  it("renders no signing fields in the mcp editor for a stored jwt_bearer grant", () => {
    render(
      <OAuthFields
        config={{ auth_mode: "oauth", oauth_grant: "jwt_bearer" }}
        onChange={vi.fn()}
      />,
    );

    expect(screen.queryByLabelText(/signing key/i)).not.toBeInTheDocument();
  });

  it("is offered, and selecting it writes oauth_grant, where jwtBearer is set", () => {
    const onChange = vi.fn();
    render(
      <OAuthFields
        config={{ auth_mode: "oauth" }}
        onChange={onChange}
        endpointAuthStyle
        jwtBearer
      />,
    );

    fireEvent.click(screen.getByRole("combobox", { name: /grant type/i }));
    fireEvent.click(screen.getByRole("option", { name: /^jwt_bearer/ }));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ oauth_grant: "jwt_bearer" }),
    );
  });
});

// #2689: the password grant, for a legacy upstream that offers no other.
describe("OAuthFields — the password grant", () => {
  it("is absent from the mcp editor", () => {
    render(<OAuthFields config={{ auth_mode: "oauth" }} onChange={vi.fn()} />);
    fireEvent.click(screen.getByRole("combobox", { name: /grant type/i }));
    expect(screen.queryByRole("option", { name: /^password/ })).not.toBeInTheDocument();
  });

  it("is offered where jwtBearer is set, and selecting it writes oauth_grant", () => {
    const onChange = vi.fn();
    render(<OAuthFields config={{ auth_mode: "oauth" }} onChange={onChange} endpointAuthStyle jwtBearer />);
    fireEvent.click(screen.getByRole("combobox", { name: /grant type/i }));
    fireEvent.click(screen.getByRole("option", { name: /^password \(username and password, legacy\)/ }));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ oauth_grant: "password" }));
  });

  it("shows Username and a write-only Password only for the grant, and writes each", () => {
    const onChange = vi.fn();
    const config = { auth_mode: "oauth", oauth_grant: "password", oauth_username: "api-user", oauth_password: "[REDACTED]" };
    render(<OAuthFields config={config} onChange={onChange} endpointAuthStyle jwtBearer />);
    expect(screen.getByText(/OAuth 2\.1 removes this grant/)).toBeInTheDocument();
    const user = screen.getByLabelText("Username");
    expect(user).toHaveValue("api-user");
    const pw = screen.getByLabelText("Password");
    expect(pw).toHaveAttribute("type", "password");
    fireEvent.change(user, { target: { value: "svc" } });
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ oauth_username: "svc" }));
    fireEvent.change(pw, { target: { value: "s3cret" } });
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ oauth_password: "s3cret" }));
    expect(screen.getByText(/Optional, with a client ID/)).toBeInTheDocument();
  });

  it("shows no Username or Password for another grant", () => {
    render(<OAuthFields config={{ auth_mode: "oauth", oauth_grant: "client_credentials" }} onChange={vi.fn()} endpointAuthStyle jwtBearer />);
    expect(screen.queryByLabelText("Username")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
  });
});
