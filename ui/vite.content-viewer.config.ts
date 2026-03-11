import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
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
    emptyOutDir: true,
    lib: {
      entry: path.resolve(__dirname, "src/content-viewer-entry.tsx"),
      name: "ContentViewer",
      formats: ["iife"],
      fileName: () => "content-viewer.js",
    },
    cssCodeSplit: false,
    rollupOptions: {
      output: {
        assetFileNames: "content-viewer[extname]",
      },
    },
  },
});
