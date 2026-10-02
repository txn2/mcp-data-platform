import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

import { SessionLoginAuthFields } from "./SessionLoginAuthFields";

// A Tableau connection as the admin API returns it: the secret redacted, the
// body and every other key as written (#2015).
const tableau = {
  auth_mode: "session_login",
  session_login_url: "/api/3.22/auth/signin",
  session_login_body: '{"credentials":{"personalAccessTokenSecret":"{{secret}}"}}',
  session_login_secret: "[REDACTED]",
  session_token_source: "body:credentials.token",
  session_token_header: "X-Tableau-Auth",
  session_logout_url: "/api/3.22/auth/signout",
  session_capture: { site_id: "body:credentials.site.id" },
  session_expired_statuses: [401, 403],
};

function renderFields(config: Record<string, unknown>) {
  const onChange = vi.fn();
  render(<SessionLoginAuthFields config={config} onChange={onChange} />);
  return onChange;
}

describe("SessionLoginAuthFields", () => {
  it("shows every stored value in its field", () => {
    renderFields(tableau);
    expect(screen.getByLabelText(/Sign-in URL/)).toHaveValue("/api/3.22/auth/signin");
    expect(screen.getByLabelText(/^Sign-in body$/)).toHaveValue(tableau.session_login_body);
    expect(screen.getByLabelText(/^Secret$/)).toHaveValue("[REDACTED]");
    expect(screen.getByLabelText(/Token location/)).toHaveValue("body:credentials.token");
    expect(screen.getByLabelText(/^Token header$/)).toHaveValue("X-Tableau-Auth");
    expect(screen.getByLabelText(/^Sign-out URL$/)).toHaveValue("/api/3.22/auth/signout");
    expect(screen.getByLabelText(/^Expired statuses$/)).toHaveValue("401, 403");
    expect(screen.getByText("site_id")).toBeInTheDocument();
    expect(screen.getByText("body:credentials.site.id")).toBeInTheDocument();
  });

  it("keeps the secret field masked", () => {
    renderFields(tableau);
    expect(screen.getByLabelText(/^Secret$/)).toHaveAttribute("type", "password");
  });

  it("saves the statuses as typed, comma included", () => {
    const onChange = renderFields(tableau);
    fireEvent.change(screen.getByLabelText(/^Expired statuses$/), { target: { value: "401," } });
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ session_expired_statuses: "401," }));
  });

  it("removes a key the operator clears", () => {
    const onChange = renderFields(tableau);
    fireEvent.change(screen.getByLabelText(/^Token header$/), { target: { value: "" } });
    expect(onChange.mock.lastCall?.[0]).not.toHaveProperty("session_token_header");
    fireEvent.change(screen.getByLabelText(/^Expired statuses$/), { target: { value: " " } });
    expect(onChange.mock.lastCall?.[0]).not.toHaveProperty("session_expired_statuses");
  });

  it("reads statuses saved as text and shows defaults for an empty connection", () => {
    renderFields({ auth_mode: "session_login", session_expired_statuses: "419" });
    expect(screen.getByLabelText(/^Expired statuses$/)).toHaveValue("419");
    expect(screen.getByLabelText(/Token location/)).toHaveValue("");
  });

  it("drops the captured-values key when its last entry is removed", () => {
    const onChange = renderFields(tableau);
    fireEvent.click(screen.getByRole("button", { name: "Remove site_id" }));
    expect(onChange.mock.lastCall?.[0]).not.toHaveProperty("session_capture");
  });
});
