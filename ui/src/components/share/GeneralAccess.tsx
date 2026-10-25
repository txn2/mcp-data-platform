import { useState } from "react";
import { Building2, Check, ChevronDown, Globe, Lock, type LucideIcon } from "lucide-react";
import type { Share } from "@/api/portal/types";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { GUEST_TINT, LIFETIMES, longDate, MAX_BANNER, MODES, opened, type GeneralMode } from "./model";

const ICONS: Record<GeneralMode, LucideIcon> = { none: Lock, authenticated: Building2, public: Globe };
const MUTED_TINT = "bg-muted text-foreground";

/** ModeMark is a mode's icon in its circle; the open-to-anyone mode is tinted as a warning. */
export function ModeMark({ mode }: { mode: GeneralMode }) {
  const Icon = ICONS[mode];
  return (
    <Avatar tint={mode === "public" ? GUEST_TINT : MUTED_TINT}>
      <Icon />
    </Avatar>
  );
}

/** A change General access asks for: a mode, a lifetime, a banner. */
export interface LinkChange {
  mode: GeneralMode;
  expiresIn?: string;
  banner?: string;
  showExpiry?: boolean;
}

/**
 * GeneralAccess is who can open the item without being added, chosen from a
 * menu and applied at once (#2291), with the link's lifetime and banner under
 * it while there is a link. Older links an item collected before it had one
 * are listed under it until they are removed.
 */
export function GeneralAccess({
  mode,
  link,
  otherLinks,
  pending,
  onChange,
  onRemoveOther,
}: {
  mode: GeneralMode;
  link: Share | undefined;
  otherLinks: Share[];
  pending: boolean;
  onChange: (c: LinkChange) => void;
  onRemoveOther: (s: Share) => void;
}) {
  return (
    <section aria-labelledby="share-general-label">
      <h3 id="share-general-label" className="mb-1.5 text-[13px] font-semibold text-foreground/80">
        General access
      </h3>
      <div className="flex items-center gap-3">
        <ModeMark mode={mode} />
        <div className="min-w-0 flex-1">
          <ModeMenu mode={mode} pending={pending} onChoose={(m) => onChange({ mode: m })} />
          <p className="text-xs text-muted-foreground" data-testid="general-sentence">
            {MODES[mode].sentence}
          </p>
        </div>
      </div>
      {link && mode !== "none" && <LinkSettings mode={mode} link={link} onChange={onChange} />}
      {otherLinks.map((s) => (
        <div key={s.id} className="mt-2 ml-12 flex items-center gap-2 text-xs text-muted-foreground" data-testid="other-link">
          <span className="flex-1">
            {["Other link", MODES[s.access_mode === "public" ? "public" : "authenticated"].label, opened(s.access_count)]
              .filter(Boolean)
              .join(" · ")}
          </span>
          <Button variant="ghost" size="xs" onClick={() => onRemoveOther(s)}>
            Remove
          </Button>
        </div>
      ))}
    </section>
  );
}

function ModeMenu({ mode, pending, onChoose }: { mode: GeneralMode; pending: boolean; onChoose: (m: GeneralMode) => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          disabled={pending}
          className="-ml-1 inline-flex items-center gap-1 rounded-md px-1 text-sm font-medium hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none disabled:opacity-60"
          aria-label={`General access: ${MODES[mode].label}`}
        >
          {MODES[mode].label}
          <ChevronDown className="size-4" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-80 rounded-xl p-1.5">
        {(Object.keys(MODES) as GeneralMode[]).map((m) => (
          <DropdownMenuItem key={m} className="items-start gap-3 rounded-lg py-2" onSelect={() => m !== mode && onChoose(m)}>
            <ModeMark mode={m} />
            <span className="flex-1">
              <span className="block text-sm font-medium">{MODES[m].label}</span>
              <span className="block text-xs text-muted-foreground">{MODES[m].option}</span>
            </span>
            {m === mode && <Check className="mt-1 size-4" aria-label="Current" />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** LinkSettings is the link's lifetime, for a link anyone can open, and its banner. */
function LinkSettings({ mode, link, onChange }: { mode: GeneralMode; link: Share; onChange: (c: LinkChange) => void }) {
  return (
    <div className="mt-3 ml-12 divide-y rounded-xl border" data-testid="link-settings">
      {mode === "public" && <Lifetime link={link} onChange={(expiresIn) => onChange({ mode, expiresIn })} />}
      <Banner mode={mode} link={link} onSave={(banner, showExpiry) => onChange({ mode, banner, showExpiry })} />
    </div>
  );
}

function Lifetime({ link, onChange }: { link: Share; onChange: (expiresIn: string) => void }) {
  const [changing, setChanging] = useState(!link.expires_at);
  return (
    <div className="flex items-center gap-2 px-3.5 py-3 text-sm">
      <span className="flex-1">
        Link stops working
        {!changing && link.expires_at && <span className="ml-1 font-medium">{longDate(link.expires_at)}</span>}
      </span>
      {changing ? (
        <Select
          onValueChange={(v) => {
            setChanging(false);
            onChange(v);
          }}
        >
          <SelectTrigger size="sm" aria-label="Link stops working">
            <SelectValue placeholder="Choose when" />
          </SelectTrigger>
          <SelectContent>
            {LIFETIMES.map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {o.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Button variant="ghost" size="xs" className="text-primary" onClick={() => setChanging(true)}>
          Change
        </Button>
      )}
    </div>
  );
}

function Banner({ mode, link, onSave }: { mode: GeneralMode; link: Share; onSave: (text: string, showExpiry: boolean) => void }) {
  const current = link.notice_text ?? "";
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(current);
  const [showExpiry, setShowExpiry] = useState(!link.hide_expiration);
  if (!editing) {
    return (
      <div className="flex items-center gap-2 px-3.5 py-3">
        <div className="min-w-0 flex-1">
          <div className="text-sm">Banner on the shared page</div>
          <div className="truncate text-xs text-muted-foreground" data-testid="banner-text">
            {current ? `“${current}”` : "None"}
          </div>
        </div>
        <Button
          variant="ghost"
          size="xs"
          className="text-primary"
          onClick={() => {
            setText(current);
            setEditing(true);
          }}
        >
          Edit
        </Button>
      </div>
    );
  }
  return (
    <div className="space-y-2 px-3.5 py-3">
      <Label htmlFor="share-banner" className="text-sm font-normal">
        Banner on the shared page
      </Label>
      <Input id="share-banner" value={text} maxLength={MAX_BANNER} onChange={(e) => setText(e.target.value)} className="h-11 rounded-[10px]" />
      {mode === "public" && (
        <Label className="text-sm font-normal">
          <input type="checkbox" checked={showExpiry} onChange={(e) => setShowExpiry(e.target.checked)} className="rounded border-input" />
          Also show the date the link stops working
        </Label>
      )}
      <div className="flex justify-end gap-2">
        <Button variant="outline" size="sm" onClick={() => setEditing(false)}>
          Cancel
        </Button>
        <Button
          size="sm"
          onClick={() => {
            setEditing(false);
            onSave(text.trim(), showExpiry);
          }}
        >
          Save
        </Button>
      </div>
    </div>
  );
}
