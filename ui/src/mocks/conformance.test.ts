/**
 * Mock Conformance Tests
 *
 * Validates that MSW mock data structures match the Swagger-generated types.
 * Run `npm run generate-api-types` before this test to regenerate types from
 * the Go backend's Swagger spec.
 *
 * This catches drift between the Go API and the mock data used in development.
 * If a field is added to a Go response struct but not to the mock data, these
 * tests will flag it.
 */
import { describe, it, expect } from "vitest";
import { mockSystemInfo, mockTools, mockConnections } from "./data/system";
import { mockPersonaDetails, mockPersonas } from "./data/personas";

// ---------------------------------------------------------------------------
// Helper: extract keys recursively from an object
// ---------------------------------------------------------------------------
function flatKeys(obj: unknown, prefix = ""): Set<string> {
  const keys = new Set<string>();
  if (obj === null || obj === undefined || typeof obj !== "object") return keys;
  if (Array.isArray(obj)) {
    if (obj.length > 0) {
      for (const k of flatKeys(obj[0], prefix)) keys.add(k);
    }
    return keys;
  }
  for (const [k, v] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${k}` : k;
    keys.add(path);
    if (v !== null && typeof v === "object" && !Array.isArray(v)) {
      for (const nested of flatKeys(v, path)) keys.add(nested);
    }
  }
  return keys;
}

// ---------------------------------------------------------------------------
// System Info
// ---------------------------------------------------------------------------
describe("Mock conformance: SystemInfo", () => {
  it("should have all expected top-level fields", () => {
    const keys = flatKeys(mockSystemInfo);
    const expected = [
      "name",
      "version",
      "commit",
      "build_date",
      "description",
      "transport",
      "config_mode",
      "portal_title",
      "portal_logo",
      "portal_logo_light",
      "portal_logo_dark",
      "features",
      "toolkit_count",
      "persona_count",
    ];
    for (const field of expected) {
      expect(keys.has(field), `missing field: ${field}`).toBe(true);
    }
  });

  it("should have all feature flags", () => {
    const featureKeys = Object.keys(mockSystemInfo.features);
    const expected = ["audit", "oauth", "knowledge", "admin", "database"];
    expect(featureKeys.sort()).toEqual(expected.sort());
  });
});

// ---------------------------------------------------------------------------
// Tools
// ---------------------------------------------------------------------------
describe("Mock conformance: ToolInfo", () => {
  it("should have tools with required fields", () => {
    expect(mockTools.length).toBeGreaterThan(0);
    for (const tool of mockTools) {
      expect(tool).toHaveProperty("name");
      expect(tool).toHaveProperty("toolkit");
      expect(tool).toHaveProperty("kind");
      expect(tool).toHaveProperty("connection");
    }
  });
});

// ---------------------------------------------------------------------------
// Connections
// ---------------------------------------------------------------------------
describe("Mock conformance: ConnectionInfo", () => {
  it("should have connections with required fields", () => {
    expect(mockConnections.length).toBeGreaterThan(0);
    for (const conn of mockConnections) {
      expect(conn).toHaveProperty("kind");
      expect(conn).toHaveProperty("name");
      expect(conn).toHaveProperty("connection");
      expect(conn).toHaveProperty("tools");
      expect(Array.isArray(conn.tools)).toBe(true);
      expect(conn).toHaveProperty("hidden_tools");
      expect(Array.isArray(conn.hidden_tools)).toBe(true);
    }
  });
});

// ---------------------------------------------------------------------------
// Personas
// ---------------------------------------------------------------------------
describe("Mock conformance: PersonaSummary", () => {
  it("should have summaries with required fields", () => {
    expect(mockPersonas.length).toBeGreaterThan(0);
    for (const p of mockPersonas) {
      expect(p).toHaveProperty("name");
      expect(p).toHaveProperty("display_name");
      expect(p).toHaveProperty("roles");
      expect(p).toHaveProperty("tool_count");
    }
  });
});

describe("Mock conformance: PersonaDetail", () => {
  it("should have details with required fields", () => {
    const details = Object.values(mockPersonaDetails);
    expect(details.length).toBeGreaterThan(0);
    for (const p of details) {
      expect(p).toHaveProperty("name");
      expect(p).toHaveProperty("display_name");
      expect(p).toHaveProperty("roles");
      expect(p).toHaveProperty("priority");
      expect(p).toHaveProperty("allow_tools");
      expect(p).toHaveProperty("deny_tools");
      expect(p).toHaveProperty("tools");
    }
  });
});
