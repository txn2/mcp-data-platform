import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Download } from "lucide-react";
import { authedFetch } from "@/api/authed";
import { siteFileContentUrl, useSite, useSiteRelease, type Site } from "@/api/portal/hooks/sites";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";
import { readsByRange, rendersFromURL } from "@/components/renderers/registry";
import { saveBlob } from "@/lib/download";
import { useContentUrl } from "@/lib/useContentUrl";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Failure } from "./SiteParts";

// Every site file, and every file of a release, opens in the portal's own
// viewer (#2629): the registry that draws asset and resource content draws a
// site file from the content route, which serves its bytes sandboxed, scriptable
// kinds as downloads, and by range so a video seeks. A site's HTML page is never
// drawn inside the portal: its relative styles, scripts and images would not
// load here.

/** The text kinds the viewer draws, each with a View beside its Source. */
const DRAWN = [/^text\/markdown/, /^text\/csv/, /^text\/tab-separated-values/, /^application\/json/, /^application\/x-ndjson/, /^application\/ndjson/, /^image\/svg\+xml/];
const DRAWN_NAMES = /\.(md|markdown|csv|tsv|json|ndjson|jsonl|svg)$/i;

/** Whether a text file has a drawn View as well as its source. */
export function drawable(contentType: string, path: string): boolean {
  return DRAWN.some((re) => re.test(contentType)) || DRAWN_NAMES.test(path);
}

/** Whether a file is a site's page, which opens on the site rather than here. */
export function isPage(contentType: string, path: string): boolean {
  return /^text\/html/.test(contentType) || /\.html?$/i.test(path);
}

/** The media type a /file refusal of a file that is not text names, else none. */
export function typeInRefusal(message: string): string {
  return message.match(/\b(?:image|video|audio|application|font|model)\/[a-z0-9.+-]+/i)?.[0] ?? "";
}

/** Saves a site file with the session's credentials: an API-key session has none an <a href> carries. */
async function download(url: string, name: string): Promise<void> {
  const res = await authedFetch(url);
  if (res.ok) saveBlob(await res.blob(), name);
}

/**
 * A file's bytes drawn by the registry, with Download beside the page's own
 * actions. An element the registry points at the content route is handed an
 * object URL on an API-key session, as the asset viewer is (useContentUrl).
 */
export function SiteFileViewer({ site, path, release, contentType, actions }: { site: string; path: string; release?: number; contentType: string; actions?: ReactNode }) {
  const url = siteFileContentUrl(site, path, release);
  const name = path.split("/").pop() ?? path;
  const media = useContentUrl(url, rendersFromURL(contentType, name) && !readsByRange(contentType, name));
  return (
    <div className="space-y-2" data-testid="file-viewer">
      <div className="flex justify-end gap-2">
        {actions}
        <Button size="sm" variant="outline" onClick={() => void download(url, name)}>
          <Download className="h-4 w-4" aria-hidden />
          Download
        </Button>
      </div>
      <div className="min-w-0 overflow-hidden rounded border">
        {media.error ? <Failure text={`The file could not be read (${media.error.message}).`} /> : media.src ? <ContentRenderer contentType={contentType} fileName={name} contentUrl={media.src} /> : <p className="p-3 text-sm text-muted-foreground">Loading…</p>}
      </div>
    </div>
  );
}

/** View or Source, for a text file the viewer draws. */
export function ViewSourceTabs({ view, onChange }: { view: "view" | "source"; onChange: (v: "view" | "source") => void }) {
  return (
    <Tabs value={view} onValueChange={(v) => onChange(v as "view" | "source")}>
      <TabsList>
        <TabsTrigger value="view">View</TabsTrigger>
        <TabsTrigger value="source">Source</TabsTrigger>
      </TabsList>
    </Tabs>
  );
}

/** The drawn View of a text file's body: what the draft says, unsaved edits included. */
export function DrawnView({ body, contentType, path }: { body: string; contentType: string; path: string }) {
  return (
    <div className="min-w-0 overflow-hidden rounded border" data-testid="file-view">
      <ContentRenderer contentType={contentType} fileName={path.split("/").pop() ?? path} content={body} />
    </div>
  );
}

/** A release file's text, read from the content route. */
function useReleaseText(url: string, enabled: boolean) {
  return useQuery({
    queryKey: ["site-file-text", url],
    queryFn: async () => {
      const res = await authedFetch(url);
      if (!res.ok) throw new Error(`The file could not be read (${res.status})`);
      return res.text();
    },
    enabled,
  });
}

// SiteReleaseFilePage is one file of a release, read-only in the viewer
// (#2629): a pruned release has no files, as on its page.
export function SiteReleaseFilePage({ site: name, number, path, onNavigate }: { site: string; number: number; path: string; onNavigate: (path: string) => void }) {
  const { data: site } = useSite(name);
  const { data: release, error } = useSiteRelease(name, number);
  const back = () => onNavigate(`/sites/${encodeURIComponent(name)}/releases/${number}`);
  if (error) return <Failure text={error.message} />;
  if (!site || !release) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const file = release.files.find((f) => f.path === path);
  return (
    <div className="space-y-4">
      <ReleaseFileHeader site={site} number={number} path={path} contentType={file?.content_type ?? ""} onBack={back} />
      {file ? <ReleaseFileBody site={name} number={number} path={path} contentType={file.content_type} /> : <Failure text={`Release ${number} has no file ${path}.`} />}
    </div>
  );
}

/** A release file's body: text drawn or shown as source, anything else in the viewer. */
function ReleaseFileBody({ site, number, path, contentType }: { site: string; number: number; path: string; contentType: string }) {
  const text = drawable(contentType, path) || isPage(contentType, path);
  const body = useReleaseText(siteFileContentUrl(site, path, number), text);
  if (!text) return <SiteFileViewer site={site} path={path} release={number} contentType={contentType} />;
  return <ReleaseText body={body.data} error={body.error?.message} contentType={contentType} path={path} />;
}

function ReleaseText({ body, error, contentType, path }: { body?: string; error?: string; contentType: string; path: string }) {
  if (error) return <Failure text={error} />;
  if (body === undefined) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (drawable(contentType, path)) return <DrawnView body={body} contentType={contentType} path={path} />;
  return (
    <pre className="max-h-[70dvh] overflow-auto whitespace-pre rounded border p-3 font-mono text-xs" data-testid="file-readonly">
      {body}
    </pre>
  );
}

function ReleaseFileHeader({ site, number, path, contentType, onBack }: { site: Site; number: number; path: string; contentType: string; onBack: () => void }) {
  return (
    <div className="space-y-2">
      <button type="button" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground" onClick={onBack}>
        <ArrowLeft className="h-4 w-4" aria-hidden />
        Release {number}
      </button>
      <h1 className="flex flex-wrap items-center gap-2 break-all font-mono text-lg font-semibold">
        {path}
        <Badge variant="muted">Release {number}</Badge>
      </h1>
      <p className="text-xs text-muted-foreground">
        {site.name} · {contentType.split(";")[0] || "file"} · read-only
      </p>
    </div>
  );
}
