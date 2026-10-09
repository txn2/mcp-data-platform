import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "path";
import fs from "fs";

// Serve mockServiceWorker.js from root "/" so the service worker scope covers
// /api/* requests. Without this, the worker at /portal/mockServiceWorker.js can
// only intercept requests under /portal/ — missing all API calls.
function mswRootWorker(): Plugin {
  return {
    name: "msw-root-worker",
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        if (req.url === "/mockServiceWorker.js") {
          const file = path.resolve(__dirname, "public/mockServiceWorker.js");
          if (fs.existsSync(file)) {
            res.setHeader("Content-Type", "application/javascript");
            res.end(fs.readFileSync(file, "utf-8"));
            return;
          }
        }
        next();
      });
    },
  };
}

/**
 * Answer the reference route from the mock fixtures during development.
 *
 * The service worker MSW installs controls the page, not a sandboxed blob:
 * frame -- and a referencing artifact renders in exactly such a frame, both in
 * the viewer and in the off-screen frame a thumbnail is captured from (#1497).
 * Without this the frame's request falls through to the SPA index.html, so a
 * referenced logo decodes as a broken image and a referenced data file parses
 * as HTML. The bytes come from the same fixture table the worker answers from
 * (src/mocks/data/assetRefs.ts), so the two surfaces cannot disagree.
 *
 * Only a token the fixtures hold is answered, with its bytes or with the 404 a
 * target that is gone earns. A token they do not hold -- every token a real
 * backend mints, since dev mode is also how the portal is run against a live
 * server -- falls through untouched, and so does a failure to load the fixture
 * module, which would otherwise leave the request hanging.
 */
function mockRefRoute(): Plugin {
  return {
    name: "mock-ref-route",
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const match = /^\/portal\/refs\/[^/]+\/([^/?#]+)/.exec(req.url ?? "");
        if (!match) {
          next();
          return;
        }
        void import("./src/mocks/data/assetRefs")
          .then(({ knowsRefToken, resolveRefContent }) => {
            const token = match[1]!;
            if (!knowsRefToken(token)) {
              next();
              return;
            }
            const content = resolveRefContent(token);
            if (!content) {
              // A token the fixtures hold whose target is gone: the 404 the
              // real route answers, and what a discarded capture is tested on.
              res.statusCode = 404;
              res.setHeader("Content-Type", "application/json");
              res.end(JSON.stringify({ detail: "no such reference" }));
              return;
            }
            res.setHeader("Content-Type", content.contentType);
            res.end(
              typeof content.body === "string" ? content.body : Buffer.from(content.body),
            );
          })
          .catch(() => next());
      });
    },
  };
}

/**
 * Serve the presentation runtime the platform ships (#1767).
 *
 * A slide deck is an HTML asset that loads reveal.js, and the platform serves
 * that library itself rather than leaving each deck to name a CDN: a deck then
 * renders on a locked-down client network and an offline deployment, and the
 * same-origin source sits inside the share viewer's existing `script-src
 * 'self'` without the policy widening. The release is pinned in package.json
 * (exact version, integrity in the lockfile) and copied out of node_modules at
 * build time, so nothing under ui/public carries a third-party blob.
 *
 * In dev the same files are answered from node_modules by a middleware, so the
 * path a deck names is the one path in every mode. The list below is the
 * contract: the knowledge page an agent reads names these paths, and the
 * acceptance suite fetches every path that page names.
 */
// Only the two themes that embed their typeface are served. The others
// (simple, night, ...) @import Google Fonts, which is the CDN dependence the
// served runtime exists to remove: a deck on one would render on an open
// network and fall back to a system face on a closed one, silently.
const REVEAL_VENDOR_PREFIX = "/portal/vendor/reveal/";
const REVEAL_VENDOR_FILES = [
  "reveal.js",
  "reveal.css",
  "reset.css",
  "theme/white.css",
  "theme/black.css",
  "plugin/markdown.js",
  "plugin/zoom.js",
];
const REVEAL_LICENSE = "LICENSE";

function revealFiles(): Map<string, string> {
  const pkgDir = path.resolve(__dirname, "node_modules/reveal.js");
  const files = new Map(REVEAL_VENDOR_FILES.map((f) => [f, path.join(pkgDir, "dist", f)]));
  files.set(REVEAL_LICENSE, path.join(pkgDir, REVEAL_LICENSE));
  return files;
}

/**
 * Serve the map runtime the platform ships (#2068), the way reveal.js is
 * served: MapLibre GL JS, the pmtiles protocol, the Protomaps basemap style,
 * the glyphs and sprites that style names, and the us-atlas boundaries with
 * the TopoJSON client that reads them.
 *
 * MapLibre is the 5.x UMD build because it starts its worker from a blob: URL
 * it makes itself. A map asset runs in a sandboxed frame whose origin is
 * opaque, and the 6.x module build starts its worker from the file's own URL,
 * which a frame with an opaque origin may not do.
 *
 * The glyphs and sprites are not on npm. They are checked in under
 * ui/vendor/maplibre at a pinned commit by scripts/sync-map-assets.sh, which
 * also copies the license texts the npm packages do not carry; every file in
 * that directory is served, and that directory is the list.
 */
const MAP_VENDOR_PREFIX = "/portal/vendor/maplibre/";

function mapFiles(): Map<string, string> {
  const mod = (p: string) => path.resolve(__dirname, "node_modules", p);
  const files = new Map<string, string>([
    ["maplibre-gl.js", mod("maplibre-gl/dist/maplibre-gl.js")],
    ["maplibre-gl.css", mod("maplibre-gl/dist/maplibre-gl.css")],
    ["LICENSE-maplibre.txt", mod("maplibre-gl/LICENSE.txt")],
    ["pmtiles.js", mod("pmtiles/dist/pmtiles.js")],
    ["basemaps.js", mod("@protomaps/basemaps/dist/basemaps.js")],
    ["topojson-client.js", mod("topojson-client/dist/topojson-client.min.js")],
    ["LICENSE-topojson-client.txt", mod("topojson-client/LICENSE")],
    ["LICENSE-us-atlas.txt", mod("us-atlas/LICENSE")],
  ]);
  for (const atlas of ["states", "counties", "nation"]) {
    for (const projection of ["10m", "albers-10m"]) {
      const file = `${atlas}-${projection}.json`;
      files.set(`us-atlas/${file}`, mod(`us-atlas/${file}`));
    }
  }
  const committed = path.resolve(__dirname, "vendor/maplibre");
  for (const entry of fs.readdirSync(committed, {
    recursive: true,
    withFileTypes: true,
  })) {
    if (!entry.isFile()) continue;
    const abs = path.join(entry.parentPath, entry.name);
    files.set(path.relative(committed, abs).split(path.sep).join("/"), abs);
  }
  return files;
}

const VENDOR_CONTENT_TYPES: Record<string, string> = {
  ".js": "application/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json",
  ".pbf": "application/x-protobuf",
  ".png": "image/png",
  ".md": "text/markdown; charset=utf-8",
};

function vendorContentType(file: string): string {
  return VENDOR_CONTENT_TYPES[path.extname(file)] ?? "text/plain; charset=utf-8";
}

/**
 * A fixed set of third-party files served under one /portal/vendor/ path:
 * answered from their sources by a middleware in dev and copied into the build
 * output, so the path a document names is the one path in every mode. The map
 * is served path to source file.
 */
function vendorFiles(name: string, prefix: string, list: () => Map<string, string>): Plugin {
  let outDir = "dist";
  const files = list();
  return {
    name,
    configResolved(config) {
      outDir = config.build.outDir;
    },
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const url = decodeURIComponent((req.url ?? "").split("?")[0] ?? "");
        const source = url.startsWith(prefix) ? files.get(url.slice(prefix.length)) : undefined;
        if (!source) {
          next();
          return;
        }
        res.setHeader("Content-Type", vendorContentType(source));
        res.end(fs.readFileSync(source));
      });
    },
    closeBundle() {
      const dest = path.resolve(__dirname, outDir, prefix.replace(/^\/portal\//, ""));
      for (const [file, source] of files) {
        const target = path.join(dest, file);
        fs.mkdirSync(path.dirname(target), { recursive: true });
        fs.copyFileSync(source, target);
      }
    },
  };
}

export default defineConfig(({ mode }) => {
  const apiTarget = process.env.VITE_API_TARGET || "http://localhost:8080";

  return {
    plugins: [
      react(),
      tailwindcss(),
      vendorFiles("reveal-vendor", REVEAL_VENDOR_PREFIX, revealFiles),
      vendorFiles("maplibre-vendor", MAP_VENDOR_PREFIX, mapFiles),
      ...(mode === "development" ? [mswRootWorker(), mockRefRoute()] : []),
    ],
    base: "/portal/",
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
      // CodeMirror's @codemirror/state enforces a single module instance at
      // runtime: if two copies load (e.g. one from @uiw/react-codemirror and
      // one from a lang-* package resolving a different version), it throws
      // and the editor crashes. This surfaced in headless screenshot runs and
      // forced the Description / Agent Instructions editors to be excluded.
      // Deduping the core modules to one instance fixes the crash.
      dedupe: ["@codemirror/state", "@codemirror/view"],
    },
    // mermaid 11.x lazy-loads diagram modules (flowDiagram, sequenceDiagram,
    // etc.) at runtime. Vite's dep optimizer tries to pre-bundle them but
    // can't resolve the dynamic imports cleanly, producing "file does not
    // exist in the optimize deps directory" errors after cache flips.
    // Excluding mermaid + its diagram registry tells Vite to load these as
    // raw ESM, which mermaid is designed for.
    //
    // Side-effect of that exclude: mermaid's transitive `dayjs` import
    // also skips vite's CJS→ESM interop shim and breaks at runtime
    // ("does not provide an export named 'default'") because the
    // shipped dayjs.min.js is UMD. Explicitly `include` dayjs so vite
    // still pre-bundles it with the interop wrapper. Same for any
    // other UMD/CJS-only deps mermaid pulls in.
    // hyparquet and its decompressors are imported only by the Parquet
    // viewer, which a session reaches by opening a Parquet file rather than by
    // loading the app. Left to discovery, the dev server pre-bundles them on
    // that first open and reloads the page mid-render, which fails the dynamic
    // import of whichever page was loading (#1833). Naming them here bundles
    // them at startup, where nothing is waiting on them.
    optimizeDeps: {
      exclude: ["mermaid"],
      include: ["dayjs", "@braintree/sanitize-url", "hyparquet", "hyparquet-compressors"],
    },
    server: {
      proxy: {
        "/api": {
          target: apiTarget,
          changeOrigin: true,
          secure: false,
          // xfwd populates X-Forwarded-Host / X-Forwarded-Proto with
          // the ORIGINAL request's host (e.g. localhost:5173), so the
          // Go server's OAuth callback URL builder points the IdP
          // back at the Vite dev server. Without this, OAuth flows
          // started from :5173 redirect back to :8080 — and the user
          // ends up viewing the embedded (compiled-in, stale) UI
          // bundle instead of the live Vite source.
          xfwd: true,
        },
        "/portal/view": {
          target: apiTarget,
          changeOrigin: true,
          secure: false,
          xfwd: true,
        },
        // The reference-serving route, which mockRefRoute above answers only
        // for the tokens the fixtures hold. A token a real backend minted fell
        // through to index.html, so a referenced file's picture was an HTML
        // page in dev and a picture everywhere else -- the image thumbnail as
        // much as the stored tile a referenced PDF now has (#1794). The plugin
        // middleware runs ahead of the proxy, so the fixtures still win.
        "/portal/refs": {
          target: apiTarget,
          changeOrigin: true,
          secure: false,
          xfwd: true,
        },
        // /portal/auth/* is the platform's browser_session OIDC flow
        // (login → IdP → callback → logout). Without this proxy, the
        // SPA dev server tries to resolve /portal/auth/login as a
        // client-side route, returns index.html, and the operator
        // sees the login page reload itself ("just jumps") instead of
        // being redirected to Keycloak.
        "/portal/auth": {
          target: apiTarget,
          changeOrigin: true,
          secure: false,
          xfwd: true,
        },
      },
    },
  };
});
