import { describe, expect, it } from "vitest";

// #1976: a value one test stores is never there for the next, and a stored
// value is there within the test that stored it, under every Node the suite
// runs on. The two cases run in order within the file.
describe("browser storage in tests", () => {
  it("keeps what a test stores for the rest of that test", () => {
    localStorage.setItem("portal.flow.view", "structure");
    sessionStorage.setItem("mcp-portal-api-key", "k");
    expect(window.localStorage.getItem("portal.flow.view")).toBe("structure");
    expect(window.sessionStorage.getItem("mcp-portal-api-key")).toBe("k");
  });

  it("opens the next test on an empty store", () => {
    expect(localStorage.getItem("portal.flow.view")).toBeNull();
    expect(sessionStorage.getItem("mcp-portal-api-key")).toBeNull();
    expect(localStorage.length).toBe(0);
  });
});
