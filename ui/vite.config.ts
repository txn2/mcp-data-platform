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

export default defineConfig(({ mode }) => {
  const apiTarget = process.env.VITE_API_TARGET || "http://localhost:8080";

  return {
    plugins: [react(), tailwindcss(), ...(mode === "development" ? [mswRootWorker()] : [])],
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
    optimizeDeps: {
      exclude: ["mermaid"],
      include: ["dayjs", "@braintree/sanitize-url"],
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
