import { X } from "lucide-react";

// PromptNotices renders the inline error banner and the "saved as asset"
// success banner shown above the prompt body. Extracted verbatim from
// PromptViewerPage.tsx (#819).
export function PromptNotices({
  error,
  saveAsAssetNotice,
  onOpenAsset,
  onDismissNotice,
}: {
  error: string | null;
  saveAsAssetNotice: { assetId: string; name: string } | null;
  onOpenAsset: (assetId: string) => void;
  onDismissNotice: () => void;
}) {
  return (
    <>
      {error && (
        <div className="rounded-md bg-red-500/10 border border-red-500/20 px-3 py-2 text-xs text-red-400">{error}</div>
      )}
      {saveAsAssetNotice && (
        <div className="flex items-center justify-between rounded-md bg-emerald-500/10 border border-emerald-500/20 px-3 py-2 text-xs text-emerald-400">
          <span>Saved as asset “{saveAsAssetNotice.name}”.</span>
          <div className="flex items-center gap-3">
            <button
              onClick={() => onOpenAsset(saveAsAssetNotice.assetId)}
              className="underline hover:text-emerald-300"
            >
              Open asset
            </button>
            <button
              onClick={onDismissNotice}
              className="text-emerald-400/70 hover:text-emerald-300"
              aria-label="Dismiss"
            >
              <X className="h-3 w-3" />
            </button>
          </div>
        </div>
      )}
    </>
  );
}
