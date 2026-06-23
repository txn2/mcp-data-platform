import { describe, it, expect } from "vitest";
import fs from "fs";
import path from "path";
import { routes, excludedRoutes } from "./route-manifest";

function readSource(rel: string): string {
  return fs.readFileSync(path.resolve(__dirname, rel), "utf-8");
}

/** Normalize a manifest path to its bare in-app route (no /portal, query, or hash). */
function bareRoute(p: string): string {
  const stripped = p
    .replace("/portal", "")
    .split("?")[0]!
    .split("#")[0]!;
  return stripped || "/";
}

/**
 * Extract the members of a string-literal union type from source, e.g.
 *   type Tab = "mcp" | "events";
 *   export type ToolDetailTab = "overview" | "tryit";
 * Returns ["mcp", "events"] / ["overview", "tryit"].
 */
function extractUnionMembers(source: string, typeName: string): string[] {
  const re = new RegExp(`type ${typeName}\\s*=\\s*([^;]+);`, "s");
  const m = source.match(re);
  if (!m) return [];
  return [...m[1]!.matchAll(/"([^"]+)"/g)].map((x) => x[1]!);
}

describe("route manifest sync", () => {
  it("covers all pageTitles routes from AppShell", () => {
    const source = readSource("../../src/components/layout/AppShell.tsx");

    const pageTitlesMatch = source.match(/const pageTitles[^{]*\{([^}]+)\}/s);
    expect(pageTitlesMatch).toBeTruthy();

    const routeKeys = [
      ...pageTitlesMatch![1]!.matchAll(/"([^"]+)":/g),
    ].map((m) => m[1]!);

    const manifestPaths = new Set(routes.map((r) => bareRoute(r.path)));

    const missing: string[] = [];
    for (const key of routeKeys) {
      // A route is "covered" if it's either in the live manifest OR
      // explicitly in the excludedRoutes set (with a documented reason).
      if (!manifestPaths.has(key) && !excludedRoutes.has(key)) {
        missing.push(key);
      }
    }

    expect(
      missing,
      `Routes in AppShell pageTitles missing from screenshot manifest: ${missing.join(", ")}. Add entries to route-manifest.ts (or to excludedRoutes if intentionally not captured).`,
    ).toEqual([]);
  });

  it("has no duplicate slugs", () => {
    const slugs = routes.map((r) => r.slug);
    const duplicates = slugs.filter((s, i) => slugs.indexOf(s) !== i);
    expect(duplicates, "Duplicate slugs found").toEqual([]);
  });

  // Tab drift is the gap the pageTitles check above cannot see: a page can be
  // in the manifest while its tabs silently grow. These two checks read the
  // real tab keys from source and assert the manifest captures every one.

  it("captures every tab of hash-routed multi-tab pages", () => {
    const checks = [
      {
        file: "../../src/pages/audit/AuditLogPage.tsx",
        typeName: "Tab",
        routePath: "/portal/admin/audit",
      },
      {
        file: "../../src/pages/knowledge/KnowledgeHub.tsx",
        typeName: "Tab",
        routePath: "/portal/knowledge",
      },
    ];

    for (const c of checks) {
      const tabs = extractUnionMembers(readSource(c.file), c.typeName);
      expect(
        tabs.length,
        `No "type ${c.typeName}" string-literal union found in ${c.file}`,
      ).toBeGreaterThan(0);

      const route = routes.find((r) => r.path === c.routePath);
      expect(route, `Missing manifest route ${c.routePath}`).toBeTruthy();

      const captured = new Set(route!.tabs ?? []);
      const missing = tabs.filter((t) => !captured.has(t));
      expect(
        missing,
        `${c.routePath} is missing tabs ${missing.join(", ")}. Update its \`tabs\` in route-manifest.ts (or remove the tab from the page).`,
      ).toEqual([]);
    }
  });

  it("captures every ToolDetail tab as a query-string route", () => {
    const tabs = extractUnionMembers(
      readSource("../../src/pages/tools/ToolDetail.tsx"),
      "ToolDetailTab",
    );
    expect(tabs.length, "No ToolDetailTab union found").toBeGreaterThan(0);

    // ToolsPage keeps the active tab in a `?tab=` search param (the default
    // "overview" tab omits it), so each tab is its own manifest route.
    const toolRoutes = routes.filter((r) =>
      r.path.startsWith("/portal/admin/tools"),
    );

    const missing = tabs.filter((t) => {
      const covered =
        t === "overview"
          ? toolRoutes.some((r) => !r.path.includes("tab="))
          : toolRoutes.some((r) => r.path.includes(`tab=${t}`));
      return !covered;
    });

    expect(
      missing,
      `ToolDetail tabs with no screenshot route: ${missing.join(", ")}. Add a /portal/admin/tools?...&tab=<tab> entry to route-manifest.ts.`,
    ).toEqual([]);
  });
});
