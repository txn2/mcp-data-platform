import { test, expect, type Page } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";
import fs from "fs";
import { mcpApps, type McpAppRoute } from "./mcpapp-manifest";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const THEMES = ["light", "dark"] as const;
const APPS_DIR = path.resolve(__dirname, "..", "..", "..", "apps");
const OUTPUT_DIR =
  process.env["SCREENSHOT_OUTPUT_DIR"] ||
  path.resolve(__dirname, "..", "..", "..", "docs", "images", "screenshots");

for (const theme of THEMES) {
  const dir = path.join(OUTPUT_DIR, theme);
  if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
}

/**
 * Inject the app-config the server injects at serve time.
 *
 * `internal/platform/mcpapps/resource.go` finds the app's `app-config` script
 * tag and replaces it with one carrying the deployment's branding, inserting
 * the tag before `</head>` when the app declares none. Doing the same here is
 * what makes the captured app the app a reader is actually served, rather
 * than the unbranded source file.
 */
function injectConfig(html: string, config: Record<string, unknown>): string {
  const tag = `<script id="app-config" type="application/json">${JSON.stringify(config)}</script>`;
  // Matched on the opening tag's PREFIX, and inserted before </head> when
  // there is none: the same two cases resource.go handles. Keying on the
  // exact `type` attribute would throw on an app the platform itself serves
  // correctly.
  const existing = /<script id="app-config"[\s\S]*?<\/script>/;
  // A replacer function, not a string: `$&`, `` $` ``, `$'` and `$n` in a
  // string replacement are expanded, and this config carries operator-supplied
  // brand values, so a brand containing `$&` would produce malformed JSON and
  // the app's own JSON.parse would fall into its catch.
  if (existing.test(html)) {
    return html.replace(existing, () => tag);
  }
  if (html.includes("</head>")) {
    return html.replace("</head>", () => `${tag}</head>`);
  }
  throw new Error("app has neither an app-config tag nor a </head> to inject before");
}

/**
 * The empty host document: an iframe at the panel size the client offers.
 * The bridge that drives it is installed separately, by `startApp`.
 */
function hostShell(route: McpAppRoute): string {
  return `<!DOCTYPE html>
<html><head><meta charset="utf-8"><style>
  html,body{margin:0;padding:0;background:transparent}
  iframe{width:${route.size.width}px;height:${route.size.height}px;border:0;display:block}
</style></head><body><iframe id="app"></iframe></body></html>`;
}

/**
 * Install the host bridge and hand the app its document.
 *
 * The app HTML is passed as an argument rather than interpolated into a
 * `<script>` in the shell: these documents carry their own inline scripts, so
 * the first `</script>` inside one would close the host's script tag and the
 * bridge would never be installed.
 */
async function startApp(
  page: Page,
  appHtml: string,
  route: McpAppRoute,
): Promise<void> {
  await page.evaluate(
    ({ html, toolName, text }) => {
      const w = window as never as { __appReady: boolean };
      w.__appReady = false;
      const frame = document.getElementById("app") as HTMLIFrameElement;
      const result = { toolName, content: [{ type: "text", text }] };

      type Msg = {
        method?: string;
        id?: number;
        params?: { height?: number };
      };
      const reply = (to: Window | null, id: number, r: unknown) =>
        to?.postMessage({ jsonrpc: "2.0", id, result: r }, "*");

      // The app calls its tool once initialize resolves, so the result is
      // pushed as the notification a host sends: that is what it renders from.
      const onInitialize = (source: Window | null, id: number) => {
        reply(source, id, { protocolVersion: "2025-01-09" });
        setTimeout(() => {
          frame.contentWindow?.postMessage(
            {
              jsonrpc: "2.0",
              method: "ui/notifications/tool-result",
              params: result,
            },
            "*",
          );
          w.__appReady = true;
        }, 50);
      };

      // The app owns its height and reports it (#1043); a host sizes the
      // iframe from the last one it sent. Honouring it is what keeps the
      // capture the size a reader sees, rather than a fixed guess with dead
      // space below the content.
      const onSizeChanged = (m: Msg) => {
        if (typeof m.params?.height === "number") {
          frame.style.height = `${m.params.height}px`;
        }
      };

      window.addEventListener("message", (e: MessageEvent) => {
        const m = e.data as Msg;
        if (!m || typeof m !== "object") return;
        const source = e.source as Window | null;
        if (m.method === "ui/initialize" && m.id !== undefined) {
          onInitialize(source, m.id);
        } else if (m.method === "ui/call-tool" && m.id !== undefined) {
          reply(source, m.id, { content: result.content });
        } else if (m.method === "ui/notifications/size-changed") {
          onSizeChanged(m);
        }
      });

      frame.srcdoc = html;
    },
    {
      html: appHtml,
      toolName: route.toolName,
      text: JSON.stringify(route.data),
    },
  );
}

test.describe("MCP App Screenshots", () => {
  for (const route of mcpApps) {
    for (const theme of THEMES) {
      const name = `app-${route.slug}-${theme}`;

      test(name, async ({ browser }) => {
        // Each app gets its own context: the apps read the colour scheme from
        // `prefers-color-scheme` only, with no in-page toggle, so the scheme
        // has to be set before the document parses.
        const context = await browser.newContext({
          viewport: { width: route.size.width, height: route.size.height },
          colorScheme: theme,
          reducedMotion: "reduce",
        });
        const page: Page = await context.newPage();
        try {
          const src = fs.readFileSync(
            path.join(APPS_DIR, route.dir, "index.html"),
            "utf-8",
          );
          await page.setContent(hostShell(route), {
            waitUntil: "domcontentloaded",
          });
          await startApp(page, injectConfig(src, route.config), route);

          const frame = page.frameLocator("#app");
          await frame.locator(route.waitFor).first().waitFor({ timeout: 15_000 });
          // The result only lands after the initialize handshake completes.
          await page.waitForFunction(() => (window as never as { __appReady: boolean }).__appReady, null, {
            timeout: 15_000,
          });
          await page.waitForTimeout(600);

          // An app that rendered nothing is a green test with a blank image,
          // which is the failure this capture exists to prevent.
          const text = await frame.locator("body").innerText();
          expect(
            text.trim().length,
            `${route.slug} rendered no text; the app did not receive its tool result`,
          ).toBeGreaterThan(40);

          const shot = (fileName: string) =>
            page
              .locator("#app")
              .screenshot({ path: path.join(OUTPUT_DIR, theme, fileName) });

          await shot(`${name}.png`);

          // The tabs only exist once the result populated them, so they are
          // captured from this same live app rather than re-mounted per view.
          for (const view of route.views ?? []) {
            const btn = frame.locator(view.click);
            await btn.waitFor({ state: "visible", timeout: 10_000 });
            await btn.click();
            await page.waitForTimeout(400);
            await shot(`app-${route.slug}-${view.suffix}-${theme}.png`);
          }
        } finally {
          await context.close();
        }
      });
    }
  }
});
