import { test, expect, type Page, type APIRequestContext } from "@playwright/test";

// Does the capture pipeline actually work?
//
// Every other test of it stubs something: the unit tests mock the capturer, and
// the acceptance suite uploads a PNG by hand because its client is not a
// browser. Neither proves that a real browser, given a real resource, produces
// a real image and that the platform then serves it -- which is the only claim
// anybody cares about (#1554).
//
// This drives a live stack. It is slow and it needs `make dev`, which is why it
// is not in `make verify`; it is the gate that says the feature works.

const API_BASE = process.env["THUMBNAIL_API_URL"] ?? "http://localhost:28080";
const API_KEY = process.env["THUMBNAIL_API_KEY"] ?? "acme-dev-key-2024";

/**
 * Sign the page in without touching the sign-in form.
 *
 * The auth store reads its key from sessionStorage at init, so seeding it is
 * the same act the form performs and none of the typing. It runs before any
 * document script, so the store is authenticated on first render.
 */
async function authenticate(page: Page): Promise<void> {
  await page.addInitScript(
    ([key]) => window.sessionStorage.setItem("mcp-portal-api-key", key as string),
    [API_KEY],
  );
}

/**
 * File a markdown resource that certainly needs a capture, and return its id.
 *
 * The suite used to assert against whatever the stack happened to hold, so it
 * passed once and then failed on a drained stack -- a test that depends on
 * leftover state is a test that reports the state, not the feature. Creating
 * its own subject makes every run identical.
 */
async function seedResource(
  api: APIRequestContext,
  file: { ext: string; mimeType: string; body: string } = {
    ext: "md",
    mimeType: "text/markdown",
    body: "# probe\n\nProse for the capturer to render.\n",
  },
): Promise<string> {
  const name = `capture-probe-${Date.now()}`;
  const form = {
    multipart: {
      scope: "global",
      path: "references",
      display_name: name,
      description: "Live capture probe: a document that certainly needs a thumbnail.",
      file: {
        name: `${name}.${file.ext}`,
        mimeType: file.mimeType,
        buffer: Buffer.from(file.body),
      },
    },
    headers: { "X-API-Key": API_KEY },
  };
  const res = await api.post(`${API_BASE}/api/v1/resources`, form);
  expect(res.ok(), `seeding a probe resource: HTTP ${res.status()}`).toBeTruthy();
  return (await res.json()).id as string;
}

/** One resource's row, which is where a capture is recorded. */
async function resourceRow(
  api: APIRequestContext,
  id: string,
): Promise<Record<string, unknown>> {
  const res = await api.get(`${API_BASE}/api/v1/resources/${id}`, {
    headers: { "X-API-Key": API_KEY },
  });
  expect(res.ok(), `reading resource ${id}: HTTP ${res.status()}`).toBeTruthy();
  return (await res.json()) as Record<string, unknown>;
}

/**
 * A document holding the box html2canvas cannot draw: a bar chart whose fills
 * compute to a fraction of a pixel.
 *
 * html2canvas draws a gradient by filling a canvas the size of the background
 * positioning area and handing it to createPattern, and the canvas dimensions
 * truncate to integers -- so a bar 0.1% of 300px wide is a zero-width canvas
 * and createPattern throws, aborting the capture of the whole document. An
 * asset containing one never had a tile and never would (#1751).
 */
const SUB_PIXEL_BARS = `<!doctype html><html><head><meta charset="utf-8"><style>
  body { font: 14px system-ui; padding: 24px; }
  .row { height: 18px; background: #eef2f7; margin: 6px 0; width: 320px; }
  i { display: inline-block; height: 18px; background: linear-gradient(90deg,#4aa3ff,#7bc0ff); }
</style></head><body>
  <h1>Regional revenue</h1>
  <div class="row"><i style="width:62%"></i></div>
  <div class="row"><i style="width:41%"></i></div>
  <div class="row"><i style="width:0.1%"></i></div>
  <div class="row"><i style="width:0.2%"></i></div>
  <div class="row"><i style="width:0%"></i></div>
  <p>Three of the bars above are narrower than one pixel.</p>
</body></html>`;

/** File a markdown asset that certainly needs a capture, and return its id. */
async function seedAsset(api: APIRequestContext): Promise<string> {
  const res = await api.post(`${API_BASE}/api/v1/portal/assets`, {
    headers: { "X-API-Key": API_KEY },
    data: {
      name: `capture-probe-${Date.now()}`,
      description: "Live capture probe: an asset whose tile is taken in a browser.",
      content_type: "text/markdown",
      content: "# Probe\n\nProse for the capturer to render.\n",
    },
  });
  expect(res.ok(), `seeding a probe asset: HTTP ${res.status()}`).toBeTruthy();
  return (await res.json()).id as string;
}

/** One asset's row, which is where its capture is recorded. */
async function assetRow(api: APIRequestContext, id: string): Promise<Record<string, unknown>> {
  const res = await api.get(`${API_BASE}/api/v1/portal/assets/${id}`, {
    headers: { "X-API-Key": API_KEY },
  });
  expect(res.ok(), `reading asset ${id}: HTTP ${res.status()}`).toBeTruthy();
  return (await res.json()) as Record<string, unknown>;
}

/** Remove a probe asset so a run leaves the library as it found it. */
async function removeAsset(api: APIRequestContext, id: string): Promise<void> {
  await api.delete(`${API_BASE}/api/v1/portal/assets/${id}`, {
    headers: { "X-API-Key": API_KEY },
  });
}

/** Remove a probe so a run leaves the library as it found it. */
async function removeResource(api: APIRequestContext, id: string): Promise<void> {
  await api.delete(`${API_BASE}/api/v1/resources/${id}`, { headers: { "X-API-Key": API_KEY } });
}

/** What the platform still reports as needing a capture. */
async function pending(api: APIRequestContext, kind: "resources" | "assets"): Promise<string[]> {
  const path =
    kind === "resources"
      ? `${API_BASE}/api/v1/resources/thumbnails/pending?limit=200`
      : `${API_BASE}/api/v1/portal/thumbnails/pending?limit=200`;
  const res = await api.get(path, { headers: { "X-API-Key": API_KEY } });
  expect(res.ok(), `${kind} pending list: HTTP ${res.status()}`).toBeTruthy();
  const body = await res.json();
  const rows = kind === "resources" ? body.resources : body.data;
  return (rows ?? []).map((r: { id: string }) => r.id);
}

test.describe("thumbnail capture against a live stack", () => {
  // A library of documents used to be a wall of identical icons, because a
  // resource had no capture at all: the tile WAS the file (#1554).
  test("a portal tab captures a resource and the platform serves the image back", async ({
    page,
    request,
  }) => {
    const id = await seedResource(request);

    const uploads: string[] = [];
    page.on("response", (res) => {
      const m = new RegExp(`/api/v1/resources/${id}/thumbnail`).exec(res.url());
      if (m && res.request().method() === "PUT") {
        uploads.push(`${res.url().includes("dark") ? "dark" : "light"} ${res.status()}`);
      }
    });

    try {
      await authenticate(page);
      await page.goto("/portal/resources");
      await page.locator("nav").waitFor({ state: "visible" });

      // The queue works while the browser is idle with the tab in front, so
      // this waits on the platform's own answer rather than on the page.
      await expect
        .poll(
          async () => {
            const res = await request.get(`${API_BASE}/api/v1/resources/${id}`, {
              headers: { "X-API-Key": API_KEY },
            });
            return res.ok() ? !!(await res.json()).thumbnail_s3_key : false;
          },
          {
            timeout: 150_000,
            intervals: [2_000],
            message: `no capture was recorded for ${id}. PUTs seen: ${uploads.join(", ") || "none"}`,
          },
        )
        .toBe(true);

      // Nothing was refused. A 404 here is the wrong API root, which is how
      // every resource capture failed silently before (#1554).
      expect(uploads.filter((u) => !u.endsWith(" 200")), "an upload was refused").toEqual([]);

      // And the stored capture is served back as a real PNG rather than an
      // error page: a capture nobody can read is a capture that did not happen.
      const img = await request.get(`${API_BASE}/api/v1/resources/${id}/thumbnail`, {
        headers: { "X-API-Key": API_KEY },
      });
      expect(img.ok(), `serving ${id}: HTTP ${img.status()}`).toBeTruthy();
      expect(img.headers()["content-type"]).toContain("image/png");
      expect([...(await img.body()).subarray(0, 4)]).toEqual([0x89, 0x50, 0x4e, 0x47]);
    } finally {
      await removeResource(request, id);
    }
  });

  // A capture older than the file it came from is behind it, and the queue is
  // told so by the row rather than by anything the page remembers (#1554).
  test("a rewritten resource is captured again", async ({ page, request }) => {
    const id = await seedResource(request);

    try {
      await authenticate(page);
      await page.goto("/portal/resources");
      await page.locator("nav").waitFor({ state: "visible" });

      const capturedAt = async (): Promise<string | undefined> => {
        const res = await request.get(`${API_BASE}/api/v1/resources/${id}`, {
          headers: { "X-API-Key": API_KEY },
        });
        return res.ok() ? (await res.json()).thumbnail_captured_at : undefined;
      };

      await expect.poll(capturedAt, { timeout: 150_000, intervals: [2_000] }).toBeTruthy();
      const first = await capturedAt();

      // Replace the content: the capture now predates the file.
      const replace = await request.post(`${API_BASE}/api/v1/resources/${id}/content`, {
        headers: { "X-API-Key": API_KEY },
        multipart: {
          file: {
            name: "probe.md",
            mimeType: "text/markdown",
            buffer: Buffer.from("# rewritten\n\nDifferent prose.\n"),
          },
        },
      });
      expect(replace.ok(), `replacing content: HTTP ${replace.status()}`).toBeTruthy();

      // The tab learns what is pending on a five-minute poll, so a reload is
      // what makes this observable inside a test's patience -- and it is what a
      // person does anyway. The wait below would otherwise be measuring the
      // poll interval rather than the capture.
      await page.reload();
      await page.locator("nav").waitFor({ state: "visible" });

      await expect
        .poll(capturedAt, {
          timeout: 150_000,
          intervals: [2_000],
          message: "a rewritten resource was never captured again",
        })
        .not.toBe(first);
    } finally {
      await removeResource(request, id);
    }
  });

  // One box html2canvas cannot draw aborted the capture of the whole document,
  // so an asset containing a sub-pixel gradient never had a tile and never
  // would: the exception was swallowed, the queue spent its attempts on it, and
  // Recapture repeated the same throw (#1751).
  test("a document holding a box that cannot be drawn is still captured", async ({
    page,
    request,
  }) => {
    const id = await seedResource(request, {
      ext: "html",
      mimeType: "text/html",
      body: SUB_PIXEL_BARS,
    });

    try {
      await authenticate(page);
      await page.goto("/portal/resources");
      await page.locator("nav").waitFor({ state: "visible" });

      await expect
        .poll(async () => !!(await resourceRow(request, id))["thumbnail_s3_key"], {
          timeout: 150_000,
          intervals: [2_000],
          message: "a document with a sub-pixel gradient was never captured",
        })
        .toBe(true);

      // And what was stored is a picture of the document, not an error page.
      const img = await request.get(`${API_BASE}/api/v1/resources/${id}/thumbnail`, {
        headers: { "X-API-Key": API_KEY },
      });
      expect(img.ok(), `serving ${id}: HTTP ${img.status()}`).toBeTruthy();
      expect([...(await img.body()).subarray(0, 4)]).toEqual([0x89, 0x50, 0x4e, 0x47]);
      // The rest of the document is in it: a 400x300 tile of a rendered page is
      // thousands of bytes, where a blank one is a few hundred.
      expect((await img.body()).length).toBeGreaterThan(2_000);
    } finally {
      await removeResource(request, id);
    }
  });

  // Recapture was "discard the stored image, and a capturer will take another".
  // On a file that has never been captured there is nothing to discard, and the
  // resource viewer mounts no capturer at all, so the press did nothing
  // whatsoever -- for every managed resource there has ever been (#1753).
  test("Recapture takes a picture of a resource that has never had one", async ({
    page,
    request,
  }) => {
    const id = await seedResource(request);

    try {
      // The background queue would capture this file on its own within seconds,
      // which would make the press unfalsifiable: what is under test is that
      // the PRESS captures. So the queue is given nothing to do -- its work list
      // is answered empty -- and the only thing left in the page that can
      // produce an image is the button.
      for (const list of ["resources/thumbnails/pending", "portal/thumbnails/pending"]) {
        await page.route(`**/api/v1/${list}*`, (route) =>
          route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ resources: [], data: [], total: 0, limit: 25 }),
          }),
        );
      }

      const uploads: string[] = [];
      page.on("request", (req) => {
        if (req.method() === "PUT" && req.url().includes(`/resources/${id}/thumbnail`)) {
          uploads.push(req.url());
        }
      });

      await authenticate(page);
      await page.goto(`/portal/resources/${id}`);
      await page.getByTestId("thumbnail-panel").waitFor({ state: "visible" });

      await page.getByRole("button", { name: /recapture/i }).click();

      await expect
        .poll(async () => !!(await resourceRow(request, id))["thumbnail_s3_key"], {
          timeout: 120_000,
          intervals: [2_000],
          message: `Recapture produced no image. PUTs seen: ${uploads.join(", ") || "none"}`,
        })
        .toBe(true);

      // The panel shows the reader the tile they asked for rather than the
      // sentence about one being made.
      await expect(page.getByAltText(/^Thumbnail for /)).toBeVisible({ timeout: 30_000 });
    } finally {
      await removeResource(request, id);
    }
  });

  // The asset half of the same control. An asset holding a current tile is one
  // no capturer in the page is arming for, so the capture that follows the
  // press is the press's doing.
  test("Recapture replaces a portal asset's tile", async ({ page, request }) => {
    const id = await seedAsset(request);

    try {
      const uploads: number[] = [];
      page.on("response", (res) => {
        if (
          res.request().method() === "PUT" &&
          res.url().includes(`/portal/assets/${id}/thumbnail`)
        ) {
          uploads.push(res.status());
        }
      });

      await authenticate(page);
      await page.goto(`/portal/assets/${id}`);
      // The asset viewer keeps its metadata sidebar behind a control; the
      // Thumbnail panel is in it.
      await page.getByRole("button", { name: /show details/i }).click();
      await page.getByTestId("thumbnail-panel").waitFor({ state: "visible" });

      // The tile it starts with, captured because the asset is open and behind.
      await expect
        .poll(async () => !!(await assetRow(request, id))["thumbnail_s3_key"], {
          timeout: 120_000,
          intervals: [2_000],
          message: "an open asset with no tile was never captured",
        })
        .toBe(true);
      const before = uploads.length;

      await page.getByRole("button", { name: /recapture/i }).click();

      await expect
        .poll(() => uploads.length, {
          timeout: 120_000,
          intervals: [1_000],
          message: `Recapture produced no upload. Statuses seen: ${uploads.join(", ")}`,
        })
        .toBeGreaterThan(before);
      expect(uploads.filter((status) => status !== 200), "an upload was refused").toEqual([]);

      await expect
        .poll(async () => !!(await assetRow(request, id))["thumbnail_s3_key"], {
          timeout: 120_000,
          intervals: [2_000],
          message: "the asset was left with no tile after a recapture",
        })
        .toBe(true);
      await expect(page.getByAltText(/^Thumbnail for /)).toBeVisible({ timeout: 30_000 });
    } finally {
      await removeAsset(request, id);
    }
  });

  // A capture that fails used to report nothing: the panel said a picture was
  // being made, forever, and the exception naming the cause was thrown away on
  // every attempt (#1752). Here one is made to fail -- the content route is
  // answered 503, which is one of the ways a real capture fails -- and what the
  // reader is told is asserted, along with the console line that is the only
  // record a capture leaves anywhere.
  test("a capture that fails says so, with the reason and a retry", async ({ page, request }) => {
    const id = await seedResource(request);

    try {
      const console_: string[] = [];
      page.on("console", (m) => {
        if (m.type() === "error" && m.text().includes("thumbnail:")) console_.push(m.text());
      });
      // Nothing else in the page may capture this file, or the panel would be
      // showing a tile rather than the failure under test.
      for (const list of ["resources/thumbnails/pending", "portal/thumbnails/pending"]) {
        await page.route(`**/api/v1/${list}*`, (route) =>
          route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ resources: [], data: [], total: 0, limit: 25 }),
          }),
        );
      }
      await page.route(`**/api/v1/resources/${id}/content*`, (route) =>
        route.fulfill({ status: 503, contentType: "application/json", body: "{}" }),
      );

      await authenticate(page);
      await page.goto(`/portal/resources/${id}`);
      await page.getByTestId("thumbnail-panel").waitFor({ state: "visible" });
      await page.getByRole("button", { name: /recapture/i }).click();

      await expect(page.getByTestId("thumbnail-placeholder")).toContainText("Could not be made", {
        timeout: 30_000,
      });
      // What went wrong, and whether waiting will change it.
      await expect(page.getByTestId("thumbnail-explanation")).toContainText(
        "contents could not be read",
      );
      // And the way to try it anyway.
      await expect(page.getByRole("button", { name: /try again/i })).toBeEnabled();

      expect(console_.join("\n"), "the failure was not reported to the console").toContain(
        `capture of resource ${id} failed (content)`,
      );
    } finally {
      await removeResource(request, id);
    }
  });

  // Seven families the viewer lays out every day were never offered a capture,
  // because what gets a tile was a second list kept by hand beside the renderer
  // registry (#1754). Each is drawn on the platform's own background, so each
  // carries a capture per color scheme.
  for (const family of [
    { name: "YAML", ext: "yaml", mimeType: "application/yaml", body: "server:\n  address: \":8080\"\n  replicas: 2\n" },
    { name: "SQL", ext: "sql", mimeType: "application/sql", body: "SELECT region, sum(total)\nFROM orders\nGROUP BY 1;\n" },
    { name: "TSV", ext: "tsv", mimeType: "text/tab-separated-values", body: "region\trevenue\nwest\t41208\neast\t38112\n" },
  ]) {
    test(`a ${family.name} resource is captured in both color schemes`, async ({
      page,
      request,
    }) => {
      const id = await seedResource(request, family);

      try {
        await authenticate(page);
        await page.goto("/portal/resources");
        await page.locator("nav").waitFor({ state: "visible" });

        await expect
          .poll(
            async () => {
              const row = await resourceRow(request, id);
              return !!row["thumbnail_s3_key"] && !!row["thumbnail_dark_s3_key"];
            },
            {
              timeout: 150_000,
              intervals: [2_000],
              message: `a ${family.name} resource was never captured in both schemes`,
            },
          )
          .toBe(true);

        for (const variant of ["", "?variant=dark"]) {
          const img = await request.get(
            `${API_BASE}/api/v1/resources/${id}/thumbnail${variant}`,
            { headers: { "X-API-Key": API_KEY } },
          );
          expect(img.ok(), `serving ${id}${variant}: HTTP ${img.status()}`).toBeTruthy();
          expect([...(await img.body()).subarray(0, 4)]).toEqual([0x89, 0x50, 0x4e, 0x47]);
        }
      } finally {
        await removeResource(request, id);
      }
    });
  }

  // The asset queue used to capture eight and stop for five minutes, which to
  // anybody watching is a queue that quit (#1554). A stack whose remaining
  // pending assets are ones the capturer cannot render never drains, so what is
  // asserted is that the queue keeps ATTEMPTING rather than falling silent.
  test("the asset queue keeps working rather than stopping at eight", async ({ page, request }) => {
    const before = await pending(request, "assets");
    // The claim is that the queue does not stop after eight, and that is only
    // observable with more than eight to do. On a drained stack the few that
    // remain are ones the capturer cannot render at all -- a JSX asset whose
    // frame loads React from a CDN, for instance, which fails without network
    // egress and is correctly left alone after three tries. Asserting progress
    // there would be asserting that a correct refusal is a bug.
    test.skip(
      before.length <= 8,
      `needs more than eight pending assets to show the old ceiling; ${before.length} pending`,
    );

    const attempts: string[] = [];
    page.on("response", (res) => {
      const m = /\/api\/v1\/portal\/assets\/([^/]+)\/thumbnail/.exec(res.url());
      if (m && res.request().method() === "PUT") {
        attempts.push(`${m[1]} ${res.status()}`);
      }
    });

    await authenticate(page);
    await page.goto("/portal/assets");
    await page.locator("nav").waitFor({ state: "visible" });

    // Past the old ceiling, in one window: the budget was eight per poll and the
    // poll is five minutes apart, so a ninth capture inside this window is the
    // evidence the ceiling is gone.
    await expect
      .poll(() => attempts.length, {
        timeout: 150_000,
        intervals: [2_000],
        message: `the asset queue stopped early. Attempts: ${attempts.length}`,
      })
      .toBeGreaterThan(8);
  });
});
