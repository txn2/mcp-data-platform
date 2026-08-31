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
async function seedResource(api: APIRequestContext): Promise<string> {
  const name = `capture-probe-${Date.now()}`;
  const form = {
    multipart: {
      scope: "global",
      path: "references",
      display_name: name,
      description: "Live capture probe: a document that certainly needs a thumbnail.",
      file: {
        name: `${name}.md`,
        mimeType: "text/markdown",
        buffer: Buffer.from(`# ${name}\n\nProse for the capturer to render.\n`),
      },
    },
    headers: { "X-API-Key": API_KEY },
  };
  const res = await api.post(`${API_BASE}/api/v1/resources`, form);
  expect(res.ok(), `seeding a probe resource: HTTP ${res.status()}`).toBeTruthy();
  return (await res.json()).id as string;
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
