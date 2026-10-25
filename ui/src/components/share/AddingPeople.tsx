import { Info, X } from "lucide-react";
import type { SharePersona } from "@/api/portal/hooks";
import type { SharePermission } from "@/api/portal/types";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { GUEST_TINT, guestNote, MAX_MESSAGE } from "./model";
import { OptionMark, RecipientSearch, type HasAccess, type Recipient } from "./RecipientSearch";

/** What the Adding view holds while recipients are being chosen. */
export interface Draft {
  chips: Recipient[];
  permission: SharePermission;
  notify: boolean;
  message: string;
  /** The server's refusal of a chip, by its key. */
  refused: Record<string, string>;
}

export const EMPTY_DRAFT: Draft = { chips: [], permission: "viewer", notify: true, message: "", refused: {} };

/**
 * AddingPeople is the body while people and groups are being added (#2291):
 * a chip for each, what they all can do, whether they are emailed, and the
 * note the email carries. A guest can only view, so a draft holding one
 * offers only Can view.
 */
export function AddingPeople({
  draft,
  onDraft,
  personas,
  has,
  isFile,
  onOpenChange,
}: {
  draft: Draft;
  onDraft: (d: Draft) => void;
  personas: SharePersona[];
  has: HasAccess;
  isFile: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const guests = draft.chips.filter((c) => c.kind === "guest").map((c) => c.name);
  const picked = new Set(draft.chips.map((c) => c.key));
  const refusals = Object.entries(draft.refused);
  return (
    <div className="space-y-[22px]">
      <div>
        <div className="flex items-start gap-2">
          <div className="flex min-h-11 flex-1 flex-wrap items-center gap-1.5 rounded-[10px] border-2 border-primary bg-background/60 p-1.5">
            {draft.chips.map((c) => (
              <Chip key={c.key} r={c} onRemove={() => onDraft(remove(draft, c.key))} />
            ))}
            <div className="min-w-40 flex-1 [&_input]:h-8 [&_input]:border-0 [&_input]:bg-transparent [&_input]:focus:border-0 [&>div>svg]:hidden [&_input]:pl-1">
              <RecipientSearch
                personas={personas}
                has={has}
                picked={picked}
                placeholder="Add more"
                onOpenChange={onOpenChange}
                onPick={(r) => onDraft({ ...draft, chips: [...draft.chips, r], permission: r.kind === "guest" ? "viewer" : draft.permission })}
              />
            </div>
          </div>
          <Select value={draft.permission} onValueChange={(v) => onDraft({ ...draft, permission: v as SharePermission })}>
            <SelectTrigger className="h-11 w-32 rounded-[10px]" aria-label="What they can do">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="viewer">Can view</SelectItem>
              <SelectItem value="editor" disabled={guests.length > 0}>
                Can edit{guests.length > 0 && <span className="ml-1 text-xs text-muted-foreground">Guests can only view.</span>}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        {refusals.map(([key, msg]) => (
          <p key={key} className="mt-1.5 text-xs text-destructive" data-testid="share-refusal">
            {draft.chips.find((c) => c.key === key)?.name}: {msg}
          </p>
        ))}
      </div>

      {guests.length > 0 && (
        <div className="flex gap-2.5 rounded-[10px] bg-primary/10 px-3.5 py-3 text-[13px]" data-testid="guest-note">
          <Info className="mt-0.5 size-[18px] shrink-0 text-primary" />
          <span>{guestNote(guests, isFile)}</span>
        </div>
      )}

      <div className="space-y-2">
        <Label className="text-sm font-normal">
          <input type="checkbox" checked={draft.notify} onChange={(e) => onDraft({ ...draft, notify: e.target.checked })} className="rounded border-input" />
          Send an email
        </Label>
        {draft.notify && (
          <div>
            <Label className="sr-only" htmlFor="share-note">
              Message
            </Label>
            <Textarea
              id="share-note"
              rows={4}
              maxLength={MAX_MESSAGE}
              placeholder="Add a message (optional)"
              value={draft.message}
              onChange={(e) => onDraft({ ...draft, message: e.target.value })}
              className="field-sizing-fixed min-h-0 resize-y rounded-[10px] text-sm"
            />
            <p className="mt-1 text-right text-xs text-muted-foreground">{MAX_MESSAGE - draft.message.length} characters left</p>
          </div>
        )}
      </div>
    </div>
  );
}

function remove(d: Draft, key: string): Draft {
  const refused = { ...d.refused };
  delete refused[key];
  return { ...d, chips: d.chips.filter((c) => c.key !== key), refused };
}

function Chip({ r, onRemove }: { r: Recipient; onRemove: () => void }) {
  return (
    <span
      data-testid="share-chip"
      className={cn("inline-flex h-[30px] items-center gap-1.5 rounded-full pr-0.5 pl-1 text-sm", r.kind === "guest" ? GUEST_TINT : "bg-muted")}
    >
      <OptionMark r={r} size={22} />
      <span className="max-w-48 truncate">{r.name}</span>
      <button type="button" aria-label={`Remove ${r.name}`} onClick={onRemove} className="grid size-6 place-items-center rounded-full hover:bg-foreground/10">
        <X className="size-3.5" />
      </button>
    </span>
  );
}
