import { describe, it, expect } from "vitest";
import fs from "fs";
import path from "path";
import { routes } from "./route-manifest";

describe("route manifest sync", () => {
  it("covers all pageTitles routes from AppShell", () => {
    const appShellPath = path.resolve(
      __dirname,
      "../../src/components/layout/AppShell.tsx",
    );
    const source = fs.readFileSync(appShellPath, "utf-8");

    const pageTitlesMatch = source.match(
      /const pageTitles[^{]*\{([^}]+)\}/s,
    );
    expect(pageTitlesMatch).toBeTruthy();

    const pageTitlesBlock = pageTitlesMatch![1]!;
    const routeKeys = [...pageTitlesBlock.matchAll(/"([^"]+)":/g)].map(
      (m) => m[1]!,
    );

    const manifestPaths = new Set(
      routes.map((r) => {
        const stripped = r.path.replace("/portal", "");
        return stripped || "/";
      }),
    );

    const missing: string[] = [];
    for (const key of routeKeys) {
      if (!manifestPaths.has(key)) {
        missing.push(key);
      }
    }

    expect(
      missing,
      `Routes in AppShell pageTitles missing from screenshot manifest: ${missing.join(", ")}. Add entries to route-manifest.ts.`,
    ).toEqual([]);
  });

  it("has no duplicate slugs", () => {
    const slugs = routes.map((r) => r.slug);
    const duplicates = slugs.filter(
      (s, i) => slugs.indexOf(s) !== i,
    );
    expect(duplicates, "Duplicate slugs found").toEqual([]);
  });
});
