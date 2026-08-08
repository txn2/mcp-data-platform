import { useState } from "react";
import { ExternalLink, Eye, EyeOff, Search } from "lucide-react";
import {
  useSetToolVisibility,
  useTestPersonaAccess,
} from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { PersonaTestAccessResult, ToolDetail } from "@/api/admin/types";
import { errorMessage } from "@/lib/utils";
import { PersonaDecisionTable } from "../parts/PersonaDecisionTable";

export function VisibilityTab({ detail }: { detail: ToolDetail }) {
  return (
    <div className="space-y-4">
      <KillSwitchSection detail={detail} />
      <PersonaAccessSection detail={detail} />
      <PreviewSection detail={detail} />
    </div>
  );
}

// KillSwitchSection is the global tools.deny toggle: what the tool's current
// visibility is, what would change it, and the one case where the toggle cannot
// help — a glob entry, which only Config can edit.
function KillSwitchSection({ detail }: { detail: ToolDetail }) {
  const setVisibility = useSetToolVisibility(detail.name);

  // When a glob (not a literal name) matches in tools.deny, toggling the
  // literal off does nothing — the glob still matches. Disable the button
  // and direct the user to Config instead.
  const globHidden =
    detail.hidden_by_global_deny &&
    detail.global_deny_pattern !== undefined &&
    detail.global_deny_pattern !== detail.name;

  return (
    <SectionCard
      title="Global kill-switch"
      action={
        <Button
          size="sm"
          onClick={() => setVisibility.mutate({ hidden: !detail.hidden_by_global_deny })}
          disabled={setVisibility.isPending || globHidden}
          title={
            globHidden
              ? "Tool is hidden by a glob pattern. Edit the tools.deny entry in Config to remove it."
              : undefined
          }
        >
          {setVisibility.isPending
            ? "Saving…"
            : detail.hidden_by_global_deny
              ? "Show tool"
              : "Hide tool"}
        </Button>
      }
    >
      <VisibilityStatement detail={detail} />

      {globHidden && (
        <Alert variant="warning" className="mt-3">
          <AlertDescription>
            Tool matches a glob pattern, not its exact name. The glob entry must be
            edited in Config to make the tool visible again.
          </AlertDescription>
        </Alert>
      )}
      {setVisibility.isError && (
        <Alert variant="destructive" className="mt-3">
          <AlertDescription>
            Failed to save: {errorMessage(setVisibility.error)}
          </AlertDescription>
        </Alert>
      )}
    </SectionCard>
  );
}

// VisibilityStatement says what the platform is doing with the tool right now,
// and what the toggle beside it would change.
function VisibilityStatement({ detail }: { detail: ToolDetail }) {
  const hidden = detail.hidden_by_global_deny;
  return (
    <div className="flex items-start gap-3">
      {hidden ? (
        <EyeOff className="mt-0.5 size-5 text-amber-600 dark:text-amber-400" />
      ) : (
        <Eye className="mt-0.5 size-5 text-emerald-600 dark:text-emerald-400" />
      )}
      <div className="flex-1">
        <p className="text-sm font-medium">
          {hidden
            ? "Hidden by tools.deny"
            : "Visible to all clients (subject to persona auth)"}
        </p>
        <p className="text-xs text-muted-foreground">
          {hidden ? (
            <>
              Matched pattern <Mono>{detail.global_deny_pattern}</Mono>. Toggle to remove.
            </>
          ) : (
            <>
              Adding the tool to <Mono>tools.deny</Mono> removes it from{" "}
              <code>tools/list</code> for every client. Persona auth is unaffected.
            </>
          )}
        </p>
      </div>
    </div>
  );
}

function Mono({ children }: { children: React.ReactNode }) {
  return (
    <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11px]">{children}</code>
  );
}

function PersonaAccessSection({ detail }: { detail: ToolDetail }) {
  const personas = detail.personas ?? [];
  const allowed = personas.filter((p) => p.allowed).length;
  const personasHref = `/portal/admin/personas?affects=${encodeURIComponent(detail.name)}`;

  return (
    <SectionCard
      title={`Persona access (${allowed} allow · ${personas.length - allowed} deny)`}
      action={
        <Button asChild variant="link" size="xs" className="px-0">
          <a href={personasHref}>
            Edit persona rules <ExternalLink />
          </a>
        </Button>
      }
    >
      {personas.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No database-managed personas. File-only personas live on the Personas page.
        </p>
      ) : (
        <PersonaDecisionTable personas={personas} />
      )}
    </SectionCard>
  );
}

// PreviewSection answers the decision for a persona that is not in the table —
// a file-only persona, or one being drafted.
function PreviewSection({ detail }: { detail: ToolDetail }) {
  const testAccess = useTestPersonaAccess();
  const [previewPersona, setPreviewPersona] = useState("");
  const [previewResult, setPreviewResult] = useState<PersonaTestAccessResult | null>(null);

  function runPreview(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!previewPersona) return;
    testAccess.mutate(
      { persona: previewPersona, toolName: detail.name },
      { onSuccess: (data) => setPreviewResult(data) },
    );
  }

  return (
    <SectionCard title="Preview decision for an arbitrary persona">
      <form onSubmit={runPreview} className="flex flex-wrap items-center gap-2">
        <Input
          type="text"
          value={previewPersona}
          onChange={(e) => setPreviewPersona(e.target.value)}
          placeholder="persona name"
          aria-label="Persona name"
          className="w-48"
        />
        <Button
          type="submit"
          variant="outline"
          size="sm"
          disabled={testAccess.isPending || !previewPersona}
        >
          <Search />
          {testAccess.isPending ? "Checking…" : "Preview"}
        </Button>
      </form>
      {testAccess.isError && (
        <Alert variant="destructive" className="mt-2">
          <AlertDescription>{errorMessage(testAccess.error)}</AlertDescription>
        </Alert>
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
    </SectionCard>
  );
}
