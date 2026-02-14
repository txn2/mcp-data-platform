import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "path";
import fs from "fs";

// Serve mockServiceWorker.js from root "/" so the service worker scope covers
// /api/* requests. Without this, the worker at /admin/mockServiceWorker.js can
// only intercept requests under /admin/ — missing all API calls.
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
  // Use VITE_API_TARGET to proxy API calls to a remote server:
  //   VITE_API_TARGET=https://mcp.pmgsc-data.org npm run dev
  const apiTarget = process.env.VITE_API_TARGET || "http://localhost:8080";

  return {
    plugins: [react(), tailwindcss(), ...(mode === "development" ? [mswRootWorker()] : [])],
    base: "/admin/",
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    server: {
      proxy: {
        "/api": {
          target: apiTarget,
          changeOrigin: true,
          secure: true,
        },
      },
    },
  };
});
