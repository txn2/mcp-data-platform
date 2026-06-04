import { describe, it, expect } from "vitest";

import { resolve } from "./PersonaEditor";

// resolve mirrors pkg/persona/filter.go: both tools and connections are
// deny-by-default. The bug these tests guard: an empty allow-list must NOT
// be shown as "allowed" — neither axis has an allow-all shortcut.
describe("resolve — deny-by-default allow/deny preview", () => {
  it("empty allow list denies (no allow-all shortcut)", () => {
    expect(resolve("platform-admin", [], []).decision).toBe("default-deny");
    expect(resolve("acme", [], []).decision).toBe("default-deny");
  });

  it("an explicit allow match permits", () => {
    expect(resolve("platform-admin", ["*"], []).decision).toBe("allow");
    expect(resolve("acme", ["acme"], []).decision).toBe("allow");
  });

  it("an allow pattern that does not match still denies", () => {
    expect(resolve("staging", ["dev-*"], []).decision).toBe("default-deny");
  });

  it("deny takes precedence over a matching allow", () => {
    const r = resolve("prod-trino", ["*"], ["prod-*"]);
    expect(r.decision).toBe("deny");
  });
});
