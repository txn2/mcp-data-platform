import { describe, it, expect, beforeEach } from "vitest";
import { applyCsrfHeader, CSRF_HEADER } from "./csrf";
import { useAuthStore } from "@/stores/auth";

describe("applyCsrfHeader", () => {
  beforeEach(() => {
    useAuthStore.setState({ authMethod: null, csrfToken: "", apiKey: "" });
  });

  it("adds the token for state-changing methods under cookie auth", () => {
    useAuthStore.setState({ authMethod: "cookie", csrfToken: "tok-123" });
    for (const method of ["POST", "PUT", "PATCH", "DELETE"]) {
      const headers: Record<string, string> = {};
      applyCsrfHeader(headers, method);
      expect(headers[CSRF_HEADER]).toBe("tok-123");
    }
  });

  it("does not add the token for safe methods", () => {
    useAuthStore.setState({ authMethod: "cookie", csrfToken: "tok-123" });
    for (const method of ["GET", "HEAD", "OPTIONS", undefined]) {
      const headers: Record<string, string> = {};
      applyCsrfHeader(headers, method);
      expect(headers[CSRF_HEADER]).toBeUndefined();
    }
  });

  it("does not add the token under API-key auth (exempt)", () => {
    useAuthStore.setState({ authMethod: "apikey", csrfToken: "", apiKey: "k" });
    const headers: Record<string, string> = {};
    applyCsrfHeader(headers, "POST");
    expect(headers[CSRF_HEADER]).toBeUndefined();
  });

  it("does not add an empty token even under cookie auth", () => {
    useAuthStore.setState({ authMethod: "cookie", csrfToken: "" });
    const headers: Record<string, string> = {};
    applyCsrfHeader(headers, "POST");
    expect(headers[CSRF_HEADER]).toBeUndefined();
  });

  it("is case-insensitive on the method", () => {
    useAuthStore.setState({ authMethod: "cookie", csrfToken: "tok-123" });
    const headers: Record<string, string> = {};
    applyCsrfHeader(headers, "post");
    expect(headers[CSRF_HEADER]).toBe("tok-123");
  });
});
