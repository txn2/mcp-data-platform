import { useState, useEffect } from "react";
import { Download, FileWarning } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { apiFetchRaw, BASE_URL } from "@/api/portal/client";
import { ContentLoadError } from "@/components/assetviewer/contentControls";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";
import { contentLoad, readsByRange } from "@/components/renderers/registry";
import { fetchContentText } from "@/lib/contentFetch";
import { formatBytes } from "@/lib/format";
import { useContentUrl } from "@/lib/useContentUrl";

interface Props {
  assetId: string;
  assetName: string;
  contentType: string;
  sizeBytes?: number;
  onClose: () => void;
}

/**
 * Modal overlay that renders an asset's content for quick preview.
 *
 * What it reads is the registry's contentLoad, the rule the asset page and the
 * admin viewer read too (#1874): an inline family under its own limit is
 * fetched as text, one past it is offered as a download without being
 * requested, and a family whose renderer loads the endpoint itself is handed
 * the endpoint. A read that fails shows its status with Retry and Download.
 *
 * The capped dialog shape keeps the asset's name and type in view while a long
 * document scrolls under them; Escape and the backdrop close it, both from the
 * dialog primitive rather than a hand-rolled key listener. Callers mount this
 * only while a preview is open, so the dialog is always open once rendered and
 * a close request goes straight back to them.
 */
export function AssetPreviewModal({ assetId, assetName, contentType, sizeBytes, onClose }: Props) {
  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent capped className="max-w-5xl" aria-describedby={undefined}>
        {/* text-left as well as flex-row: DialogHeader's default pair is
            `text-center sm:text-left`, so a row that only overrides the
            direction still centres its text on a narrow viewport. */}
        <DialogHeader className="shrink-0 flex-row items-center gap-3 border-b px-4 py-3 pr-12 text-left">
          <DialogTitle className="min-w-0 flex-1 truncate text-sm">{assetName}</DialogTitle>
          <span className="shrink-0 text-xs text-muted-foreground">{contentType}</span>
        </DialogHeader>

        <div className="min-h-0 flex-1 overflow-auto">
          <PreviewBody assetId={assetId} assetName={assetName} contentType={contentType} sizeBytes={sizeBytes} />
        </div>
      </DialogContent>
    </Dialog>
  );
}

/**
 * The text read a preview makes for a family that renders from text: nothing
 * unless `enabled`, and again on retry.
 */
function usePreviewText(assetId: string, enabled: boolean) {
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  // Bumped by retry, so the read runs again after a failure.
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    setContent(null);
    setError(null);
    fetchContentText(() => apiFetchRaw(`/assets/${assetId}/content`))
      .then((text) => {
        if (!cancelled) setContent(text);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err);
      });
    return () => { cancelled = true; };
  }, [assetId, enabled, attempt]);

  return { content, error, retry: () => setAttempt((n) => n + 1) };
}

function PreviewBody({ assetId, assetName, contentType, sizeBytes = 0 }: Omit<Props, "onClose">) {
  const contentUrl = `${BASE_URL}/assets/${assetId}/content`;
  const load = contentLoad(contentType, sizeBytes, assetName);
  const text = usePreviewText(assetId, load === "fetch");
  const media = useContentUrl(contentUrl, load === "url" && !readsByRange(contentType, assetName));
  // The read that failed is the one this family renders from.
  const failure = load === "url" ? { error: media.error, retry: media.retry } : text;
  const waiting = load === "url" ? media.loading : text.content === null;

  if (load === "too-large") {
    return <PreviewTooLarge name={assetName} sizeBytes={sizeBytes} contentUrl={contentUrl} />;
  }
  if (failure.error) {
    return (
      <ContentLoadError asset={{ name: assetName }} error={failure.error} contentUrl={contentUrl} onRetry={failure.retry} />
    );
  }
  if (waiting) {
    return (
      <div className="flex items-center justify-center py-20 text-muted-foreground text-sm">
        Loading...
      </div>
    );
  }
  return (
    <div className="p-4">
      <ContentRenderer
        contentType={contentType}
        content={text.content ?? undefined}
        fileName={assetName}
        contentUrl={media.src || contentUrl}
        sizeBytes={sizeBytes}
      />
    </div>
  );
}

function PreviewTooLarge({ name, sizeBytes, contentUrl }: { name: string; sizeBytes: number; contentUrl: string }) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-16 text-center">
      <FileWarning className="h-10 w-10 text-muted-foreground" />
      <p className="text-sm text-muted-foreground">Too large to preview ({formatBytes(sizeBytes)})</p>
      <Button asChild size="sm">
        <a href={contentUrl} download={name}>
          <Download />
          Download
        </a>
      </Button>
    </div>
  );
}
