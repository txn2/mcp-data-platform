import { useState, useEffect } from "react";
import { Download, FileWarning } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { apiFetchRaw } from "@/api/portal/client";
import { LARGE_ASSET_THRESHOLD } from "@/api/portal/hooks";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";
import { formatBytes } from "@/lib/format";

interface Props {
  assetId: string;
  assetName: string;
  contentType: string;
  sizeBytes?: number;
  onClose: () => void;
}

/**
 * Modal overlay that fetches and renders an asset's content for quick preview.
 * Skips loading for assets exceeding LARGE_ASSET_THRESHOLD.
 *
 * The capped dialog shape keeps the asset's name and type in view while a long
 * document scrolls under them; Escape and the backdrop close it, both from the
 * dialog primitive rather than a hand-rolled key listener. Callers mount this
 * only while a preview is open, so the dialog is always open once rendered and
 * a close request goes straight back to them.
 */
export function AssetPreviewModal({ assetId, assetName, contentType, sizeBytes, onClose }: Props) {
  const tooLarge = sizeBytes != null && sizeBytes > LARGE_ASSET_THRESHOLD;
  const [content, setContent] = useState<string | null>(null);
  const [loading, setLoading] = useState(!tooLarge);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (tooLarge) return;
    let cancelled = false;
    apiFetchRaw(`/assets/${assetId}/content`)
      .then(async (res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const text = await res.text();
        if (!cancelled) {
          setContent(text);
          setLoading(false);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err.message || "Failed to load content");
          setLoading(false);
        }
      });
    return () => { cancelled = true; };
  }, [assetId, tooLarge]);

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
          {tooLarge ? (
            <div className="flex flex-col items-center justify-center gap-3 py-16 text-center">
              <FileWarning className="h-10 w-10 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">
                Too large to preview ({formatBytes(sizeBytes!)})
              </p>
              <Button asChild size="sm">
                <a href={`/api/v1/portal/assets/${assetId}/content`} download={assetName}>
                  <Download />
                  Download
                </a>
              </Button>
            </div>
          ) : loading ? (
            <div className="flex items-center justify-center py-20 text-muted-foreground text-sm">
              Loading...
            </div>
          ) : error ? (
            <div className="flex items-center justify-center py-20 text-destructive text-sm">
              {error}
            </div>
          ) : content !== null ? (
            <div className="p-4">
              <ContentRenderer contentType={contentType} content={content} />
            </div>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  );
}
