import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, it, expect } from "vitest";
import { canonicalRoute, isAdminRoute, isKnownRoute, redirectFor } from "./portalRoutes";

describe("isKnownRoute", () => {
  it("recognizes a section index and its detail alike", () => {
    expect(isKnownRoute("/automations")).toBe(true);
    expect(isKnownRoute("/automations/script-001")).toBe(true);
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
    expect(isKnownRoute("/automations/")).toBe(false);
    expect(isKnownRoute("/assets/")).toBe(false);
  });

  it("does not let a detail pattern swallow a deeper path", () => {
    expect(isKnownRoute("/collections/c-1/nonesuch")).toBe(false);
    expect(isKnownRoute("/automations/script-001/nonesuch")).toBe(false);
    expect(isKnownRoute("/admin/automations/script-001/nonesuch")).toBe(false);
  });

  // One managed resource (#1470), in both sections that list resources. A
  // resource had no address at all before: it opened in a dialog over the
  // library, so it could not be linked to, bookmarked or reloaded.
  it("recognizes one resource in either section", () => {
    expect(isKnownRoute("/resources/res-1")).toBe(true);
    expect(isKnownRoute("/admin/resources/res-1")).toBe(true);
    expect(isKnownRoute("/resources/res-1/nonesuch")).toBe(false);
    expect(isKnownRoute("/resources/")).toBe(false);
  });

  // One run of one script (#1405), which is the address the cross-script Runs
  // listing links to.
  it("recognizes a run under its script", () => {
    expect(isKnownRoute("/automations/script-001/runs/run-042")).toBe(true);
    // The administrator's section links to the same shape (#1407).
    expect(isKnownRoute("/admin/automations/script-001/runs/run-042")).toBe(true);
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
    expect(canonicalRoute("/automations/")).toBe("/automations");
    expect(canonicalRoute("/admin/tools/")).toBe("/admin/tools");
    expect(canonicalRoute("/collections/c-1/")).toBe("/collections/c-1");
  });

  it("leaves a route that is already canonical alone", () => {
    expect(canonicalRoute("/")).toBeNull();
    expect(canonicalRoute("/automations")).toBeNull();
    expect(canonicalRoute("/automations/script-001")).toBeNull();
  });

  // #1912: the section moved, and a link built under the old prefix (a mailed
  // failed-run link, a show_scripts URL, a bookmark) lands on the same page.
  it("sends every path under the old scripts section to the same path under automations", () => {
    expect(canonicalRoute("/scripts")).toBe("/automations");
    expect(canonicalRoute("/scripts/script-001")).toBe("/automations/script-001");
    expect(canonicalRoute("/scripts/script-001/runs/run-042")).toBe(
      "/automations/script-001/runs/run-042",
    );
    expect(canonicalRoute("/admin/scripts")).toBe("/admin/automations");
    expect(canonicalRoute("/admin/scripts/script-001")).toBe("/admin/automations/script-001");
    expect(canonicalRoute("/admin/scripts/script-001/runs/run-042")).toBe(
      "/admin/automations/script-001/runs/run-042",
    );
    expect(canonicalRoute("/scripts/")).toBe("/automations");
    // A prefix is a whole segment: a path that merely starts with the letters
    // is not under the section.
    expect(canonicalRoute("/scriptsx")).toBeNull();
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
    expect(isAdminRoute("/automations")).toBe(false);
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
    "../components/layout/AdminPages.tsx",
    "../pages/activity/ActivityRoutes.tsx",
    "../pages/activity/routes.ts",
    "../pages/calls/CallRoutes.tsx",
    "../pages/collections/AdminCollectionRoutes.tsx",
    "../pages/scripts/AdminScriptRoutes.tsx",
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
    // The admin section resolves an exact path through a keyed table rather
    // than a run of comparisons, so its keys are routes too.
    for (const m of text.matchAll(/^\s*\["(\/[^"]*)",/gm)) literals.add(m[1]!);
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

describe("redirectFor", () => {
  it("carries the query string and hash of a moved path to its new place", () => {
    expect(redirectFor("/scripts/script-001/runs/run-042?x=1#source")).toBe(
      "/automations/script-001/runs/run-042?x=1#source",
    );
    expect(redirectFor("/admin/scripts#runs")).toBe("/admin/automations#runs");
  });

  it("lets a target that names its own tab keep it", () => {
    expect(redirectFor("/my-knowledge?x=1")).toBe("/knowledge#insights");
  });

  it("answers null for a path rendered where it is", () => {
    expect(redirectFor("/automations/script-001?x=1")).toBeNull();
  });
});
