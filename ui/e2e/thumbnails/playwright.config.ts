import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Thumbnail capture against a LIVE stack.
//
// Nothing on a server rasterizes a document: the image is made by a real
// browser, from real bytes, and uploaded to the real routes. MSW cannot stand
// in for any of that -- it would mock away the exact thing under test -- and a
// unit test with a stubbed capturer proves only that the queue calls something.
//
// So this suite runs against `make dev`, drives the resources the seed file
// creates, and asserts that captures actually land and are actually served.
// It is not part of `make verify`: it needs a stack. Run it with `make dev` up.
const baseURL =
  process.env["THUMBNAIL_BASE_URL"] ??
  `http://localhost:${process.env["DEV_UI_PORT"] ?? "5173"}`;

export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 180_000,
  expect: { timeout: 30_000 },
  use: {
    baseURL,
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
  },
  workers: 1,
  retries: 0,
  reporter: [["list"]],
});
