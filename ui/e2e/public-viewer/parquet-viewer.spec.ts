import { test, expect, type APIRequestContext, type Page, type Request } from "@playwright/test";
import { readFileSync } from "fs";
import path from "path";
import { fileURLToPath } from "url";

// A Parquet file is viewed, not downloaded (#1833), and read by byte range.
//
// The Parquet viewer reads the file's footer from the end of the file, then one
// row group at a time, through the same content endpoint every viewer uses.
// These cases open the pyarrow fixture from the acceptance suite as a portal
// asset, as a managed resource and through a public share link, and hold what
// each shows -- the schema with the type each column registers as, the file's
// facts, the first rows, a later row group on paging -- and that every read of
// the file carried a Range header, so nothing fetched it whole.
//
// Everything is created here through the REST API and removed afterwards, so
// the run needs a live stack (`make dev`) and nothing seeded for it. The public
// page is served by the platform binary with the content-viewer bundle it
// embeds, which `make frontend-build` produces; the portal pages are the SPA
// Vite serves.

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const FIXTURE = path.resolve(__dirname, "../../../test/acceptance/testdata/issue_1833_all_types.parquet");
const API_KEY = process.env["MCP_API_KEY"] ?? "acme-dev-key-2024";
const PORTAL = process.env["PORTAL_BASE_URL"] ?? "http://localhost:5173";

interface Created {
  assetID: string;
  resourceID: string;
  shareToken: string;
}

const created: Created = { assetID: "", resourceID: "", shareToken: "" };
let api: APIRequestContext;

test.beforeAll(async ({ playwright }) => {
  const baseURL = test.info().project.use.baseURL;
  api = await playwright.request.newContext({ baseURL, extraHTTPHeaders: { "X-API-Key": API_KEY } });
  const bytes = readFileSync(FIXTURE);

  // An asset holding the fixture: created, then given the Parquet bytes as its
  // content, which the content route types from the bytes themselves.
  const asset = await api.post("/api/v1/portal/assets", {
    data: { name: "Acceptance 1833 viewer", content_type: "application/octet-stream", content: "placeholder" },
  });
  expect(asset.status(), await asset.text()).toBe(201);
  created.assetID = ((await asset.json()) as { id: string }).id;
  const put = await api.put(`/api/v1/portal/assets/${created.assetID}/content`, { data: bytes });
  expect(put.status(), await put.text()).toBe(200);

  const share = await api.post(`/api/v1/portal/assets/${created.assetID}/shares`, {
    data: { access_mode: "public", expires_in: "1h" },
  });
  expect(share.status(), await share.text()).toBe(201);
  created.shareToken = ((await share.json()) as { share: { token: string } }).share.token;

  const resource = await api.post("/api/v1/resources", {
    multipart: {
      scope: "global",
      path: "acceptance-1833",
      display_name: `Acceptance 1833 viewer ${Date.now()}`,
      description: "Acceptance #1833: the Parquet viewer.",
      file: { name: "acc-1833-viewer.parquet", mimeType: "application/vnd.apache.parquet", buffer: bytes },
    },
  });
  expect(resource.status(), await resource.text()).toBe(201);
  created.resourceID = ((await resource.json()) as { id: string }).id;
});

test.afterAll(async () => {
  if (created.assetID) await api.delete(`/api/v1/portal/assets/${created.assetID}`);
  if (created.resourceID) await api.delete(`/api/v1/resources/${created.resourceID}`);
  await api.dispose();
});

/** Every request the page makes for the file's bytes, with whether it asked for a range. */
function watchContent(page: Page, pattern: RegExp): Request[] {
  const seen: Request[] = [];
  page.on("request", (req) => {
    if (req.method() === "GET" && pattern.test(new URL(req.url()).pathname)) seen.push(req);
  });
  return seen;
}

async function assertRanged(requests: Request[]): Promise<void> {
  expect(requests.length, "the viewer read nothing from the content endpoint").toBeGreaterThan(0);
  for (const req of requests) {
    const headers = await req.allHeaders();
    expect(headers["range"], `${req.url()} was fetched without a Range header`).toMatch(/^bytes=\d+-\d+$/);
  }
}

/** What every surface shows of the fixture, and paging to its second row group. */
async function assertViewer(page: Page): Promise<void> {
  const schema = page.getByTestId("parquet-schema");
  await expect(schema).toBeVisible({ timeout: 30_000 });
  for (const text of ["amount", "DECIMAL(12,2)", "TIMESTAMP(6)", "ARRAY(VARCHAR)", 'ROW("a" BIGINT, "n" VARCHAR)']) {
    await expect(schema).toContainText(text);
  }
  await expect(schema).toContainText("INT64 TIMESTAMP(MICROS)");

  const facts = page.getByTestId("parquet-facts");
  await expect(facts).toContainText("Rows4");
  await expect(facts).toContainText("Row groups2");
  await expect(facts).toContainText("parquet-cpp-arrow");

  const rows = page.getByTestId("parquet-rows");
  await expect(rows.getByRole("button", { name: /^Open row/ })).toHaveCount(2);
  await expect(rows).toContainText("alpha");
  await page.getByRole("button", { name: "Next row group" }).click();
  await expect(rows).toContainText("Row group 2 of 2");
  await expect(rows.getByRole("button", { name: /^Open row/ })).toHaveCount(2);
  await expect(rows).toContainText("9007199254740993");
}

test.describe("the Parquet viewer", () => {
  test("renders a public share by range", async ({ page }) => {
    const reads = watchContent(page, new RegExp(`/portal/view/${created.shareToken}/content$`));
    await page.goto(`/portal/view/${created.shareToken}`);
    await assertViewer(page);
    await assertRanged(reads);
  });

  test("renders a portal asset by range", async ({ page }) => {
    const reads = watchContent(page, new RegExp(`/api/v1/portal/assets/${created.assetID}/content$`));
    await signIn(page);
    await page.goto(`${PORTAL}/portal/assets/${created.assetID}`);
    await assertViewer(page);
    await assertRanged(reads);
  });

  test("renders a managed resource by range", async ({ page }) => {
    const reads = watchContent(page, new RegExp(`/api/v1/resources/${created.resourceID}/content$`));
    await signIn(page);
    await page.goto(`${PORTAL}/portal/admin/resources/${created.resourceID}`);
    await assertViewer(page);
    await assertRanged(reads);
  });
});

/** Signs the SPA in with the API key, the portal's own sign-in form. */
async function signIn(page: Page): Promise<void> {
  await page.goto(`${PORTAL}/portal/`);
  const nav = page.locator("nav");
  const key = page.getByPlaceholder("X-API-Key");
  const which = await Promise.race([
    nav.waitFor({ state: "visible", timeout: 20_000 }).then(() => "nav" as const),
    key.waitFor({ state: "visible", timeout: 20_000 }).then(() => "login" as const),
  ]);
  if (which === "login") {
    await key.fill(API_KEY);
    await page.getByRole("button", { name: "Sign in with API key" }).click();
    await nav.waitFor({ state: "visible", timeout: 15_000 });
  }
}
