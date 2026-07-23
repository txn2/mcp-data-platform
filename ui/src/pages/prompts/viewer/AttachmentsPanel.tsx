import { useMemo, useState } from "react";
import { AlertTriangle, ArrowDown, ArrowUp, EyeOff, Paperclip, Plus, X } from "lucide-react";
import type { Prompt } from "@/api/admin/types";
import {
  usePromptAttachments,
  useAttachResource,
  useDetachResource,
  useReorderAttachments,
  type PromptAttachment,
} from "@/api/portal/hooks/attachments";
import { useResources } from "@/api/resources/hooks";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";

// AttachmentsPanel manages the reference material a prompt carries (#1013):
// the template it fills, the checklist it follows, the brand asset it embeds.
//
// Read-only for callers who cannot edit the prompt, because an attachment is
// part of the prompt's substance; the server applies the same rule.
export function AttachmentsPanel({ prompt, canEdit }: { prompt: Prompt; canEdit: boolean }) {
  const { data, isLoading, isError } = usePromptAttachments(prompt.id);
  const [picking, setPicking] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const attachments = useMemo(() => data?.data ?? [], [data]);

  // A caller who cannot edit and has nothing attached has nothing to see; a
  // 403 or 404 from the list route hides the section the same way.
  if (isError || (!isLoading && attachments.length === 0 && !canEdit)) {
    return null;
  }

  const order = attachments.map((a) => a.resource_id);

  return (
    <div className="rounded-lg border bg-card" data-testid="prompt-attachments">
      <PanelHeader
        count={attachments.length}
        isLoading={isLoading}
        canEdit={canEdit}
        onTogglePick={() => { setPicking((p) => !p); setError(null); }}
      />

      {canEdit && attachments.length === 0 && !isLoading && <EmptyHint />}

      {error && (
        <p className="mx-4 mt-3 rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">
          {error}
        </p>
      )}

      <ul className="divide-y">
        {attachments.map((a, i) => (
          <AttachmentRow
            key={a.resource_id}
            attachment={a}
            promptId={prompt.id}
            canEdit={canEdit}
            isFirst={i === 0}
            isLast={i === attachments.length - 1}
            order={order}
            onError={setError}
          />
        ))}
      </ul>

      {picking && (
        <AttachmentPicker
          promptId={prompt.id}
          attached={order}
          onClose={() => setPicking(false)}
          onError={setError}
        />
      )}
    </div>
  );
}

// EmptyHint tells a first-time author what attachments are for.
function EmptyHint() {
  return (
    <p className="px-4 py-3 text-xs text-muted-foreground">
      Attach a template, checklist, or reference file and every agent that runs this prompt
      receives it as authoritative material.
    </p>
  );
}

interface HeaderProps {
  count: number;
  isLoading: boolean;
  canEdit: boolean;
  onTogglePick: () => void;
}

// PanelHeader is split out to keep the panel's own branching under the
// complexity gate.
function PanelHeader({ count, isLoading, canEdit, onTogglePick }: HeaderProps) {
  return (
    <div className="flex items-center gap-2 border-b px-4 py-2.5 text-sm font-semibold">
      <Paperclip className="h-4 w-4 text-muted-foreground" />
      Attached materials
      {count > 0 && <span className="text-xs font-normal text-muted-foreground">{count}</span>}
      {isLoading && <span className="text-xs font-normal text-muted-foreground">Loading...</span>}
      {canEdit && (
        <button
          type="button"
          onClick={onTogglePick}
          className="ml-auto inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs hover:bg-accent"
        >
          <Plus className="h-3 w-3" />
          Attach
        </button>
      )}
    </div>
  );
}

interface RowProps {
  attachment: PromptAttachment;
  promptId: string;
  canEdit: boolean;
  isFirst: boolean;
  isLast: boolean;
  order: string[];
  onError: (msg: string | null) => void;
}

// AttachmentRow renders one attached material, or the flag that stands in for
// it when the link is broken or out of the caller's reach.
function AttachmentRow({ attachment, promptId, canEdit, isFirst, isLast, order, onError }: RowProps) {
  const detach = useDetachResource(promptId);
  const reorder = useReorderAttachments(promptId);

  function move(delta: number) {
    const from = order.indexOf(attachment.resource_id);
    const to = from + delta;
    if (from < 0 || to < 0 || to >= order.length) return;
    const next = [...order];
    const moved = next[from];
    const displaced = next[to];
    if (moved === undefined || displaced === undefined) return;
    next[from] = displaced;
    next[to] = moved;
    onError(null);
    reorder.mutate(next, {
      onError: (err) => onError(err instanceof Error ? err.message : "Reorder failed"),
    });
  }

  return (
    <li className="flex items-start gap-3 px-4 py-3">
      <div className="min-w-0 flex-1">
        {attachment.broken ? (
          <BrokenLabel resourceId={attachment.resource_id} />
        ) : attachment.unreadable ? (
          <UnreadableLabel />
        ) : (
          <ReadableLabel attachment={attachment} />
        )}
      </div>

      {canEdit && (
        <div className="flex shrink-0 items-center gap-1">
          <button
            type="button"
            aria-label="Move up"
            disabled={isFirst || reorder.isPending}
            onClick={() => move(-1)}
            className="rounded p-1 text-muted-foreground hover:bg-accent disabled:opacity-30"
          >
            <ArrowUp className="h-3.5 w-3.5" />
          </button>
          <button
            type="button"
            aria-label="Move down"
            disabled={isLast || reorder.isPending}
            onClick={() => move(1)}
            className="rounded p-1 text-muted-foreground hover:bg-accent disabled:opacity-30"
          >
            <ArrowDown className="h-3.5 w-3.5" />
          </button>
          <button
            type="button"
            aria-label={`Detach ${attachment.display_name ?? attachment.resource_id}`}
            disabled={detach.isPending}
            onClick={() => {
              onError(null);
              detach.mutate(attachment.resource_id, {
                onError: (err) => onError(err instanceof Error ? err.message : "Detach failed"),
              });
            }}
            className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-red-400 disabled:opacity-30"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      )}
    </li>
  );
}

// BrokenLabel marks a link whose resource was deleted. Naming the id is the
// point: it is what the author needs in order to clean up.
function BrokenLabel({ resourceId }: { resourceId: string }) {
  return (
    <div className="flex items-start gap-2" data-testid="attachment-broken">
      <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-400" />
      <div className="min-w-0">
        <p className="text-sm text-amber-400">Missing resource</p>
        <p className="truncate text-xs text-muted-foreground">
          {resourceId} was deleted. Agents running this prompt are told the material is
          unavailable. Detach it or upload a replacement.
        </p>
      </div>
    </div>
  );
}

// UnreadableLabel stands in for an attachment outside the caller's scope. It
// deliberately shows nothing about the resource, matching what the server sends.
function UnreadableLabel() {
  return (
    <div className="flex items-start gap-2" data-testid="attachment-unreadable">
      <EyeOff className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <div>
        <p className="text-sm text-muted-foreground">Restricted material</p>
        <p className="text-xs text-muted-foreground">
          This prompt attaches a resource outside your scope.
        </p>
      </div>
    </div>
  );
}

function ReadableLabel({ attachment }: { attachment: PromptAttachment }) {
  return (
    <>
      <p className="truncate text-sm font-medium">{attachment.display_name}</p>
      {attachment.description && (
        <p className="truncate text-xs text-muted-foreground">{attachment.description}</p>
      )}
      <p className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <ScopeChip scope={attachment.scope} scopeId={attachment.scope_id} />
        {attachment.mime_type && <span>{attachment.mime_type}</span>}
        {attachment.size_bytes ? <span>{formatBytes(attachment.size_bytes)}</span> : null}
        {attachment.attached_by && <span>added by {attachment.attached_by}</span>}
      </p>
    </>
  );
}

// ScopeChip shows how widely a material is visible, which is the rule that
// decides whether the prompt can be promoted while carrying it.
function ScopeChip({ scope, scopeId }: { scope?: string; scopeId?: string }) {
  if (!scope) return null;
  const label = scope === "persona" && scopeId ? `persona: ${scopeId}` : scope;
  return (
    <span
      className={cn(
        "rounded px-1.5 py-0.5",
        scope === "global" && "bg-emerald-500/10 text-emerald-400",
        scope === "persona" && "bg-sky-500/10 text-sky-400",
        scope === "user" && "bg-amber-500/10 text-amber-400",
      )}
    >
      {label}
    </span>
  );
}

interface PickerProps {
  promptId: string;
  attached: string[];
  onClose: () => void;
  onError: (msg: string | null) => void;
}

// AttachmentPicker searches the caller's visible resources and attaches one.
// It does not pre-filter by scope: the server owns that rule, and showing a
// resource with its scope chip plus the server's explanation teaches the rule
// better than silently hiding candidates.
function AttachmentPicker({ promptId, attached, onClose, onError }: PickerProps) {
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState("");
  const { data, isLoading } = useResources({
    q: query.trim() || undefined,
    category: category || undefined,
  });
  const attach = useAttachResource(promptId);

  const candidates = useMemo(
    () => (data?.resources ?? []).filter((r) => !attached.includes(r.id)),
    [data, attached],
  );
  const categories = useMemo(
    () => [...new Set((data?.resources ?? []).map((r) => r.category).filter(Boolean))].sort(),
    [data],
  );

  return (
    <div className="border-t bg-muted/30 p-4" data-testid="attachment-picker">
      <div className="flex flex-wrap items-center gap-2">
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search resources"
          aria-label="Search resources"
          className="min-w-0 flex-1 rounded-md border bg-background px-2 py-1 text-xs outline-none"
        />
        <select
          value={category}
          onChange={(e) => setCategory(e.target.value)}
          aria-label="Filter by category"
          className="rounded-md border bg-background px-2 py-1 text-xs outline-none"
        >
          <option value="">All categories</option>
          {categories.map((c) => (
            <option key={c} value={c}>{c}</option>
          ))}
        </select>
        <button
          type="button"
          onClick={onClose}
          className="rounded-md border px-2 py-1 text-xs hover:bg-accent"
        >
          Done
        </button>
      </div>

      {isLoading && <p className="mt-3 text-xs text-muted-foreground">Loading resources...</p>}

      {!isLoading && candidates.length === 0 && (
        <p className="mt-3 text-xs text-muted-foreground">
          No resources match. Upload reference files on the Resources page first.
        </p>
      )}

      <ul className="mt-2 max-h-64 space-y-1 overflow-y-auto">
        {candidates.map((r) => (
          <li key={r.id}>
            <button
              type="button"
              disabled={attach.isPending}
              onClick={() => {
                onError(null);
                attach.mutate(r.id, {
                  onError: (err) => onError(err instanceof Error ? err.message : "Attach failed"),
                });
              }}
              className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-accent disabled:opacity-50"
            >
              <span className="min-w-0 flex-1 truncate text-xs">
                {r.display_name || r.filename}
              </span>
              <ScopeChip scope={r.scope} scopeId={r.scope_id} />
              <span className="shrink-0 text-xs text-muted-foreground">{r.mime_type}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
