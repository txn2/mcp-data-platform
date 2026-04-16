import { test, type Page } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";
import fs from "fs";
import { routes } from "./route-manifest";
import { authenticate } from "./helpers/auth";
import { setTheme, resetThemeTracking } from "./helpers/theme";
import { waitForPageReady } from "./helpers/wait";
import { loadBrandingConfig } from "./branding.config";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const config = loadBrandingConfig();
const THEMES = ["light", "dark"] as const;
const OUTPUT_DIR = config.outputDir || path.resolve(__dirname, "..", "..", "..", "docs", "images", "screenshots");
const PREFIX = config.prefix || "";

for (const theme of THEMES) {
  const dir = path.join(OUTPUT_DIR, theme);
  if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
}

async function navigateClientSide(page: Page, appPath: string): Promise<void> {
  await page.evaluate((p) => {
    window.history.pushState(null, "", p);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, appPath);
  await page.waitForTimeout(1000);
  await page.waitForLoadState("networkidle");
}

test.describe("Portal Screenshots", () => {
  let page: Page;

  test.beforeAll(async ({ browser }) => {
    const context = await browser.newContext({
      viewport: { width: 1440, height: 900 },
      reducedMotion: "reduce",
      colorScheme: "light",
    });
    page = await context.newPage();

    const hasBranding = config.portalTitle || config.platformName || config.portalLogo;
    if (hasBranding) {
      const overrides = {
        title: config.portalTitle || config.platformName,
        logo: config.portalLogo || "",
      };
      await page.addInitScript((o) => {
        const origFetch = window.fetch;
        window.fetch = async function (...args) {
          const res = await origFetch.apply(this, args);
          const url = typeof args[0] === "string" ? args[0] : (args[0] as Request).url;
          if (url.includes("/public/branding")) {
            const json = await res.json();
            if (o.title) json.portal_title = o.title;
            if (o.logo) {
              json.portal_logo = o.logo;
              json.portal_logo_light = o.logo;
              json.portal_logo_dark = o.logo;
            }
            return new Response(JSON.stringify(json), {
              status: res.status,
              headers: res.headers,
            });
          }
          return res;
        };
      }, overrides);
    }

    await authenticate(page);
  });

  test.afterAll(async () => {
    await page?.context().close();
  });

  for (const route of routes) {
    const tabList = route.tabs ?? [undefined];

    for (const tab of tabList) {
      for (const theme of THEMES) {
        const tabSuffix = tab ? `-${tab}` : "";
        const name = `${PREFIX}${route.category}-${route.slug}${tabSuffix}-${theme}`;

        test(name, async () => {
          const url = tab ? `${route.path}#${tab}` : route.path;

          if (route.clientNav) {
            await navigateClientSide(page, url);
          } else {
            await page.goto(url, { waitUntil: "domcontentloaded" });
            await page
              .waitForSelector("nav", { timeout: 10_000 })
              .catch(() => {});
          }

          resetThemeTracking();
          await setTheme(page, theme);

          await waitForPageReady(page, route);

          await page.screenshot({
            path: path.join(OUTPUT_DIR, theme, `${name}.png`),
            fullPage: false,
          });
        });
      }
    }
  }
});
