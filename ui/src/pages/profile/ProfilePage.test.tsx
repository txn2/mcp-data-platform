import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { useAuthStore } from "@/stores/auth";
import { ProfilePage } from "./ProfilePage";

afterEach(() => {
  cleanup();
  useAuthStore.setState({ user: null, authMethod: null });
});

describe("ProfilePage (#2215)", () => {
  it("shows the account, persona, roles, admin status and sign-in method", () => {
    useAuthStore.setState({
      user: {
        user_id: "u-123", email: "ada@example.com", roles: ["analyst", "dp_viewer"],
        is_admin: false, persona: "analyst", csrf_token: "secret-token",
      },
      authMethod: "cookie",
    });
    render(<ProfilePage />);
    expect(screen.getAllByText("ada@example.com")).toHaveLength(2);
    expect(screen.getByText("u-123")).toBeInTheDocument();
    expect(screen.getByText("analyst", { selector: "dd" })).toBeInTheDocument();
    expect(screen.getByText("dp_viewer")).toBeInTheDocument();
    expect(screen.getByText("No")).toBeInTheDocument();
    expect(screen.getByText("Single sign-on")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /sign out/i })).toBeInTheDocument();
    expect(screen.queryByText("secret-token")).not.toBeInTheDocument();
  });

  it("renders an API-key session with no email or persona", () => {
    useAuthStore.setState({ user: { user_id: "apikey:ci", roles: [], is_admin: true }, authMethod: "apikey" });
    render(<ProfilePage />);
    expect(screen.getAllByText("apikey:ci")).toHaveLength(2);
    expect(screen.getByText("API key")).toBeInTheDocument();
    expect(screen.getByText("Yes")).toBeInTheDocument();
    expect(screen.getAllByText("None").length).toBeGreaterThanOrEqual(3);
  });
});
