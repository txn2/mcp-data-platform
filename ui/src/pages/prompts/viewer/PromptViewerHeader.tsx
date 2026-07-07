import {
  ArrowLeft,
  Pencil,
  Save,
  X,
  Trash2,
  Copy,
  Check,
  FileBox,
  Share2,
  ArrowUpCircle,
} from "lucide-react";
import type { Prompt } from "@/api/admin/types";
import { FeedbackButton } from "@/components/feedback/FeedbackButton";
import { PromptStatusBadge } from "../PromptStatusBadge";
import { ScopeBadge } from "./ScopeBadge";

// PromptViewerHeader is the title row and action bar for the prompt viewer:
// back button, title, scope/status badges, and the mode-dependent action
// buttons (copy/feedback/save-as-asset/share/promote/edit/delete when viewing;
// save/cancel when editing). Purely presentational — all actions route through
// handlers from PromptViewerPage. Extracted from PromptViewerPage.tsx (#819).
export function PromptViewerHeader({
  prompt,
  editing,
  isOwner,
  copied,
  createAssetPending,
  updatePending,
  saveDisabled,
  onBack,
  onCopyContent,
  onSaveAsAsset,
  onShare,
  onRequestPromotion,
  onEdit,
  onDeleteRequest,
  onSave,
  onCancel,
}: {
  prompt: Prompt;
  editing: boolean;
  isOwner: boolean;
  copied: boolean;
  createAssetPending: boolean;
  updatePending: boolean;
  saveDisabled: boolean;
  onBack: () => void;
  onCopyContent: () => void;
  onSaveAsAsset: () => void;
  onShare: () => void;
  onRequestPromotion: () => void;
  onEdit: () => void;
  onDeleteRequest: () => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <button
        onClick={onBack}
        className="rounded-md p-1.5 hover:bg-accent"
        aria-label="Back"
      >
        <ArrowLeft className="h-4 w-4" />
      </button>
      <h2 className="text-lg font-semibold truncate max-w-[24rem]" title={prompt.display_name || prompt.name}>{prompt.display_name || prompt.name}</h2>
      <ScopeBadge scope={prompt.scope} />
      <PromptStatusBadge status={prompt.status} />
      {prompt.review_requested && (
        <span className="inline-flex items-center gap-1 rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-xs font-medium text-amber-400 whitespace-nowrap">
          <ArrowUpCircle className="h-3 w-3" /> Promotion requested
        </span>
      )}

      {!editing && (
        <>
          <button
            onClick={onCopyContent}
            className="ml-auto inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent"
            title="Copy prompt content"
          >
            {copied ? <Check className="h-3.5 w-3.5 text-green-500" /> : <Copy className="h-3.5 w-3.5" />}
            {copied ? "Copied" : "Copy"}
          </button>
          <FeedbackButton target={{ type: "prompt", id: prompt.id }} canModerate={isOwner} />
          <button
            onClick={onSaveAsAsset}
            disabled={createAssetPending}
            className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent disabled:opacity-50"
            title="Snapshot this prompt as a markdown asset in My Assets"
          >
            <FileBox className="h-3.5 w-3.5" />
            {createAssetPending ? "Saving..." : "Save as Asset"}
          </button>
          {isOwner && (
            <button
              onClick={onShare}
              className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
              title="Share this prompt with another user"
            >
              <Share2 className="h-3.5 w-3.5" />
              Share
            </button>
          )}
          {isOwner && !prompt.review_requested && (
            <button
              onClick={onRequestPromotion}
              className="inline-flex items-center gap-1.5 rounded-md border border-amber-500/30 px-3 py-1.5 text-sm font-medium text-amber-400 hover:bg-amber-500/10"
              title="Request an admin promote this prompt to a shared scope"
            >
              <ArrowUpCircle className="h-3.5 w-3.5" /> Request Promotion
            </button>
          )}
          {isOwner && (
            <>
              <button
                onClick={onEdit}
                className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent"
              >
                <Pencil className="h-3.5 w-3.5" /> Edit
              </button>
              <button
                onClick={onDeleteRequest}
                className="inline-flex items-center gap-1.5 rounded-md border border-destructive/30 px-3 py-1.5 text-sm font-medium text-destructive hover:bg-destructive/10"
              >
                <Trash2 className="h-3.5 w-3.5" /> Delete
              </button>
            </>
          )}
        </>
      )}

      {editing && (
        <>
          <button
            onClick={onSave}
            disabled={saveDisabled}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            <Save className="h-3.5 w-3.5" /> {updatePending ? "Saving..." : "Save"}
          </button>
          <button
            onClick={onCancel}
            className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent"
          >
            <X className="h-3.5 w-3.5" /> Cancel
          </button>
        </>
      )}
    </div>
  );
}
