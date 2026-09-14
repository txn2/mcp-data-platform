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
