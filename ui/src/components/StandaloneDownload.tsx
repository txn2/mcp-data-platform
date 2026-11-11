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
import { DownloadControl } from "@/components/download/DownloadControl";
import { DownloadStatus } from "@/components/download/DownloadStatus";
import { saveDownload, useDownload } from "@/components/download/useDownload";
import { failureMessage, fetchWithProgress, saveBlob, type DownloadProgress } from "@/lib/download";

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

/**
 * What a standalone download came to. A refusal the reader can act on
 * (platform data, a build that failed) is shown in a dialog; any other status,
 * or no answer, is "error" and is told the way every failed download is
 * (#2614).
 */
export type StandaloneOutcome =
  | { kind: "saved"; notices: StandaloneNotice[] }
  | { kind: "platform-data"; reasons: string[] }
  | { kind: "failed"; message: string; reasons: string[] }
  | { kind: "error"; status: number | null };

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

/**
 * Builds the standalone file and saves it, or says why it did not: preparing
 * until the platform answers, then the bytes read (#2614).
 */
export async function downloadStandalone(
  standaloneUrl: string,
  name: string,
  headers: AuthHeaders = noHeaders,
  report: (p: DownloadProgress) => void = () => {},
): Promise<StandaloneOutcome> {
  const got = await fetchWithProgress(() => fetch(standaloneUrl, { credentials: "include", headers: headers() }), report, true);
  if (got.ok) {
    saveBlob(got.blob, standaloneFileName(name));
    return { kind: "saved", notices: readNotices(got.res.headers.get(NOTICES_HEADER)) };
  }
  if (got.res?.status === 409) return { kind: "platform-data", reasons: await readReasons(got.res) };
  if (got.res?.status === 422) {
    return { kind: "failed", message: "This asset could not be built into a standalone file.", reasons: await readReasons(got.res) };
  }
  return { kind: "error", status: got.status };
}

/** A read of a URL with the page's own headers. */
function opener(url: string, headers: AuthHeaders): () => Promise<Response> {
  return () => fetch(url, { credentials: "include", headers: headers() });
}

interface Pending {
  outcome: StandaloneOutcome;
  contentUrl: string;
  name: string;
}

/**
 * The download for one page: `start` downloads an asset, `progress` is how far
 * it has got for the control that started it, and `dialog` is what the page
 * renders to show what came of it, the download's status line on a phone and
 * its failure included (#2614).
 */
export function useStandaloneDownload(headers: AuthHeaders = noHeaders): {
  start: (contentUrl: string, name: string, contentType: string) => Promise<void>;
  busy: boolean;
  progress: DownloadProgress | null;
  dialog: ReactNode;
} {
  const [pending, setPending] = useState<Pending | null>(null);
  const download = useDownload();
  const run = download.run;

  const start = useCallback(
    (contentUrl: string, name: string, contentType: string) => {
      const standaloneUrl = isStandaloneType(contentType, name) ? standaloneUrlFor(contentUrl) : null;
      // Any other type, or a content URL with no standalone route beside
      // it, downloads what the content route serves, as it always has.
      if (!standaloneUrl) return run(name, saveDownload(opener(contentUrl, headers), name));
      return run(standaloneFileName(name), async (report) => {
        const outcome = await downloadStandalone(standaloneUrl, name, headers, report);
        if (outcome.kind === "error") return failureMessage(outcome.status);
        if (outcome.kind !== "saved" || outcome.notices.length > 0) setPending({ outcome, contentUrl, name });
        return null;
      });
    },
    [headers, run],
  );

  const close = () => setPending(null);
  const dialog = (
    <>
      {pending && (
        <StandaloneDialog
          outcome={pending.outcome}
          onClose={close}
          onDownloadSource={() => {
            void run(pending.name, saveDownload(opener(storedSourceUrlFor(pending.contentUrl), headers), pending.name));
            close();
          }}
        />
      )}
      <DownloadStatus download={download} />
    </>
  );
  return { start, busy: download.busy, progress: download.progress, dialog };
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
    case "error":
      title = "Could not make a standalone file";
      description = failureMessage(outcome.status);
      items = [];
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
  const { start, progress, dialog } = useStandaloneDownload(authHeaders);
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
      <DownloadControl progress={progress} variant="default" size={size} onClick={() => void start(contentUrl, name, contentType)} />
      {dialog}
    </>
  );
}
