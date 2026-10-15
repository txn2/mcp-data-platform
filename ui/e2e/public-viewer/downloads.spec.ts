import { test, expect, type APIRequestContext, type Page } from "@playwright/test";

// #2127: an HTML asset can offer files for download. The frame it renders in
// is sandboxed with allow-downloads, so a CSV the page builds from a Blob and
// an <a download> naming a declared file both download when the reader
// clicks -- in the public share viewer and in the portal's asset page. A
// browser blocks every download from a sandboxed frame without that token, so
// only a browser can hold this.
//
// The suite runs against a live stack (playwright.config.ts). The asset, its
// declared CSV and its public share are made here through the portal API as
// the dev stack's administrator, and removed after.

const KEY = process.env["DOWNLOADS_API_KEY"] ?? "acme-dev-key-2024";
const CSV = "region,total\nwest,4\neast,9\n";

/** The page: a button that builds a CSV, and a link offering the declared file. */
function page2127(ref: string): string {
  return `<!DOCTYPE html><html><body>
<h1>Orders</h1>
<button id="build" type="button">Download the table</button>
<a id="declared" href="${ref}" download>Download the orders file</a>
<script>
document.getElementById("build").addEventListener("click", function () {
  var blob = new Blob(["region,total\\nnorth,7\\n"], { type: "text/csv" });
  var a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = "table.csv";
  document.body.appendChild(a);
  a.click();
});
</script></body></html>`;
}

interface Made {
  reportId: string;
  dataId: string;
  token: string;
}

async function api(request: APIRequestContext, method: "POST" | "DELETE", path: string, data?: unknown) {
  const res = await request.fetch(path, { method, headers: { "X-API-Key": KEY }, data });
  expect(res.status(), `${method} ${path}: ${await res.text()}`).toBeLessThan(300);
  return res.status() === 204 ? {} : ((await res.json()) as Record<string, unknown>);
}

let made: Made;

test.beforeAll(async ({ playwright }) => {
  const baseURL = test.info().project.use.baseURL;
  const request = await playwright.request.newContext({ baseURL });
  const data = await api(request, "POST", "/api/v1/portal/assets", {
    name: `acc-2127-orders-${Date.now()}`, content_type: "text/csv", content: CSV,
  });
  const dataId = data["id"] as string;
  const report = await api(request, "POST", "/api/v1/portal/assets", {
    name: `acc-2127-report-${Date.now()}`, content_type: "text/html", content: page2127(`mcp:asset:${dataId}`),
  });
  const reportId = report["id"] as string;
  await api(request, "POST", `/api/v1/portal/assets/${reportId}/references`, { target_kind: "asset", target_id: dataId });
  const share = await api(request, "POST", `/api/v1/portal/assets/${reportId}/shares`, {
    access_mode: "public", expires_in: "1h",
  });
  made = { reportId, dataId, token: (share["share"] as Record<string, unknown>)["token"] as string };
  await request.dispose();
});

test.afterAll(async ({ playwright }) => {
  if (!made) return;
  const baseURL = test.info().project.use.baseURL;
  const request = await playwright.request.newContext({ baseURL });
  for (const id of [made.reportId, made.dataId]) {
    await request.fetch(`/api/v1/portal/assets/${id}`, { method: "DELETE", headers: { "X-API-Key": KEY } });
  }
  await request.dispose();
});

/** Clicks target inside the artifact frame and returns the download it started. */
async function downloadFrom(page: Page, selector: string) {
  const frame = page.frameLocator("iframe").first();
  await expect(frame.locator(selector)).toBeVisible();
  const [download] = await Promise.all([page.waitForEvent("download"), frame.locator(selector).click()]);
  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const chunk of stream) chunks.push(chunk as Buffer);
  return { name: download.suggestedFilename(), body: Buffer.concat(chunks).toString("utf8") };
}

async function signIn(page: Page): Promise<void> {
  await page.goto("/portal/");
  const nav = page.locator("nav");
  const apiKey = page.locator('input[placeholder="X-API-Key"]');
  const which = await Promise.race([
    nav.waitFor({ state: "visible", timeout: 30_000 }).then(() => "nav" as const),
    apiKey.waitFor({ state: "visible", timeout: 30_000 }).then(() => "login" as const),
  ]);
  if (which === "login") {
    await apiKey.fill(KEY);
    await page.getByRole("button", { name: /Sign in with API key/i }).click();
    await nav.waitFor({ state: "visible", timeout: 30_000 });
  }
}

test.describe("downloads from an HTML asset (#2127)", () => {
  test("the public viewer downloads a CSV the page builds and the declared file", async ({ page }) => {
    await page.goto(`/portal/view/${made.token}`, { waitUntil: "networkidle" });
    const built = await downloadFrom(page, "#build");
    expect(built.name).toBe("table.csv");
    expect(built.body).toBe("region,total\nnorth,7\n");
    const declared = await downloadFrom(page, "#declared");
    expect(declared.body).toBe(CSV);
  });

  test("the portal's asset page downloads a CSV the page builds and the declared file", async ({ page }) => {
    await signIn(page);
    await page.goto(`/portal/assets/${made.reportId}`, { waitUntil: "networkidle" });
    const built = await downloadFrom(page, "#build");
    expect(built.name).toBe("table.csv");
    expect(built.body).toBe("region,total\nnorth,7\n");
    const declared = await downloadFrom(page, "#declared");
    expect(declared.body).toBe(CSV);
  });
});
