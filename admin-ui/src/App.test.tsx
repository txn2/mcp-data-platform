import { render, screen } from "@testing-library/react";
import { describe, it, expect, beforeEach } from "vitest";
import { App } from "./App";
import { useAuthStore } from "@/stores/auth";

describe("App", () => {
  beforeEach(() => {
    sessionStorage.clear();
    useAuthStore.getState().clearApiKey();
  });

  it("renders login form when not authenticated", () => {
    render(<App />);
    expect(screen.getByPlaceholderText("API Key")).toBeInTheDocument();
    expect(screen.getByText("Sign In")).toBeInTheDocument();
  });

  it("renders app shell when authenticated", () => {
    useAuthStore.getState().setApiKey("test-key");
    render(<App />);
    expect(screen.getByText("Admin Portal")).toBeInTheDocument();
    // "Home" appears in both sidebar and header
    expect(screen.getAllByText("Home")).toHaveLength(2);
    expect(screen.getByText("Tools")).toBeInTheDocument();
    expect(screen.getByText("Audit Log")).toBeInTheDocument();
    expect(screen.getByText("Knowledge")).toBeInTheDocument();
    expect(screen.getByText("Personas")).toBeInTheDocument();
    expect(screen.getByText("Sign Out")).toBeInTheDocument();
  });
});
