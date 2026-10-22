import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { ResolvedPortal } from "@/lib/portalAccess";
import type { PersonaDraft } from "./types";

// The leveled areas, each with the choices its dropdown offers, lowest first
// (#2225).
const LEVELED: {
  key: "assets" | "resources" | "knowledge" | "catalog" | "apis";
  label: string;
  options: { value: string; label: string }[];
}[] = [
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
    options: [
      { value: "none", label: "None" },
      { value: "shared", label: "Read" },
      { value: "own", label: "Own folder" },
      { value: "persona", label: "Own and persona folders" },
    ],
  },
  {
    key: "knowledge",
    label: "Knowledge pages",
    options: [
      { value: "none", label: "None" },
      { value: "read", label: "Read" },
      { value: "write", label: "Edit" },
    ],
  },
  {
    key: "catalog",
    label: "Catalog",
    options: [
      { value: "none", label: "None" },
      { value: "read", label: "Read" },
      { value: "write", label: "Edit" },
    ],
  },
  {
    key: "apis",
    label: "APIs",
    options: [
      { value: "none", label: "None" },
      { value: "browse", label: "Browse" },
    ],
  },
];

// The on/off areas, with the line that says what each covers where its name
// does not.
const SWITCHES: { key: "feedback" | "prompts" | "automations" | "search" | "memory" | "download"; label: string; help?: string }[] = [
  { key: "feedback", label: "Feedback", help: "Threads, comments and sign-off" },
  { key: "prompts", label: "Prompts" },
  { key: "automations", label: "Automations" },
  { key: "search", label: "Search" },
  { key: "memory", label: "Memory", help: "Memory and insights" },
  { key: "download", label: "Download", help: "Download files and export PDFs" },
];

// PortalTab is the persona editor's Portal tab: what the persona's people may
// do in the portal, one setting per area (#2225), and whether they may share
// what they own (#2241). The server enforces each on every route of its area.
export function PortalTab({
  draft,
  onUpdate,
  isReadOnly,
}: {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  isReadOnly: boolean;
}) {
  const set = (partial: Partial<ResolvedPortal>) => onUpdate({ portal: { ...draft.portal, ...partial } });
  return (
    <div className="flex-1 overflow-y-auto px-6 py-5">
      <p className="mb-5 text-xs text-muted-foreground">
        Administrators have every area, whatever is set here.
      </p>
      <fieldset disabled={isReadOnly} className="contents">
        <label className="mb-5 flex items-start gap-2 text-sm">
          <input
            type="checkbox"
            className="mt-0.5"
            checked={draft.external}
            onChange={(e) => onUpdate({ external: e.target.checked })}
            aria-describedby="persona-external-help"
          />
          <span>
            <span className="font-medium">External</span>
            <span id="persona-external-help" className="block text-xs text-muted-foreground">
              Sees only what is shared with them. No tools, keys, prompts or people directory.
            </span>
          </span>
        </label>
        <div className="grid max-w-xl grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-3">
          {LEVELED.map((area) => (
            <div key={area.key} className="contents">
              <label htmlFor={`persona-portal-${area.key}`} className="text-sm font-medium">
                {area.label}
              </label>
              <Select
                value={draft.portal[area.key]}
                onValueChange={(v) => set({ [area.key]: v } as Partial<ResolvedPortal>)}
                disabled={isReadOnly}
              >
                <SelectTrigger id={`persona-portal-${area.key}`} size="sm" className="h-8 w-56" aria-label={area.label}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {area.options.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          ))}
        </div>
        <div className="mt-6 space-y-3">
          {/* Native checkboxes, as on the Behavior tab: no checkbox primitive
              is vendored. */}
          {SWITCHES.map((sw) => (
            <label key={sw.key} className="flex items-start gap-2 text-sm">
              <input
                type="checkbox"
                className="mt-0.5"
                checked={draft.portal[sw.key]}
                onChange={(e) => set({ [sw.key]: e.target.checked } as Partial<ResolvedPortal>)}
                aria-describedby={sw.help ? `persona-portal-${sw.key}-help` : undefined}
              />
              <span>
                <span className="font-medium">{sw.label}</span>
                {sw.help && (
                  <span id={`persona-portal-${sw.key}-help`} className="block text-xs text-muted-foreground">
                    {sw.help}
                  </span>
                )}
              </span>
            </label>
          ))}
          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={draft.allowSharing}
              onChange={(e) => onUpdate({ allowSharing: e.target.checked })}
              aria-describedby="persona-sharing-help"
            />
            <span>
              <span className="font-medium">Sharing</span>
              <span id="persona-sharing-help" className="block text-xs text-muted-foreground">
                Share what they own with a person or to a channel
              </span>
            </span>
          </label>
        </div>
      </fieldset>
    </div>
  );
}
