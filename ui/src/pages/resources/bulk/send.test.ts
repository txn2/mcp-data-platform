import { afterEach, describe, expect, it } from "vitest";
import { useAuthStore } from "@/stores/auth";
import { sendFile, sendForm } from "./send";

/** FakeXHR records what was sent and answers when told to. */
class FakeXHR {
  method = "";
  url = "";
  withCredentials = false;
  headers: Record<string, string> = {};
  body: unknown = null;
  status = 0;
  statusText = "";
  responseText = "";
  upload: { onprogress: ((e: ProgressEvent) => void) | null } = { onprogress: null };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }
  setRequestHeader(name: string, value: string) {
    this.headers[name] = value;
  }
  send(body: unknown) {
    this.body = body;
  }
  answer(status: number, body: string, statusText = "") {
    this.status = status;
    this.statusText = statusText;
    this.responseText = body;
    this.onload?.();
  }
}

function run(answer: (x: FakeXHR) => void, progress: number[] = []) {
  const xhr = new FakeXHR();
  const p = sendFile(new FormData(), (f) => progress.push(f), () => xhr as unknown as XMLHttpRequest);
  answer(xhr);
  return { p, xhr };
}

afterEach(() => useAuthStore.setState({ authMethod: undefined, apiKey: undefined, csrfToken: undefined } as never));

describe("sendForm", () => {
  it("puts every field ahead of the file and asks to skip unchanged files", () => {
    const file = new File(["x"], "orig.png");
    const fd = sendForm({
      target: { scope: "persona", scope_id: "brand" },
      path: "brand/logos",
      displayName: " Acme ",
      description: "Acme logo",
      tags: ["brand", "logo"],
      filename: "acme.png",
      file,
      mimeType: "image/png",
    });
    const keys = Array.from(fd.keys());
    expect(keys).toEqual(["scope", "scope_id", "path", "display_name", "description", "tags", "tags", "if_exists", "file"]);
    expect(fd.get("if_exists")).toBe("skip_unchanged");
    expect(fd.get("display_name")).toBe("Acme");
    const sent = fd.get("file") as File;
    expect(sent.name).toBe("acme.png");
    expect(sent.type).toBe("image/png");
  });

  it("keeps a typed file's own type and omits an empty scope id", () => {
    const fd = sendForm({
      target: { scope: "global", scope_id: "" },
      path: "p",
      displayName: "a",
      description: "d",
      tags: [],
      filename: "a.eps",
      file: new File(["x"], "a.eps", { type: "application/postscript" }),
      mimeType: "",
    });
    expect(fd.has("scope_id")).toBe(false);
    expect((fd.get("file") as File).type).toBe("application/postscript");
  });
});

describe("sendFile", () => {
  it("reports each outcome the server names", async () => {
    for (const outcome of ["created", "revised", "unchanged"]) {
      const { p } = run((x) => x.answer(outcome === "created" ? 201 : 200, JSON.stringify({ outcome })));
      expect(await p).toEqual({ ok: true, status: outcome === "created" ? 201 : 200, outcome });
    }
  });

  it("reports a success that names no outcome as a failure", async () => {
    for (const [status, text] of [[201, "{}"], [200, "not json"]] as const) {
      const got = await run((x) => x.answer(status, text)).p;
      expect(got.ok).toBe(false);
      expect(got.error).toContain("did not say");
    }
  });

  it("carries the server's reason for a refusal", async () => {
    const { p } = run((x) => x.answer(400, JSON.stringify({ error: 'file extension ".exe" is not allowed' })));
    expect(await p).toEqual({ ok: false, status: 400, error: 'file extension ".exe" is not allowed' });
    const bare = await run((x) => x.answer(503, "", "Service Unavailable")).p;
    expect(bare.error).toBe("Service Unavailable");
    const nothing = await run((x) => x.answer(500, "null")).p;
    expect(nothing.error).toBe("HTTP 500");
  });

  it("expires the session on a 401", async () => {
    let expired = false;
    useAuthStore.setState({ expireSession: () => (expired = true) } as never);
    await run((x) => x.answer(401, "{}")).p;
    expect(expired).toBe(true);
  });

  it("reports a dropped connection as a result, not a rejection", async () => {
    const { p } = run((x) => x.onerror?.());
    expect(await p).toMatchObject({ ok: false, status: 0 });
  });

  it("reports progress and sends the auth headers", async () => {
    useAuthStore.setState({ authMethod: "apikey", apiKey: "k1" } as never);
    const progress: number[] = [];
    const { p, xhr } = run((x) => {
      x.upload.onprogress?.({ lengthComputable: true, loaded: 5, total: 10 } as ProgressEvent);
      x.upload.onprogress?.({ lengthComputable: false, loaded: 5, total: 0 } as ProgressEvent);
      x.answer(201, "{}");
    }, progress);
    await p;
    expect(progress).toEqual([0.5]);
    expect(xhr.method).toBe("POST");
    expect(xhr.url).toBe("/api/v1/resources");
    expect(xhr.withCredentials).toBe(true);
    expect(xhr.headers["X-API-Key"]).toBe("k1");
  });

  it("sends the CSRF token under cookie auth", async () => {
    useAuthStore.setState({ authMethod: "cookie", csrfToken: "t1" } as never);
    const { p, xhr } = run((x) => x.answer(201, "{}"));
    await p;
    expect(xhr.headers["X-CSRF-Token"]).toBe("t1");
  });
});
