import { useState, useEffect } from "react";
import { X } from "lucide-react";
import { apiFetchRaw } from "@/api/portal/client";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";

interface Props {
  assetId: string;
  assetName: string;
  contentType: string;
  onClose: () => void;
}

/**
 * Modal overlay that fetches and renders an asset's content for quick preview.
 * Press Escape or click the backdrop to close.
 */
export function AssetPreviewModal({ assetId, assetName, contentType, onClose }: Props) {
  const [content, setContent] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
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
  }, [assetId]);

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
          {loading ? (
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
