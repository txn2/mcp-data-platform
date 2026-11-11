import { describe, it, expect } from "vitest";
import { declaredSize, failureMessage, fetchWithProgress, percent, progressDetail, progressLabel, type DownloadProgress } from "./download";

// #2614: a download reports how far it has got, and a failure says why.

/** An answer whose body arrives in the pieces given. */
function streamed(pieces: string[], headers: Record<string, string> = {}, status = 200): Response {
  const enc = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      for (const p of pieces) c.enqueue(enc.encode(p));
      c.close();
    },
  });
  return new Response(body, { status, headers });
}

describe("declaredSize", () => {
  it("is the Content-Length of an uncompressed body", () => {
    expect(declaredSize(new Response("x", { headers: { "Content-Length": "31037849" } }))).toBe(31037849);
  });

  it("is not known for a compressed body, read at another size, or with no length", () => {
    expect(declaredSize(new Response("x", { headers: { "Content-Length": "10", "Content-Encoding": "gzip" } }))).toBeNull();
    expect(declaredSize(new Response(null))).toBeNull();
    expect(declaredSize(new Response("x", { headers: { "Content-Length": "nope" } }))).toBeNull();
  });
});

describe("what a running download says", () => {
  const known: DownloadProgress = { phase: "downloading", received: 13_002_342, total: 31_037_849 };
  const unknown: DownloadProgress = { phase: "downloading", received: 13_002_342, total: null };

  it("says Preparing… while the standalone file is built", () => {
    expect(progressLabel({ phase: "preparing" })).toBe("Preparing…");
    expect(progressDetail({ phase: "preparing" })).toBe("Making one file that opens offline");
    expect(percent({ phase: "preparing" })).toBeNull();
  });

  it("gives the share read when the size is known, and the bytes on hover", () => {
    expect(progressLabel(known)).toBe("Downloading 41%");
    expect(progressDetail(known)).toBe("12.4 MB of 29.6 MB");
    expect(percent({ phase: "downloading", received: 50, total: 40 })).toBe(100);
  });

  it("counts the bytes when the size is not known", () => {
    expect(progressLabel(unknown)).toBe("Downloading 12.4 MB");
    expect(progressDetail(unknown)).toBe("12.4 MB so far");
    expect(progressLabel({ phase: "downloading", received: 0, total: null })).toBe("Downloading…");
  });

  it("names the status a failure was answered with, or that the platform was not reached", () => {
    expect(failureMessage(500)).toBe("Download failed: the platform answered 500.");
    expect(failureMessage(null)).toBe("Download failed: the platform could not be reached.");
  });
});

describe("fetchWithProgress", () => {
  it("reports preparing, then the bytes read, and returns the whole body", async () => {
    const seen: DownloadProgress[] = [];
    const got = await fetchWithProgress(async () => streamed(["abc", "defg"], { "Content-Length": "7", "Content-Type": "text/plain" }), (p) => seen.push(p), true);
    expect(got.ok).toBe(true);
    if (!got.ok) return;
    expect(await got.blob.text()).toBe("abcdefg");
    expect(got.blob.type).toBe("text/plain");
    expect(seen[0]).toEqual({ phase: "preparing" });
    expect(seen[1]).toEqual({ phase: "downloading", received: 0, total: 7 });
    expect(seen[seen.length - 1]).toEqual({ phase: "downloading", received: 7, total: 7 });
  });

  it("starts at downloading when there is nothing to prepare", async () => {
    const seen: DownloadProgress[] = [];
    await fetchWithProgress(async () => streamed(["a"]), (p) => seen.push(p));
    expect(seen[0]).toEqual({ phase: "downloading", received: 0, total: null });
  });

  it("returns a refusal unread, with its status", async () => {
    const got = await fetchWithProgress(async () => new Response(JSON.stringify({ reasons: ["x"] }), { status: 409 }), () => {});
    expect(got.ok).toBe(false);
    if (got.ok) return;
    expect(got.status).toBe(409);
    expect(await got.res?.json()).toEqual({ reasons: ["x"] });
  });

  it("says the platform was not reached when the request or the body fails", async () => {
    expect(await fetchWithProgress(() => Promise.reject(new Error("offline")), () => {})).toEqual({ ok: false, status: null });
    const broken = new Response(
      new ReadableStream({
        pull(c) {
          c.error(new Error("reset"));
        },
      }),
    );
    expect(await fetchWithProgress(async () => broken, () => {})).toEqual({ ok: false, status: null });
  });
});
