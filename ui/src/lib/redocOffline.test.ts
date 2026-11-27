import fs from "fs";
import path from "path";
import { describe, expect, it } from "vitest";

import {
  FETCHED_HOSTS,
  REDOC_THIRD_PARTY,
  isRedocModule,
  redocOffline,
  stripRedocThirdParty,
} from "../../redocOfflinePlugin";

// The installed ReDoc, read as the build reads it: the test is about the bytes
// this lockfile ships, so a ReDoc upgrade that adds a host fails here (#2769).
const BUNDLE = path.resolve(__dirname, "../../node_modules/redoc/bundles/redoc.js");

describe("ReDoc's third-party fetches", () => {
  it("are all gone from the installed bundle once it is rewritten", () => {
    const source = fs.readFileSync(BUNDLE, "utf8");
    // The bundle as published does fetch from them: otherwise this test
    // would pass against a rewrite that did nothing.
    expect(FETCHED_HOSTS.some((h) => source.includes(h))).toBe(true);

    const out = stripRedocThirdParty(source);
    for (const host of FETCHED_HOSTS) {
      expect(out, host).not.toContain(host);
    }
    // The links a reader follows on purpose stay: they are not fetches.
    expect(out).toContain("https://redocly.com/learn/openapi/");
  });

  it("leaves no logo request and no telemetry collector behind", () => {
    const logo = REDOC_THIRD_PARTY.find(([from]) => from.endsWith("logo-mini-full.svg"));
    expect(logo?.[1]).toMatch(/^data:image\/svg\+xml,/);
    const collector = REDOC_THIRD_PARTY.find(([from]) => from.includes("otel."));
    // Empty, so ReDoc's tracer has no address and is never built.
    expect(collector?.[1]).toBe("");
  });

  it("fails the build when ReDoc fetches from a host it does not know", () => {
    const upgraded = 'img({src:"https://cdn.redoc.ly/redoc/new-logo.svg"})';
    expect(() => stripRedocThirdParty(upgraded)).toThrow(/cdn\.redoc\.ly/);
  });

  it("rewrites ReDoc's bundle and nothing else", () => {
    expect(isRedocModule("/x/node_modules/redoc/bundles/redoc.js")).toBe(true);
    expect(isRedocModule("C:\\x\\node_modules\\redoc\\bundles\\redoc.standalone.js")).toBe(true);
    expect(isRedocModule("/x/node_modules/redoc/bundles/redoc.js?v=1a2b")).toBe(true);
    expect(isRedocModule("/x/node_modules/other/redoc.js")).toBe(false);
    expect(isRedocModule("/x/src/pages/api-reference/redocTheme.ts")).toBe(false);

    const plugin = redocOffline();
    const transform = plugin.transform as (code: string, id: string) => { code: string } | null;
    expect(transform('"https://cdn.redoc.ly/redoc/logo-mini.svg"', "/a/src/x.ts")).toBeNull();
    const done = transform(
      '"https://cdn.redoc.ly/redoc/logo-mini.svg"',
      "/a/node_modules/redoc/bundles/redoc.js",
    );
    expect(done?.code).not.toContain("cdn.redoc.ly");
  });
});
