import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

// The public share viewer's JavaScript.
//
// This is an ES-module build with real code splitting, not a single bundle:
// the renderers behind ContentRenderer's lazy() boundaries each become their
// own chunk, and the page loads the entry plus whichever chunks the asset it
// is showing actually needs. The previous IIFE build could not do this —
// `formats: ["iife"]` forces rollup to inline every dynamic import — so a
// markdown document shipped the JSX transformer, CodeMirror and the diagram
// engine along with it (#1355).
//
// The chunks are served as files from /portal/view/_assets/ rather than
// inlined into the page, so they are also content-addressed and cacheable
// across views.
export default defineConfig({
  plugins: [react()],
  base: "/portal/view/_assets/",
  define: {
    "process.env.NODE_ENV": JSON.stringify("production"),
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  build: {
    outDir: "dist-content-viewer",
    // public/ belongs to the SPA; the viewer serves only its own build output.
    copyPublicDir: false,
    emptyOutDir: true,
    // The Go side reads the manifest to find the entry chunk's hashed name.
    manifest: true,
    cssCodeSplit: false,
    rollupOptions: {
      input: path.resolve(__dirname, "src/content-viewer-entry.tsx"),
      output: {
        format: "es",
        entryFileNames: "[name]-[hash].js",
        chunkFileNames: "[name]-[hash].js",
        assetFileNames: "[name]-[hash][extname]",
      },
    },
  },
});
