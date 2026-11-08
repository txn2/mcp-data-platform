import { useCallback, useState, type ReactNode } from "react";
import { Download } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { resolveRenderer } from "@/components/renderers/registry";

/**
 * Download of an HTML or JSX asset as one self-contained file (#2530).
 *
 * The stored source of a deck or a JSX dashboard does not run on its own: it
 * names the platform's served runtimes, its declared images and fonts, and for
 * JSX the libraries the viewer resolves. Each content route a portal or share
 * page downloads from has a .../standalone beside it that answers one HTML file
 * with all of that inside it. An asset that reads the platform's data while it
 * renders (a declared data file, an embedded form, the basemap archive) is
 * answered 409, and the reader is offered the source as stored instead
 * (?source=stored on the content route). Every other content type downloads as
 * it always has.
 */

/** A file the standalone download left as its URL, and why. */
export interface StandaloneNotice {
  url: string;
  reason: string;
}

/** The header the platform names those files in, as URL-escaped JSON. */
export const NOTICES_HEADER = "X-Standalone-Notices";

/**
 * The content routes a standalone route sits beside: an asset on the portal,
 * and a share's asset or collection item. The admin console's content route
 * has none.
 */
const CONTENT_ROUTE = /^(?:\/api\/v1\/portal\/assets\/[^/?#]+|\/portal\/view\/[^/?#]+(?:\/items\/[^/?#]+)?)\/content$/;

/** Headers a page adds to its own reads: the portal's API-key sign-in. */
export type AuthHeaders = () => Record<string, string>;
const noHeaders: AuthHeaders = () => ({});

/** Whether an asset of this type downloads as a standalone file. */
export function isStandaloneType(contentType: string, fileName?: string): boolean {
  const kind = resolveRenderer({ contentType, fileName }).kind;
  return kind === "html" || kind === "jsx";
}

/**
 * The standalone route beside a content route, or null for a URL that is not
 * one (a version's content, a signed URL), which downloads as stored.
 */
export function standaloneUrlFor(contentUrl: string): string | null {
  const bare = contentUrl.split(/[?#]/)[0] ?? "";
  if (!CONTENT_ROUTE.test(bare)) return null;
  return bare.replace(/\/content$/, "/standalone");
}

/** The content route asked for the asset as stored, with no rewrite. */
export function storedSourceUrlFor(contentUrl: string): string {
  const bare = contentUrl.split("#")[0] ?? contentUrl;
  return bare + (bare.includes("?") ? "&" : "?") + "source=stored";
}

/** What a standalone download came to. */
export type StandaloneOutcome =
  | { kind: "saved"; notices: StandaloneNotice[] }
  | { kind: "platform-data"; reasons: string[] }
  | { kind: "failed"; message: string; reasons: string[] };

export function readNotices(header: string | null): StandaloneNotice[] {
  if (!header) return [];
  try {
    const parsed: unknown = JSON.parse(decodeURIComponent(header.replace(/\+/g, " ")));
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (n): n is StandaloneNotice =>
        typeof n === "object" && n !== null && typeof (n as StandaloneNotice).url === "string",
    );
  } catch {
    return [];
  }
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

/** The file name a standalone download is saved under. */
export function standaloneFileName(name: string): string {
  const dot = name.lastIndexOf(".");
  return (dot > 0 ? name.slice(0, dot) : name) + ".html";
}

async function readReasons(res: Response): Promise<string[]> {
  try {
    const body = (await res.json()) as { reasons?: unknown };
    return Array.isArray(body.reasons) ? body.reasons.filter((r): r is string => typeof r === "string") : [];
  } catch {
    return [];
  }
}

/** Fetches the standalone file and saves it, or says why it did not. */
export async function downloadStandalone(
  standaloneUrl: string,
  name: string,
  headers: AuthHeaders = noHeaders,
): Promise<StandaloneOutcome> {
  let res: Response;
  try {
    res = await fetch(standaloneUrl, { credentials: "include", headers: headers() });
  } catch {
    return { kind: "failed", message: "The download could not reach the platform.", reasons: [] };
  }
  if (res.status === 409) {
    return { kind: "platform-data", reasons: await readReasons(res) };
  }
  if (!res.ok) {
    const reasons = res.status === 422 ? await readReasons(res) : [];
    const message =
      res.status === 422
        ? "This asset could not be built into a standalone file."
        : `The standalone file could not be made (${res.status}).`;
    return { kind: "failed", message, reasons };
  }
  saveBlob(await res.blob(), standaloneFileName(name));
  return { kind: "saved", notices: readNotices(res.headers.get(NOTICES_HEADER)) };
}

/** Saves what a URL answers, under a file name. */
export async function downloadUrl(url: string, name: string, headers: AuthHeaders = noHeaders): Promise<boolean> {
  try {
    const res = await fetch(url, { credentials: "include", headers: headers() });
    if (!res.ok) return false;
    saveBlob(await res.blob(), name);
    return true;
  } catch {
    return false;
  }
}

/** Saves the asset as stored. */
export function downloadStoredSource(contentUrl: string, name: string, headers: AuthHeaders = noHeaders): Promise<boolean> {
  return downloadUrl(storedSourceUrlFor(contentUrl), name, headers);
}

interface Pending {
  outcome: StandaloneOutcome;
  contentUrl: string;
  name: string;
}

/**
 * The standalone download for one page: `start` downloads an asset, and
 * `dialog` is what the page renders to show what came of it.
 */
export function useStandaloneDownload(headers: AuthHeaders = noHeaders): {
  start: (contentUrl: string, name: string, contentType: string) => Promise<void>;
  busy: boolean;
  dialog: ReactNode;
} {
  const [pending, setPending] = useState<Pending | null>(null);
  const [busy, setBusy] = useState(false);

  const start = useCallback(async (contentUrl: string, name: string, contentType: string) => {
    const standaloneUrl = isStandaloneType(contentType, name) ? standaloneUrlFor(contentUrl) : null;
    setBusy(true);
    try {
      // Any other type, or a content URL with no standalone route beside
      // it, downloads what the content route serves, as it always has.
      if (!standaloneUrl) {
        await downloadUrl(contentUrl, name, headers);
        return;
      }
      const outcome = await downloadStandalone(standaloneUrl, name, headers);
      if (outcome.kind === "saved" && outcome.notices.length === 0) return;
      setPending({ outcome, contentUrl, name });
    } finally {
      setBusy(false);
    }
  }, [headers]);

  const close = () => setPending(null);
  const dialog = pending ? (
    <StandaloneDialog
      outcome={pending.outcome}
      onClose={close}
      onDownloadSource={() => {
        void downloadStoredSource(pending.contentUrl, pending.name, headers);
        close();
      }}
    />
  ) : null;
  return { start, busy, dialog };
}

/** What the reader is shown when the download is not a plain save. */
export function StandaloneDialog({
  outcome,
  onClose,
  onDownloadSource,
}: {
  outcome: StandaloneOutcome;
  onClose: () => void;
  onDownloadSource: () => void;
}) {
  let title: string;
  let description: string;
  let items: string[];
  let offerSource = true;
  switch (outcome.kind) {
    case "platform-data":
      title = "This asset reads data from the platform";
      description = "A downloaded file would open without that data. You can download the source as stored instead.";
      items = outcome.reasons;
      break;
    case "failed":
      title = "Could not make a standalone file";
      description = outcome.message;
      items = outcome.reasons;
      break;
    case "saved":
      title = "Downloaded";
      description = "These files could not be put inside the file, so the parts that use them need the network:";
      items = outcome.notices.map((n) => `${n.url} (${n.reason})`);
      offerSource = false;
      break;
  }
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {items.length > 0 && (
          <ul className="max-h-60 list-disc space-y-1 overflow-auto pl-5 text-sm text-muted-foreground break-all">
            {items.map((item) => (
              <li key={item}>{item}</li>
            ))}
          </ul>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {offerSource ? "Cancel" : "Close"}
          </Button>
          {offerSource && (
            <Button variant="secondary" onClick={onDownloadSource}>
              <Download />
              Download source
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * A Download control: the standalone download for an HTML or JSX asset on a
 * content route that has one, and a plain link to the content for anything
 * else, as it has always been.
 */
export function DownloadButton({
  contentUrl,
  name,
  contentType,
  size,
  authHeaders = noHeaders,
}: {
  contentUrl: string;
  name: string;
  contentType: string;
  size?: "sm" | "default";
  /** The page's own read headers; the portal passes its API-key sign-in. */
  authHeaders?: AuthHeaders;
}) {
  const { start, busy, dialog } = useStandaloneDownload(authHeaders);
  if (!isStandaloneType(contentType, name) || !standaloneUrlFor(contentUrl)) {
    return (
      <Button asChild size={size}>
        <a href={contentUrl} download={name}>
          <Download />
          Download
        </a>
      </Button>
    );
  }
  return (
    <>
      <Button size={size} disabled={busy} onClick={() => void start(contentUrl, name, contentType)}>
        <Download />
        {busy ? "Preparing…" : "Download"}
      </Button>
      {dialog}
    </>
  );
}
