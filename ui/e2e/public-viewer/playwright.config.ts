import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// End-to-end coverage of the public share viewer, which is a server-rendered
// Go page rather than part of the SPA: its markup, its embedded content-viewer
// bundle and its Content-Security-Policy all come from the platform binary, so
// MSW cannot stand in for the backend the way it does for the admin suites.
//
// The suite therefore runs against a live stack — `make dev` (platform binary +
// Postgres + SeaweedFS, seeded by dev/seed.sql) — and drives the share tokens
// that seed file creates. Point it elsewhere with PUBLIC_VIEWER_BASE_URL.
//
// The JSX case loads react, recharts and lucide-react from esm.sh inside the
// artifact frame, exactly as a shared JSX asset does in production, so the run
// needs network egress to esm.sh.
const baseURL =
  process.env["PUBLIC_VIEWER_BASE_URL"] ??
  `http://localhost:${process.env["DEV_API_PORT"] ?? "8080"}`;

export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 90_000,
  expect: { timeout: 20_000 },
  use: {
    baseURL,
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
  },
  workers: 1,
  retries: process.env["CI"] ? 1 : 0,
  reporter: [["list"]],
});
