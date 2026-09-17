import { expect, type Page, type APIRequestContext } from "@playwright/test";

// The live stack, as the thumbnail suites reach it: where it is, who they sign
// in as, and the probe rows they file and take away again. Shared because a
// suite that asserted against whatever the stack happened to hold would be
// reporting the state rather than the feature, and because the file that held
// them reached its size budget (#1771).

export const API_BASE = process.env["THUMBNAIL_API_URL"] ?? "http://localhost:28080";
export const API_KEY = process.env["THUMBNAIL_API_KEY"] ?? "acme-dev-key-2024";

/**
 * Sign the page in without touching the sign-in form.
 *
 * The auth store reads its key from sessionStorage at init, so seeding it is
 * the same act the form performs and none of the typing. It runs before any
 * document script, so the store is authenticated on first render.
 */
export async function authenticate(page: Page): Promise<void> {
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
export async function seedResource(
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
export async function resourceRow(
  api: APIRequestContext,
  id: string,
): Promise<Record<string, unknown>> {
  const res = await api.get(`${API_BASE}/api/v1/resources/${id}`, {
    headers: { "X-API-Key": API_KEY },
  });
  expect(res.ok(), `reading resource ${id}: HTTP ${res.status()}`).toBeTruthy();
  return (await res.json()) as Record<string, unknown>;
}


/** File a markdown asset that certainly needs a capture, and return its id. */
export async function seedAsset(api: APIRequestContext): Promise<string> {
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
export async function assetRow(api: APIRequestContext, id: string): Promise<Record<string, unknown>> {
  const res = await api.get(`${API_BASE}/api/v1/portal/assets/${id}`, {
    headers: { "X-API-Key": API_KEY },
  });
  expect(res.ok(), `reading asset ${id}: HTTP ${res.status()}`).toBeTruthy();
  return (await res.json()) as Record<string, unknown>;
}

/** Remove a probe asset so a run leaves the library as it found it. */
export async function removeAsset(api: APIRequestContext, id: string): Promise<void> {
  await api.delete(`${API_BASE}/api/v1/portal/assets/${id}`, {
    headers: { "X-API-Key": API_KEY },
  });
}

/** Remove a probe so a run leaves the library as it found it. */
export async function removeResource(api: APIRequestContext, id: string): Promise<void> {
  await api.delete(`${API_BASE}/api/v1/resources/${id}`, { headers: { "X-API-Key": API_KEY } });
}

/** What the platform still reports as needing a capture. */
export async function pending(api: APIRequestContext, kind: "resources" | "assets"): Promise<string[]> {
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

