import {
  ArrowUpCircle,
  Check,
  Copy,
  FileBox,
  MessageSquare,
  Pencil,
  Save,
  Share2,
  Trash2,
  X,
} from "lucide-react";
import type { Prompt } from "@/api/admin/types";
import { FeedbackButton } from "@/components/feedback/FeedbackButton";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { PromptStatusBadge } from "../PromptStatusBadge";
import { ScopeBadge } from "./ScopeBadge";

interface ViewActionProps {
  prompt: Prompt;
  isOwner: boolean;
  copied: boolean;
  createAssetPending: boolean;
  onCopyContent: () => void;
  onSaveAsAsset: () => void;
  onShare: () => void;
  onRequestPromotion: () => void;
  onEdit: () => void;
  onDeleteRequest: () => void;
}

// PromptViewerHeader is the title row and action bar for the prompt viewer:
// back link, title, scope/status badges, and the mode-dependent action
// buttons (copy/feedback/save-as-asset/share/promote/edit/delete when viewing;
// save/cancel when editing). Purely presentational — all actions route through
// handlers from PromptViewerPage. Extracted from PromptViewerPage.tsx (#819).
export function PromptViewerHeader({
  prompt,
  editing,
  updatePending,
  saveDisabled,
  onBack,
  onSave,
  onCancel,
  ...view
}: ViewActionProps & {
  editing: boolean;
  updatePending: boolean;
  saveDisabled: boolean;
  onBack: () => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  return (
    <PageHeader
      onBack={onBack}
      icon={MessageSquare}
      title={
        <>
          <span className="truncate" title={prompt.display_name || prompt.name}>
            {prompt.display_name || prompt.name}
          </span>
          <ScopeBadge scope={prompt.scope} />
          <PromptStatusBadge status={prompt.status} />
          {prompt.review_requested && (
            <Badge variant="warning">
              <ArrowUpCircle /> Promotion requested
            </Badge>
          )}
        </>
      }
      actions={
        editing ? (
          <EditActions
            updatePending={updatePending}
            saveDisabled={saveDisabled}
            onSave={onSave}
            onCancel={onCancel}
          />
        ) : (
          <ViewActions prompt={prompt} {...view} />
        )
      }
    />
  );
}

function EditActions({
  updatePending,
  saveDisabled,
  onSave,
  onCancel,
}: {
  updatePending: boolean;
  saveDisabled: boolean;
  onSave: () => void;
  onCancel: () => void;
}) {
  return (
    <>
      <Button size="sm" onClick={onSave} disabled={saveDisabled}>
        <Save /> {updatePending ? "Saving..." : "Save"}
      </Button>
      <Button variant="outline" size="sm" onClick={onCancel}>
        <X /> Cancel
      </Button>
    </>
  );
}

function ViewActions({
  prompt,
  isOwner,
  copied,
  createAssetPending,
  onCopyContent,
  onSaveAsAsset,
  onShare,
  onRequestPromotion,
  onEdit,
  onDeleteRequest,
}: ViewActionProps) {
  return (
    <>
      <Button variant="outline" size="sm" onClick={onCopyContent} title="Copy prompt content">
        {copied ? <Check className="text-emerald-500" /> : <Copy />}
        {copied ? "Copied" : "Copy"}
      </Button>
      <FeedbackButton target={{ type: "prompt", id: prompt.id }} canModerate={isOwner} />
      <Button
        variant="outline"
        size="sm"
        onClick={onSaveAsAsset}
        disabled={createAssetPending}
        title="Snapshot this prompt as a markdown asset in My Assets"
      >
        <FileBox />
        {createAssetPending ? "Saving..." : "Save as Asset"}
      </Button>
      {isOwner && (
        <Button size="sm" onClick={onShare} title="Share this prompt with another user">
          <Share2 /> Share
        </Button>
      )}
      {isOwner && !prompt.review_requested && (
        <Button
          variant="outline"
          size="sm"
          onClick={onRequestPromotion}
          title="Request an admin promote this prompt to a shared scope"
          className="border-amber-500/30 text-amber-600 hover:bg-amber-500/10 dark:text-amber-300"
        >
          <ArrowUpCircle /> Request Promotion
        </Button>
      )}
      {isOwner && (
        <>
          <Button variant="outline" size="sm" onClick={onEdit}>
            <Pencil /> Edit
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={onDeleteRequest}
            className="border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive"
          >
            <Trash2 /> Delete
          </Button>
        </>
      )}
    </>
  );
}
