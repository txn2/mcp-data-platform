import { defineConfig } from "vite";
import tailwindcss from "@tailwindcss/vite";
import path from "path";

// The public share viewer's stylesheet, compiled against the viewer bundle
// that vite.content-viewer.config.ts just emitted. It must run second: the
// entry declares dist-content-viewer as its only Tailwind source, so building
// it before the JS produces an empty utility set. See src/content-viewer.css.
export default defineConfig({
  plugins: [tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  build: {
    outDir: "dist-content-viewer-css",
    // public/ belongs to the SPA; the viewer serves only its own build output.
    copyPublicDir: false,
    emptyOutDir: true,
    rollupOptions: {
      input: path.resolve(__dirname, "src/content-viewer.css"),
      output: {
        assetFileNames: "content-viewer[extname]",
      },
    },
  },
});
