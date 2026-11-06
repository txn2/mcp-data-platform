import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";
import type { Plugin } from "vite";

const here = path.dirname(fileURLToPath(import.meta.url));

/**
 * Writes boot-recovery.js inline into index.html directly after the charset
 * meta, above the entry and preload tags the build injects (#2495). Inline
 * because it is what recovers when those hashed files answer 404 during a
 * rolling update, and a file of its own could answer 404 the same way. The
 * build fails rather than shipping a page without it.
 */
export function bootRecovery(): Plugin {
  let assets = "/assets/";
  return {
    name: "boot-recovery",
    configResolved(config) {
      assets = `${config.base}${config.build.assetsDir}/`;
    },
    transformIndexHtml: {
      order: "pre",
      handler(html) {
        const anchor = /<meta charset="UTF-8" \/>/i;
        if (!anchor.test(html)) {
          throw new Error("boot-recovery: index.html has no charset meta to place the recovery script after");
        }
        const source = fs
          .readFileSync(path.join(here, "boot-recovery.js"), "utf8")
          .replace('"__ASSETS__"', JSON.stringify(assets));
        return html.replace(anchor, (m) => `${m}\n    <script id="portal-boot-recovery">\n${source}</script>`);
      },
    },
  };
}
