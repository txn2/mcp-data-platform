import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, it, expect } from "vitest";
import { canonicalRoute, isAdminRoute, isKnownRoute } from "./portalRoutes";

describe("isKnownRoute", () => {
  it("recognizes a section index and its detail alike", () => {
    expect(isKnownRoute("/scripts")).toBe(true);
    expect(isKnownRoute("/scripts/script-001")).toBe(true);
    expect(isKnownRoute("/admin/calls/call-1")).toBe(true);
    expect(isKnownRoute("/collections/c-1/assets/a-1")).toBe(true);
  });

  // The bug this module exists for: a path that renders no page and no request.
  it("refuses a path the shell renders nothing for", () => {
    expect(isKnownRoute("/assets")).toBe(false);
    expect(isKnownRoute("/knowledge/tags")).toBe(false);
    expect(isKnownRoute("/admin/nonesuch")).toBe(false);
  });

  // A section index with a trailing slash is not the detail of a record whose
  // id is the empty string, and must not be treated as one.
  it("does not read a trailing slash as an identifier", () => {
    expect(isKnownRoute("/scripts/")).toBe(false);
    expect(isKnownRoute("/assets/")).toBe(false);
  });

  it("does not let a detail pattern swallow a deeper path", () => {
    expect(isKnownRoute("/collections/c-1/nonesuch")).toBe(false);
  });
});

describe("canonicalRoute", () => {
  it("sends a surface that moved to where it lives now", () => {
    expect(canonicalRoute("/shared")).toBe("/");
    expect(canonicalRoute("/knowledge-pages")).toBe("/knowledge#knowledge");
    expect(canonicalRoute("/my-knowledge")).toBe("/knowledge#insights");
    expect(canonicalRoute("/admin/knowledge")).toBe("/knowledge#insights");
  });

  // The reported path (#1359). Assets are mounted at the portal root, so the
  // name the section carries everywhere else is a URL somebody will type.
  it("sends the guessed assets path to the page the assets are on", () => {
    expect(canonicalRoute("/assets")).toBe("/");
    expect(canonicalRoute("/assets/")).toBe("/");
  });

  it("drops a trailing slash from a route that exists without one", () => {
    expect(canonicalRoute("/scripts/")).toBe("/scripts");
    expect(canonicalRoute("/admin/tools/")).toBe("/admin/tools");
    expect(canonicalRoute("/collections/c-1/")).toBe("/collections/c-1");
  });

  it("leaves a route that is already canonical alone", () => {
    expect(canonicalRoute("/")).toBeNull();
    expect(canonicalRoute("/scripts")).toBeNull();
    expect(canonicalRoute("/scripts/script-001")).toBeNull();
  });

  // An unknown path is a not-found page. Redirecting it would land the reader
  // somewhere they did not ask for and tell them nothing about what failed.
  it("does not invent a destination for a path with no page", () => {
    expect(canonicalRoute("/nonesuch")).toBeNull();
    expect(canonicalRoute("/nonesuch/")).toBeNull();
  });
});

describe("isAdminRoute", () => {
  it("claims the admin section and nothing that merely starts with its letters", () => {
    expect(isAdminRoute("/admin")).toBe(true);
    expect(isAdminRoute("/admin/tools")).toBe(true);
    expect(isAdminRoute("/administrators")).toBe(false);
    expect(isAdminRoute("/scripts")).toBe(false);
  });
});

// The drift gate. This module decides whether a path renders a page or renders
// "no such page", and it is a hand-kept list, so a route added to the shell and
// not added here would turn a working page into a refusal — the same class of
// silent wrong answer that #1359 is about, pointed the other way.
//
// So the routes are read back out of the source that renders them. Every path
// the shell or a section component matches on has to be one this module knows.
describe("the routes the shell renders", () => {
  const SOURCES = [
    "../components/layout/AppShell.tsx",
    "../pages/activity/ActivityRoutes.tsx",
    "../pages/activity/routes.ts",
    "../pages/calls/CallRoutes.tsx",
    "../pages/collections/AdminCollectionRoutes.tsx",
    "../pages/scripts/ScriptRoutes.tsx",
    "../pages/sessions/SessionRoutes.tsx",
  ];

  // Comments are stripped before the scan: they discuss routes ("the matches
  // below are exact, route === \"/x\"") and a gate that reads prose as source
  // fails on the prose rather than on a real gap.
  function read(rel: string): string {
    const text = readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");
    return text.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
  }

  // sampleOf turns a route pattern into a path that matches it: the capture is
  // an identifier, and an identifier is what a reader's URL carries.
  function sampleOf(pattern: string): string {
    return pattern
      .replace(/^\/\^/, "")
      .replace(/\$\/$/, "")
      .replace(/\\\//g, "/")
      .replace(/\(\[\^\/\]\+\)|\(\.\+\)|\[\^\/\]\+|\.\+/g, "sample");
  }

  const literals = new Set<string>();
  const samples = new Set<string>();
  for (const source of SOURCES) {
    const text = read(source);
    // `route === "/x"`, and the route constants the Activity section exports.
    for (const m of text.matchAll(/route === "(\/[^"]*)"/g)) literals.add(m[1]!);
    for (const m of text.matchAll(/^export const [A-Z_]+ = "(\/[^"]*)";$/gm)) {
      literals.add(m[1]!);
    }
    // The detail patterns, which are always anchored regex literals.
    for (const m of text.matchAll(/\/\^\\\/[^\n]*?\$\//g)) samples.add(sampleOf(m[0]!));
  }

  it("finds the routes it is checking, so an empty pass cannot look green", () => {
    expect(literals.size).toBeGreaterThan(20);
    expect(samples.size).toBeGreaterThan(8);
  });

  it.each([...literals].sort())("renders %s, so the table knows it", (route) => {
    expect(isKnownRoute(route)).toBe(true);
  });

  it.each([...samples].sort())("renders %s, so the table knows it", (route) => {
    expect(isKnownRoute(route)).toBe(true);
  });
});
