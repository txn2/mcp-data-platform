import { formatBytes } from "@/lib/format";

/**
 * Downloads the portal saves itself (#2614): an asset, a managed resource and
 * an HTML or JSX asset's standalone file are read here with their progress
 * reported, then handed to the browser. The browser's own download bar
 * appears only once the whole file is handed to it, so until then the control
 * that started the download is the only thing that says it is running.
 */

/**
 * How far a download has got: preparing until the platform answers (an HTML or
 * JSX asset's standalone file is built first), then the bytes read so far out
 * of the size the answer declares, null when it declares none.
 */
export type DownloadProgress = { phase: "preparing" } | { phase: "downloading"; received: number; total: number | null };

/**
 * The size an answer declares for its body, null when it declares none or the
 * bytes read will not add up to it: a compressed body is declared at its
 * compressed size and read decompressed.
 */
export function declaredSize(res: Response): number | null {
  if (res.headers.get("Content-Encoding")) return null;
  const n = Number(res.headers.get("Content-Length"));
  return Number.isFinite(n) && n > 0 ? n : null;
}

/** Reads an answer's body to a blob, reporting the bytes read after each piece. */
export async function readBody(res: Response, onBytes: (received: number) => void): Promise<Blob> {
  const type = res.headers.get("Content-Type") ?? "";
  if (!res.body) {
    const blob = await res.blob();
    onBytes(blob.size);
    return blob;
  }
  const reader = res.body.getReader();
  const parts: BlobPart[] = [];
  let received = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    parts.push(value as BlobPart);
    received += value.byteLength;
    onBytes(received);
  }
  return new Blob(parts, { type });
}

/** What opening a download came to: the body read, or the status it was refused with (null: the platform was not reached). */
export type Fetched = { ok: true; res: Response; blob: Blob } | { ok: false; status: number | null; res?: Response };

/**
 * How often, at most, the bytes read are reported for a body of no declared
 * size. A body of a known size is reported once per whole percent, so a
 * control re-renders at most a hundred times however large the file.
 */
export const PROGRESS_INTERVAL_MS = 100;

/**
 * Opens a download and reads its body, reporting progress: preparing until the
 * platform answers when preparing is set, downloading from the start
 * otherwise. A refused answer is returned unread, so the caller can read why
 * from it.
 */
export async function fetchWithProgress(open: () => Promise<Response>, report: (p: DownloadProgress) => void, preparing = false): Promise<Fetched> {
  report(preparing ? { phase: "preparing" } : { phase: "downloading", received: 0, total: null });
  let res: Response;
  try {
    res = await open();
  } catch {
    return { ok: false, status: null };
  }
  if (!res.ok) return { ok: false, status: res.status, res };
  const total = declaredSize(res);
  report({ phase: "downloading", received: 0, total });
  let lastShare = 0;
  let lastAt = 0;
  try {
    const blob = await readBody(res, (received) => {
      const p: DownloadProgress = { phase: "downloading", received, total };
      if (total) {
        const share = percent(p) ?? 0;
        if (share === lastShare) return;
        lastShare = share;
      } else {
        const now = Date.now();
        if (now - lastAt < PROGRESS_INTERVAL_MS) return;
        lastAt = now;
      }
      report(p);
    });
    report({ phase: "downloading", received: blob.size, total });
    return { ok: true, res, blob };
  } catch {
    return { ok: false, status: null };
  }
}

/** What a failed download says: the status the platform answered, or that it was not reached. */
export function failureMessage(status: number | null): string {
  return status === null ? "Download failed: the platform could not be reached." : `Download failed: the platform answered ${status}.`;
}

/** The share read, 0 to 100, null while preparing or when the size is not known. */
export function percent(p: DownloadProgress): number | null {
  if (p.phase !== "downloading" || !p.total) return null;
  return Math.min(100, Math.floor((p.received / p.total) * 100));
}

/** What a running download's control says: "Preparing…", "Downloading 42%" or "Downloading 12.4 MB". */
export function progressLabel(p: DownloadProgress): string {
  if (p.phase === "preparing") return "Preparing…";
  const share = percent(p);
  if (share !== null) return `Downloading ${share}%`;
  return p.received > 0 ? `Downloading ${formatBytes(p.received)}` : "Downloading…";
}

/** The bytes behind the label: "12.4 MB of 29.6 MB", or "12.4 MB so far" when the size is not known. */
export function progressDetail(p: DownloadProgress): string {
  if (p.phase === "preparing") return "Making one file that opens offline";
  if (p.total) return `${formatBytes(p.received)} of ${formatBytes(p.total)}`;
  return p.received > 0 ? `${formatBytes(p.received)} so far` : "Waiting for the platform";
}

/** Saves a blob under a file name through a temporary link. */
export function saveBlob(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}
