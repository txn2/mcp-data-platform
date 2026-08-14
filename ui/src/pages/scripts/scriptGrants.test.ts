import { describe, it, expect } from "vitest";
import type { ScriptGrants, VersionReview } from "@/api/admin/types";
import {
  destinationKey,
  destinationKeys,
  EMPTY_GRANT,
  grantDelta,
  incompleteDestinations,
  portalDestination,
  proposedGrant,
  toggle,
  widensAuthority,
} from "./scriptGrants";

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
      destinations: [],
      dynamic_connections: false,
      dynamic_destinations: false,
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
          destinations: ["portal"],
          dynamic_connections: false,
          dynamic_destinations: false,
        },
      }),
    );
    expect(grant.destinations).toEqual([portalDestination()]);
  });

  it("proposes a bucket destination with no address, so the reviewer supplies one", () => {
    const grant = proposedGrant(
      review({
        referenced: {
          capabilities: ["platform.query", "platform.export"],
          connections: [],
          destinations: ["acme-drop", "portal"],
          dynamic_connections: false,
          dynamic_destinations: false,
        },
      }),
    );
    expect(grant.destinations).toEqual([
      { name: "acme-drop", kind: "s3" },
      portalDestination(),
    ]);
    expect(incompleteDestinations(grant)).toEqual(["acme-drop"]);
  });

  it("keeps the address an earlier approval already bound", () => {
    const r = review({
      referenced: {
        capabilities: ["platform.export"],
        connections: [],
        destinations: ["acme-drop"],
        dynamic_connections: false,
        dynamic_destinations: false,
      },
    });
    r.version.grants = {
      roles: ["analyst"],
      connections: [],
      capabilities: ["platform.export"],
      destinations: [
        { name: "acme-drop", kind: "s3", connection: "acme-s3", bucket: "exports", prefix: "weekly" },
      ],
    };
    const grant = proposedGrant(r);
    expect(grant.destinations).toHaveLength(1);
    expect(grant.destinations[0]!.bucket).toBe("exports");
    expect(incompleteDestinations(grant)).toEqual([]);
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
      destinations: [portalDestination()],
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

  // A destination repointed at another bucket is new authority even though its
  // name did not change: a diff taken over names alone would report no change
  // while the data started going somewhere else.
  it("is true when a destination keeps its name and changes its address", () => {
    const held: ScriptGrants = {
      ...approved,
      destinations: [
        { name: "acme-drop", kind: "s3", connection: "acme-s3", bucket: "exports", prefix: "weekly" },
      ],
    };
    const repointed: ScriptGrants = {
      ...held,
      destinations: [
        { name: "acme-drop", kind: "s3", connection: "acme-s3", bucket: "somewhere-else", prefix: "weekly" },
      ],
    };
    expect(widensAuthority(held, repointed)).toBe(true);
    expect(widensAuthority(held, held)).toBe(false);
  });
});

describe("destinationKey", () => {
  it("renders the portal as its name, because it has no address", () => {
    expect(destinationKey(portalDestination())).toBe("portal");
  });

  it("renders a bucket destination as the address a reviewer is agreeing to", () => {
    expect(
      destinationKey({
        name: "acme-drop",
        kind: "s3",
        connection: "acme-s3",
        bucket: "acme-exports",
        prefix: "weekly",
      }),
    ).toBe("acme-drop -> s3 acme-s3 acme-exports/weekly");
  });

  it("reads a retyped prefix as the grant the server will store", () => {
    const stored = destinationKey({
      name: "acme-drop",
      kind: "s3",
      connection: "acme-s3",
      bucket: "acme-exports",
      prefix: "weekly",
    });
    const retyped = destinationKey({
      name: "acme-drop",
      kind: "s3",
      connection: " acme-s3 ",
      bucket: "acme-exports",
      prefix: "/weekly/",
    });
    expect(retyped).toBe(stored);
  });

  it("renders a whole axis", () => {
    expect(destinationKeys([portalDestination()])).toEqual(["portal"]);
    expect(destinationKeys(undefined)).toEqual([]);
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
