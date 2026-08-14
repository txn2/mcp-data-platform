import { describe, it, expect } from "vitest";
import type { ScriptGrants, VersionReview } from "@/api/admin/types";
import { EMPTY_GRANT, grantDelta, proposedGrant, toggle, widensAuthority } from "./scriptGrants";

function review(overrides: Partial<VersionReview> = {}): VersionReview {
  return {
    version: {
      id: "sver_1",
      script_id: "script_1",
      version: 2,
      display_name: "Daily Sales",
      description: "",
      source: "",
      author: "jane@example.com",
      author_roles: ["analyst"],
      status: "draft",
      grants: EMPTY_GRANT,
      created_at: "2026-08-10T09:00:00Z",
    },
    referenced: {
      capabilities: ["platform.query"],
      connections: ["warehouse"],
      dynamic_connections: false,
    },
    ...overrides,
  };
}

describe("proposedGrant", () => {
  it("opens on what the code reaches for", () => {
    const grant = proposedGrant(review());
    expect(grant.capabilities).toEqual(["platform.query"]);
    expect(grant.connections).toEqual(["warehouse"]);
    expect(grant.roles).toEqual(["analyst"]);
  });

  it("grants the portal destination when the code exports", () => {
    const grant = proposedGrant(
      review({
        referenced: {
          capabilities: ["platform.query", "platform.export"],
          connections: [],
          dynamic_connections: false,
        },
      }),
    );
    expect(grant.destinations).toEqual(["portal"]);
  });

  it("leaves the destination empty when nothing is exported", () => {
    expect(proposedGrant(review()).destinations).toEqual([]);
  });

  it("keeps what an earlier approval of this version already bound", () => {
    const r = review();
    r.version.grants = {
      roles: ["analyst"],
      connections: ["reporting"],
      capabilities: ["platform.export"],
      destinations: ["portal"],
    };
    const grant = proposedGrant(r);
    expect(grant.connections).toEqual(["reporting", "warehouse"]);
    expect(grant.capabilities).toEqual(["platform.export", "platform.query"]);
  });
});

describe("grantDelta", () => {
  it("separates what is added, removed, and unchanged", () => {
    const delta = grantDelta(["a", "b"], ["b", "c"]);
    expect(delta).toEqual({ added: ["c"], removed: ["a"], unchanged: ["b"] });
  });

  it("treats an absent previous grant as nothing held", () => {
    expect(grantDelta(undefined, ["a"]).added).toEqual(["a"]);
  });
});

describe("widensAuthority", () => {
  const approved: ScriptGrants = {
    roles: ["analyst"],
    connections: ["warehouse"],
    capabilities: ["platform.query"],
    destinations: [],
  };

  it("is true when the proposal reaches somewhere new", () => {
    expect(
      widensAuthority(approved, { ...approved, capabilities: ["platform.query", "platform.export"] }),
    ).toBe(true);
  });

  it("is false when the proposal only narrows", () => {
    expect(widensAuthority(approved, { ...approved, connections: [] })).toBe(false);
  });

  it("is true for a first approval that grants anything at all", () => {
    expect(widensAuthority(undefined, approved)).toBe(true);
  });

  it("is false for a first approval that grants nothing", () => {
    expect(widensAuthority(undefined, EMPTY_GRANT)).toBe(false);
  });
});

describe("toggle", () => {
  it("adds a value and keeps the axis sorted", () => {
    expect(toggle(["b"], "a")).toEqual(["a", "b"]);
  });

  it("removes a value that is already granted", () => {
    expect(toggle(["a", "b"], "a")).toEqual(["b"]);
  });
});
