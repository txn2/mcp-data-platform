import { useState } from "react";
import { ExternalLink, Eye, EyeOff, Search } from "lucide-react";
import {
  useSetToolVisibility,
  useTestPersonaAccess,
} from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { PersonaTestAccessResult, ToolDetail } from "@/api/admin/types";
import { errorMessage } from "@/lib/utils";

export function VisibilityTab({ detail }: { detail: ToolDetail }) {
  const setVisibility = useSetToolVisibility(detail.name);
  const testAccess = useTestPersonaAccess();
  const [previewPersona, setPreviewPersona] = useState("");
  const [previewResult, setPreviewResult] =
    useState<PersonaTestAccessResult | null>(null);

  const personas = detail.personas ?? [];
  const allowed = personas.filter((p) => p.allowed);
  const denied = personas.filter((p) => !p.allowed);

  // When a glob (not a literal name) matches in tools.deny, toggling the
  // literal off does nothing — the glob still matches. Disable the button
  // and direct the user to Config instead.
  const globHidden =
    detail.hidden_by_global_deny &&
    detail.global_deny_pattern !== undefined &&
    detail.global_deny_pattern !== detail.name;

  const personasHref = `/portal/admin/personas?affects=${encodeURIComponent(detail.name)}`;

  function runPreview(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!previewPersona) return;
    testAccess.mutate(
      { persona: previewPersona, toolName: detail.name },
      { onSuccess: (data) => setPreviewResult(data) },
    );
  }

  return (
    <div className="space-y-6">
      <section>
        <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          Global kill-switch
        </h3>
        <div className="flex items-start gap-3 rounded-lg border bg-card p-4">
          {detail.hidden_by_global_deny ? (
            <EyeOff className="mt-0.5 h-5 w-5 text-yellow-700" />
          ) : (
            <Eye className="mt-0.5 h-5 w-5 text-green-700" />
          )}
          <div className="flex-1">
            <p className="text-sm font-medium">
              {detail.hidden_by_global_deny
                ? "Hidden by tools.deny"
                : "Visible to all clients (subject to persona auth)"}
            </p>
            <p className="text-xs text-muted-foreground">
              {detail.hidden_by_global_deny ? (
                <>
                  Matched pattern{" "}
                  <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11px]">
                    {detail.global_deny_pattern}
                  </code>
                  . Toggle below to remove.
                </>
              ) : (
                <>
                  Adding the tool to{" "}
                  <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11px]">
                    tools.deny
                  </code>{" "}
                  removes it from <code>tools/list</code> for every client.
                  Persona auth is unaffected.
                </>
              )}
            </p>
            {detail.hidden_by_global_deny &&
              detail.global_deny_pattern !== detail.name && (
                <p className="mt-1 text-xs text-yellow-800">
                  Tool matches a glob pattern, not its exact name. Toggling here
                  appends the literal name; the glob entry must be edited in
                  Config.
                </p>
              )}
          </div>
          <button
            onClick={() =>
              setVisibility.mutate({ hidden: !detail.hidden_by_global_deny })
            }
            disabled={setVisibility.isPending || globHidden}
            title={
              globHidden
                ? "Tool is hidden by a glob pattern. Edit the tools.deny entry in Config to remove it."
                : undefined
            }
            className="inline-flex items-center gap-2 rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground disabled:opacity-50"
          >
            {setVisibility.isPending
              ? "Saving…"
              : detail.hidden_by_global_deny
                ? "Show tool"
                : "Hide tool"}
          </button>
        </div>
        {setVisibility.isError && (
          <p className="mt-1 text-xs text-destructive">
            Failed to save: {errorMessage(setVisibility.error)}
          </p>
        )}
      </section>

      <section>
        <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          Persona access ({allowed.length} allow · {denied.length} deny)
        </h3>
        {personas.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No database-managed personas. File-only personas live on the
            Personas page.
          </p>
        ) : (
          <div className="overflow-hidden rounded border">
            <table className="w-full text-sm">
              <thead className="bg-muted/40 text-xs">
                <tr>
                  <th className="px-3 py-1.5 text-left font-medium">Persona</th>
                  <th className="px-3 py-1.5 text-left font-medium">Decision</th>
                  <th className="px-3 py-1.5 text-left font-medium">
                    Matched pattern
                  </th>
                  <th className="px-3 py-1.5 text-left font-medium">Source</th>
                </tr>
              </thead>
              <tbody>
                {personas.map((p) => (
                  <tr key={p.persona} className="border-t">
                    <td className="px-3 py-1.5 font-medium">{p.persona}</td>
                    <td className="px-3 py-1.5">
                      <StatusBadge variant={p.allowed ? "success" : "neutral"}>
                        {p.allowed ? "allow" : "deny"}
                      </StatusBadge>
                    </td>
                    <td className="px-3 py-1.5 font-mono text-xs">
                      {p.matched_pattern || (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </td>
                    <td className="px-3 py-1.5 text-xs text-muted-foreground">
                      {p.source}
                      {!p.connection_allowed && (
                        <span
                          className="ml-1 text-yellow-700"
                          title="Tool rule allows but the persona's connection rules deny this tool's connection."
                        >
                          · connection denied
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <a
          href={personasHref}
          className="mt-2 inline-flex items-center gap-1 text-xs text-primary hover:underline"
        >
          Edit persona rules <ExternalLink className="h-3 w-3" />
        </a>
      </section>

      <section>
        <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          Preview decision for an arbitrary persona
        </h3>
        <form
          onSubmit={runPreview}
          className="flex flex-wrap items-center gap-2"
        >
          <input
            type="text"
            value={previewPersona}
            onChange={(e) => setPreviewPersona(e.target.value)}
            placeholder="persona name"
            className="rounded border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
          />
          <button
            type="submit"
            disabled={testAccess.isPending || !previewPersona}
            className="inline-flex items-center gap-1 rounded border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50"
          >
            <Search className="h-3 w-3" />
            {testAccess.isPending ? "Checking…" : "Preview"}
          </button>
        </form>
        {testAccess.isError && (
          <p className="mt-1 text-xs text-destructive">
            {errorMessage(testAccess.error)}
          </p>
        )}
        {previewResult && !testAccess.isError && (
          <div className="mt-2 flex items-center gap-2 text-xs">
            <StatusBadge variant={previewResult.allowed ? "success" : "neutral"}>
              {previewResult.allowed ? "allow" : "deny"}
            </StatusBadge>
            <span className="text-muted-foreground">
              source: {previewResult.source}
              {previewResult.matched_pattern && (
                <>
                  {" "}· pattern{" "}
                  <code className="rounded bg-muted px-1 py-0.5 font-mono">
                    {previewResult.matched_pattern}
                  </code>
                </>
              )}
            </span>
          </div>
        )}
      </section>
    </div>
  );
}
