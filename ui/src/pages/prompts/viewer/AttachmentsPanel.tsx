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
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { formatBytes } from "@/lib/format";
import { markdownToPlainText } from "@/lib/markdownText";
import { FormError, ListSkeleton } from "../primitives";

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

  return (
    <div data-testid="prompt-attachments">
      <SectionCard
        title={<PanelTitle count={attachments.length} />}
        action={
          canEdit && (
            <Button
              variant="outline"
              size="xs"
              onClick={() => { setPicking((p) => !p); setError(null); }}
            >
              <Plus /> Attach
            </Button>
          )
        }
      >
        <PanelBody
          promptId={prompt.id}
          attachments={attachments}
          isLoading={isLoading}
          canEdit={canEdit}
          picking={picking}
          error={error}
          onError={setError}
          onClosePicker={() => setPicking(false)}
        />
      </SectionCard>
    </div>
  );
}

function PanelTitle({ count }: { count: number }) {
  return (
    <span className="flex items-center gap-2">
      <Paperclip className="size-4 text-muted-foreground" />
      Attached materials
      {count > 0 && <Badge variant="muted" className="text-[11px]">{count}</Badge>}
    </span>
  );
}

// PanelBody is the section's content in each state it can be in: loading, empty
// and editable, listed, and listed with the picker open.
function PanelBody({
  promptId,
  attachments,
  isLoading,
  canEdit,
  picking,
  error,
  onError,
  onClosePicker,
}: {
  promptId: string;
  attachments: PromptAttachment[];
  isLoading: boolean;
  canEdit: boolean;
  picking: boolean;
  error: string | null;
  onError: (msg: string | null) => void;
  onClosePicker: () => void;
}) {
  const order = attachments.map((a) => a.resource_id);
  return (
    <div className="space-y-3">
      {isLoading && <ListSkeleton rows={2} />}

      {canEdit && attachments.length === 0 && !isLoading && (
        <EmptyState icon={Paperclip}>
          Attach a template, checklist, or reference file and every agent that runs this prompt
          receives it as authoritative material.
        </EmptyState>
      )}

      <FormError message={error} />

      {attachments.length > 0 && (
        <ul className="divide-y rounded-lg border">
          {attachments.map((a, i) => (
            <AttachmentRow
              key={a.resource_id}
              attachment={a}
              promptId={promptId}
              canEdit={canEdit}
              isFirst={i === 0}
              isLast={i === attachments.length - 1}
              order={order}
              onError={onError}
            />
          ))}
        </ul>
      )}

      {picking && (
        <AttachmentPicker
          promptId={promptId}
          attached={order}
          onClose={onClosePicker}
          onError={onError}
        />
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
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label="Move up"
            disabled={isFirst || reorder.isPending}
            onClick={() => move(-1)}
            className="text-muted-foreground"
          >
            <ArrowUp />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label="Move down"
            disabled={isLast || reorder.isPending}
            onClick={() => move(1)}
            className="text-muted-foreground"
          >
            <ArrowDown />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Detach ${attachment.display_name ?? attachment.resource_id}`}
            disabled={detach.isPending}
            onClick={() => {
              onError(null);
              detach.mutate(attachment.resource_id, {
                onError: (err) => onError(err instanceof Error ? err.message : "Detach failed"),
              });
            }}
            className="text-muted-foreground hover:text-destructive"
          >
            <X />
          </Button>
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
      <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-amber-500" />
      <div className="min-w-0">
        <p className="text-sm text-amber-600 dark:text-amber-400">Missing resource</p>
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
      <EyeOff className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
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
        <p className="truncate text-xs text-muted-foreground">{markdownToPlainText(attachment.description)}</p>
      )}
      <p className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <ScopeChip scope={attachment.scope} scopeId={attachment.scope_id} />
        {attachment.mime_type && <span>{attachment.mime_type}</span>}
        {attachment.size_bytes ? <span>{formatBytes(attachment.size_bytes)}</span> : null}
        {attachment.attached_by && <span>added by {attachment.attached_by}</span>}
      </p>
    </>
  );
}

const SCOPE_VARIANTS: Record<string, "success" | "info" | "warning"> = {
  global: "success",
  persona: "info",
  user: "warning",
};

// ScopeChip shows how widely a material is visible, which is the rule that
// decides whether the prompt can be promoted while carrying it.
function ScopeChip({ scope, scopeId }: { scope?: string; scopeId?: string }) {
  if (!scope) return null;
  const label = scope === "persona" && scopeId ? `persona: ${scopeId}` : scope;
  return (
    <Badge variant={SCOPE_VARIANTS[scope] ?? "muted"} className="text-[11px]">
      {label}
    </Badge>
  );
}

interface PickerProps {
  promptId: string;
  attached: string[];
  onClose: () => void;
  onError: (msg: string | null) => void;
}

// ALL_FOLDERS is the picker's unfiltered choice; a Select item cannot carry
// the empty value the query parameter uses for it.
const ALL_FOLDERS = "__all__";

// AttachmentPicker searches the caller's visible resources and attaches one.
// It does not pre-filter by scope: the server owns that rule, and showing a
// resource with its scope chip plus the server's explanation teaches the rule
// better than silently hiding candidates.
function AttachmentPicker({ promptId, attached, onClose, onError }: PickerProps) {
  const [query, setQuery] = useState("");
  const [folder, setFolder] = useState("");
  const { data, isLoading } = useResources({
    q: query.trim() || undefined,
    path: folder || undefined,
  });
  const attach = useAttachResource(promptId);

  const candidates = useMemo(
    () => (data?.resources ?? []).filter((r) => !attached.includes(r.id)),
    [data, attached],
  );
  // The folders the candidates are actually in, offered as a narrowing. The
  // server reads the value as a path prefix, so picking one shows that folder
  // and everything beneath it.
  const folders = useMemo(
    () => [...new Set((data?.resources ?? []).map((r) => r.path).filter(Boolean))].sort(),
    [data],
  );

  return (
    <div className="rounded-lg border bg-muted/30 p-3" data-testid="attachment-picker">
      <div className="flex flex-wrap items-center gap-2">
        <Input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search resources"
          aria-label="Search resources"
          className="h-8 min-w-0 flex-1 text-xs md:text-xs"
        />
        <Select
          value={folder || ALL_FOLDERS}
          onValueChange={(v) => setFolder(v === ALL_FOLDERS ? "" : v)}
        >
          <SelectTrigger size="sm" aria-label="Filter by folder" className="text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL_FOLDERS} className="text-xs">All folders</SelectItem>
            {folders.map((f) => (
              <SelectItem key={f} value={f} className="text-xs">{f}</SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button variant="outline" size="sm" onClick={onClose}>
          Done
        </Button>
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
            <Button
              variant="ghost"
              disabled={attach.isPending}
              onClick={() => {
                onError(null);
                attach.mutate(r.id, {
                  onError: (err) => onError(err instanceof Error ? err.message : "Attach failed"),
                });
              }}
              className="h-auto w-full justify-start px-2 py-1.5 font-normal"
            >
              <span className="min-w-0 flex-1 truncate text-left text-xs">
                {r.display_name || r.filename}
              </span>
              <ScopeChip scope={r.scope} scopeId={r.scope_id} />
              <span className="shrink-0 text-xs text-muted-foreground">{r.mime_type}</span>
            </Button>
          </li>
        ))}
      </ul>
    </div>
  );
}
