import { useState, useEffect } from "react";
import { X, Download, FileWarning } from "lucide-react";
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
 * Press Escape or click the backdrop to close.
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

  useEffect(() => {
    function handleKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div
        className="absolute inset-0 bg-black/60"
        onClick={onClose}
        role="button"
        tabIndex={-1}
        aria-label="Close preview"
      />
      <div className="relative bg-card border rounded-lg shadow-xl w-full max-w-5xl mx-4 max-h-[90vh] flex flex-col">
        {/* Header */}
        <div className="flex items-center gap-3 px-4 py-3 border-b shrink-0">
          <h3 className="text-sm font-semibold truncate flex-1">{assetName}</h3>
          <span className="text-xs text-muted-foreground shrink-0">{contentType}</span>
          <button
            onClick={onClose}
            className="rounded-md p-1 hover:bg-accent text-muted-foreground hover:text-foreground"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-auto">
          {tooLarge ? (
            <div className="flex flex-col items-center justify-center gap-3 py-16 text-center">
              <FileWarning className="h-10 w-10 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">
                Too large to preview ({formatBytes(sizeBytes!)})
              </p>
              <a
                href={`/api/v1/portal/assets/${assetId}/content`}
                download={assetName}
                className="inline-flex items-center gap-2 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90"
              >
                <Download className="h-3.5 w-3.5" />
                Download
              </a>
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
      </div>
    </div>
  );
}
