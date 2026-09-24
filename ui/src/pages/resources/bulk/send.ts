/**
 * Sending one file of a bulk upload to POST /api/v1/resources (#1862).
 *
 * XMLHttpRequest rather than fetch because fetch reports no upload progress,
 * and a batch of large images with no progress reads as a hung page. The auth
 * and CSRF headers are the ones every resource request carries
 * (api/resources/client.ts).
 */

import { applyCsrfHeader } from "@/api/csrf";
import { BASE_URL } from "@/api/resources/client";
import type { ScopeTarget } from "../scopes";
import { useAuthStore } from "@/stores/auth";

/**
 * What the server did with a file it accepted: stored it new, recorded it as the
 * next version of the file already at that address, or found the same bytes
 * already there and wrote nothing.
 */
export type UploadOutcome = "created" | "revised" | "unchanged";

/** The answer to one upload. */
export interface SendResult {
  ok: boolean;
  status: number;
  outcome?: UploadOutcome;
  /** The server's own reason, for a refused upload. */
  error?: string;
}

/** Everything one file's form carries. */
export interface SendFields {
  target: ScopeTarget;
  path: string;
  displayName: string;
  description: string;
  tags: string[];
  filename: string;
  file: File;
  /** Declared for a file the browser handed over untyped. */
  mimeType: string;
}

/**
 * sendForm builds the multipart body. The file goes last: the server streams
 * that part to storage where it finds it and refuses a form with a part behind
 * it, so every field, if_exists included, is appended first.
 */
export function sendForm(f: SendFields): FormData {
  const fd = new FormData();
  fd.set("scope", f.target.scope);
  if (f.target.scope_id) fd.set("scope_id", f.target.scope_id);
  fd.set("path", f.path);
  fd.set("display_name", f.displayName.trim());
  fd.set("description", f.description);
  for (const t of f.tags) fd.append("tags", t);
  // An address already holding the same bytes is left alone and one holding
  // different bytes gets a new version, so re-uploading a folder that is
  // mostly unchanged writes only what changed.
  fd.set("if_exists", "skip_unchanged");
  const body = f.mimeType && f.file.type === "" ? new Blob([f.file], { type: f.mimeType }) : f.file;
  fd.set("file", body, f.filename);
  return fd;
}

/** readOutcome is the outcome the server named, or undefined when it named none. */
function readOutcome(body: { outcome?: string }): UploadOutcome | undefined {
  if (body.outcome === "revised" || body.outcome === "unchanged" || body.outcome === "created") {
    return body.outcome;
  }
  return undefined;
}

/** parseBody reads a JSON body, or an empty object for one that is not JSON. */
function parseBody(text: string): { outcome?: string; error?: string } {
  try {
    const parsed: unknown = JSON.parse(text);
    return parsed && typeof parsed === "object" ? (parsed as { outcome?: string; error?: string }) : {};
  } catch {
    return {};
  }
}

/** settle turns a finished request into its result. */
function settle(xhr: XMLHttpRequest): SendResult {
  const body = parseBody(xhr.responseText ?? "");
  if (xhr.status === 401) useAuthStore.getState().expireSession();
  if (xhr.status >= 200 && xhr.status < 300) {
    const outcome = readOutcome(body);
    // Every create names what it did. An answer that names nothing is not one
    // this client can report as stored, so it is shown as the failure it is.
    if (!outcome) return { ok: false, status: xhr.status, error: "The server did not say what it did with this file." };
    return { ok: true, status: xhr.status, outcome };
  }
  return { ok: false, status: xhr.status, error: body.error || xhr.statusText || `HTTP ${xhr.status}` };
}

/**
 * sendFile uploads one form and reports progress as a fraction of the bytes
 * sent. It never rejects: a refusal and a dropped connection are both results,
 * so one file's failure is recorded against that file and nothing else.
 */
export function sendFile(
  form: FormData,
  onProgress: (fraction: number) => void,
  makeRequest: () => XMLHttpRequest = () => new XMLHttpRequest(),
): Promise<SendResult> {
  return new Promise((resolve) => {
    const xhr = makeRequest();
    xhr.open("POST", BASE_URL);
    xhr.withCredentials = true;
    const headers: Record<string, string> = {};
    const { apiKey, authMethod } = useAuthStore.getState();
    if (authMethod === "apikey" && apiKey) headers["X-API-Key"] = apiKey;
    applyCsrfHeader(headers, "POST");
    for (const [name, value] of Object.entries(headers)) xhr.setRequestHeader(name, value);
    xhr.upload.onprogress = (e: ProgressEvent) => {
      if (e.lengthComputable && e.total > 0) onProgress(e.loaded / e.total);
    };
    xhr.onload = () => resolve(settle(xhr));
    xhr.onerror = () =>
      resolve({ ok: false, status: 0, error: "The connection closed before the server answered." });
    xhr.send(form);
  });
}
