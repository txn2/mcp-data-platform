import { useState } from "react";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { usePersonas } from "@/api/admin/hooks";
import type { EffectiveAccess } from "@/api/admin/types";
import { resolvePortal, type ResolvedPortal, type PortalUploads } from "@/lib/portalAccess";
import { ChipInput } from "../ChipInput";
import { EditorSection } from "./sections";
import type { PersonaDraft } from "./types";

type Leveled = "assets" | "resources" | "knowledge" | "catalog" | "apis" | "scratch_tables";
type Switch = "feedback" | "prompts" | "automations" | "search" | "memory" | "download";

// The leveled areas, each with the choices its dropdown offers, lowest first,
// and the line naming what governs the AI side of it (#2225, #2257).
const LEVELED: { key: Leveled; label: string; help?: string; options: { value: string; label: string }[] }[] = [
  {
    key: "assets",
    label: "Assets and collections",
    options: [
      { value: "shared", label: "Shared with them" },
      { value: "own", label: "Their own and shared" },
    ],
  },
  {
    key: "resources",
    label: "Resources",
    help: "An AI client's MCP resources are not governed by this.",
    options: [
      { value: "none", label: "None" },
      { value: "shared", label: "Read only: own, persona and Global folders" },
      { value: "own", label: "Own folder, read and write; Global, read" },
      { value: "persona", label: "Own folder, read and write; persona and Global folders, read" },
    ],
  },
  {
    key: "knowledge",
    label: "Knowledge pages",
    help: "The AI assistant edits knowledge through apply_knowledge, a tool.",
    options: [
      { value: "none", label: "None" },
      { value: "read", label: "Read" },
      { value: "write", label: "Edit" },
    ],
  },
  {
    key: "catalog",
    label: "Catalog",
    help: "The AI assistant edits the catalog through apply_knowledge, a tool.",
    options: [
      { value: "none", label: "None" },
      { value: "read", label: "Read" },
      { value: "write", label: "Edit" },
    ],
  },
  {
    key: "apis",
    label: "APIs",
    help: "The AI assistant calls APIs through api_invoke_endpoint, under Tools and API endpoints.",
    options: [
      { value: "none", label: "None" },
      { value: "browse", label: "Browse" },
    ],
  },
  {
    key: "scratch_tables",
    label: "Scratch tables",
    help: "The tables are still on the persona's connections; SQL through trino_execute is a tool.",
    options: [
      { value: "none", label: "None" },
      { value: "list", label: "List" },
      { value: "register", label: "List and register" },
    ],
  },
];

const SWITCHES: { key: Switch; label: string; help: string }[] = [
  { key: "feedback", label: "Feedback", help: "Threads, comments and sign-off" },
  { key: "prompts", label: "Prompts", help: "The prompt library. An AI client's MCP prompts are not governed by this." },
  { key: "automations", label: "Automations", help: "Automations in the portal. The AI assistant runs them through run_script, a tool." },
  { key: "search", label: "Search", help: "The portal's search" },
  { key: "memory", label: "Memory", help: "Memory and insights in the portal. memory_manage is a tool." },
  { key: "download", label: "PDF export", help: "Export an asset or a file as PDF" },
];

// What External leaves an external persona's people choosing between for
// Resources: none, or this persona's folder.
const EXTERNAL_RESOURCES = [
  { value: "none", label: "None" },
  { value: "shared", label: "Read only: this persona's folder" },
];

function optionLabel(key: Leveled, value: string): string {
  return LEVELED.find((a) => a.key === key)?.options.find((o) => o.value === value)?.label ?? value;
}

function switchText(on: boolean): string {
  return on ? "On" : "Off";
}

// PortalAccessSection is what the persona's people may do in the portal,
// one setting per area (#2225), enforced on the portal's pages and API. An
// external persona's limits lock the rows they set, showing the saved choice
// beside what applies (#2257); an administrator has every area.
export function PortalAccessSection({
  draft,
  onUpdate,
  report,
  isReadOnly,
}: {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  report: EffectiveAccess | undefined;
  isReadOnly: boolean;
}) {
  const ctx: RowContext = {
    draft,
    set: (partial) => onUpdate({ portal: { ...draft.portal, ...partial } }),
    effective: report ? resolvePortal(report.portal.effective) : draft.portal,
    capped: new Set(report?.portal.capped_by_external ?? []),
    disabled: isReadOnly || !!report?.is_admin,
  };
  return (
    <EditorSection
      id="portal"
      title="Portal access"
      governs="Enforced on the portal's pages and the portal API. It does not change what an AI client reaches."
    >
      {report?.is_admin && <p className="mb-4 text-xs text-muted-foreground">Administrators have every area, whatever is set here.</p>}
      <fieldset disabled={ctx.disabled} className="max-w-3xl space-y-4">
        {LEVELED.map((area) => (
          <LeveledRow key={area.key} area={area} ctx={ctx} />
        ))}
        <div className="space-y-3 pt-2">
          {SWITCHES.map((sw) => (
            <SwitchRow key={sw.key} sw={sw} ctx={ctx} />
          ))}
          <SharingRow draft={draft} onUpdate={onUpdate} disabled={ctx.disabled} />
        </div>
        {draft.external && (
          <UploadsSetting value={draft.portal.uploads} onChange={(uploads) => ctx.set({ uploads })} disabled={ctx.disabled} />
        )}
      </fieldset>
    </EditorSection>
  );
}

interface RowContext {
  draft: PersonaDraft;
  set: (partial: Partial<ResolvedPortal>) => void;
  effective: ResolvedPortal;
  capped: Set<string>;
  disabled: boolean;
}

/** cappedNote is the line under a row External set, or undefined. */
function cappedNote(ctx: RowContext, key: string, saved: string, applied: string): string | undefined {
  return ctx.capped.has(key) ? `Set by External: ${applied}. Saved choice: ${saved}.` : undefined;
}

/** leveledValue is what a leveled row shows: what applies when External set it. */
function leveledValue(area: Leveled, ctx: RowContext, locked: boolean, externalResources: boolean): string {
  if (locked) return ctx.effective[area];
  // An external persona chooses between none and its folder; the saved level
  // reads as the second of those.
  if (externalResources) return ctx.draft.portal.resources === "none" ? "none" : "shared";
  return ctx.draft.portal[area];
}

function LeveledRow({ area, ctx }: { area: (typeof LEVELED)[number]; ctx: RowContext }) {
  const externalResources = ctx.draft.external && area.key === "resources";
  const locked = ctx.capped.has(area.key) && !externalResources;
  const options = externalResources ? EXTERNAL_RESOURCES : area.options;
  const note = cappedNote(ctx, area.key, optionLabel(area.key, ctx.draft.portal[area.key]), optionLabel(area.key, ctx.effective[area.key]));
  return (
    <div className="grid gap-1 sm:grid-cols-[12rem_minmax(0,1fr)] sm:items-start">
      <label htmlFor={`persona-portal-${area.key}`} className="pt-1.5 text-sm font-medium">
        {area.label}
      </label>
      <div>
        <Select
          value={leveledValue(area.key, ctx, locked, externalResources)}
          onValueChange={(v) => ctx.set({ [area.key]: v } as Partial<ResolvedPortal>)}
          disabled={ctx.disabled || locked}
        >
          <SelectTrigger id={`persona-portal-${area.key}`} size="sm" className="h-8 w-full max-w-md" aria-label={area.label}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(locked ? area.options : options).map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {o.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <RowNotes area={area} note={note ?? (externalResources ? "An external persona never reads Global." : undefined)} name={ctx.draft.name} />
      </div>
    </div>
  );
}

function RowNotes({ area, note, name }: { area: (typeof LEVELED)[number]; note: string | undefined; name: string }) {
  return (
    <>
      {note && <p className="mt-1 text-xs text-muted-foreground">{note}</p>}
      {area.help && <p className="mt-1 text-xs text-muted-foreground">{area.help}</p>}
      {area.key === "resources" && (
        <p className="mt-1 text-xs text-muted-foreground">
          Writers of this persona's folder: role <span className="font-mono">persona-admin:{name || "<name>"}</span> and
          administrators.
        </p>
      )}
    </>
  );
}

function SwitchRow({ sw, ctx }: { sw: (typeof SWITCHES)[number]; ctx: RowContext }) {
  const locked = ctx.capped.has(sw.key);
  const note = cappedNote(ctx, sw.key, switchText(ctx.draft.portal[sw.key]), switchText(ctx.effective[sw.key]));
  return (
    <label className="flex items-start gap-2 text-sm">
      <input
        type="checkbox"
        className="mt-0.5"
        checked={locked ? ctx.effective[sw.key] : ctx.draft.portal[sw.key]}
        disabled={ctx.disabled || locked}
        onChange={(e) => ctx.set({ [sw.key]: e.target.checked } as Partial<ResolvedPortal>)}
      />
      <span>
        <span className="font-medium">{sw.label}</span>
        <span className="block text-xs text-muted-foreground">{note ?? sw.help}</span>
      </span>
    </label>
  );
}

function SharingRow({
  draft,
  onUpdate,
  disabled,
}: {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  disabled: boolean;
}) {
  return (
    <label className="flex items-start gap-2 text-sm">
      <input
        type="checkbox"
        className="mt-0.5"
        checked={draft.external ? false : draft.allowSharing}
        disabled={disabled || draft.external}
        onChange={(e) => onUpdate({ allowSharing: e.target.checked })}
      />
      <span>
        <span className="font-medium">Sharing</span>
        <span className="block text-xs text-muted-foreground">
          {draft.external
            ? `Set by External: Off. Saved choice: ${switchText(draft.allowSharing)}.`
            : "Share what they own with a person, a persona or a channel"}
        </span>
      </span>
    </label>
  );
}

// UploadsSetting is an external persona's uploads (#2258): none, or files its
// people upload, each shared on arrival with the personas and people named.
function UploadsSetting({
  value,
  onChange,
  disabled,
}: {
  value: PortalUploads | null;
  onChange: (v: PortalUploads | null) => void;
  disabled: boolean;
}) {
  const { data } = usePersonas();
  const [emailDraft, setEmailDraft] = useState("");
  const personas = (data?.personas ?? []).filter((p) => !p.external);
  const on = value !== null;
  const shareP = value?.share_personas ?? [];
  const shareE = value?.share_emails ?? [];
  return (
    <fieldset disabled={disabled} className="space-y-2 border-t pt-4">
      <legend className="text-sm font-medium">Uploads</legend>
      <label className="flex items-center gap-2 text-sm">
        <input type="radio" name="persona-uploads" checked={!on} onChange={() => onChange(null)} />
        None
      </label>
      <label className="flex items-center gap-2 text-sm">
        <input type="radio" name="persona-uploads" checked={on} onChange={() => onChange({ share_personas: [], share_emails: [] })} />
        Upload files, shared with:
      </label>
      {on && (
        <div className="space-y-2 pl-6">
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            {personas.map((p) => (
              <label key={p.name} className="flex items-center gap-1.5 text-xs">
                <input
                  type="checkbox"
                  checked={shareP.includes(p.name)}
                  onChange={(e) =>
                    onChange({
                      share_personas: e.target.checked ? [...shareP, p.name] : shareP.filter((n) => n !== p.name),
                      share_emails: shareE,
                    })
                  }
                />
                {p.display_name || p.name}
              </label>
            ))}
          </div>
          <ChipInput
            values={shareE}
            onAdd={(email) => {
              const v = email.trim();
              setEmailDraft("");
              if (v && !shareE.includes(v)) onChange({ share_personas: shareP, share_emails: [...shareE, v] });
            }}
            onRemove={(email) => onChange({ share_personas: shareP, share_emails: shareE.filter((e) => e !== email) })}
            draft={emailDraft}
            onDraftChange={setEmailDraft}
            placeholder="add an address + Enter"
            label="Add address"
          />
          <p className="text-xs text-muted-foreground">
            Each upload lands in the uploader's own folder and is shared with these as viewer. The uploader sees their
            own uploads and nothing else of theirs.
          </p>
        </div>
      )}
    </fieldset>
  );
}
