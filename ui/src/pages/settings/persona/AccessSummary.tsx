import type { ReactNode } from "react";
import type { EffectiveAccess, PersonaReferences, ScratchAccess } from "@/api/admin/types";
import { resolvePortal, type ResolvedPortal } from "@/lib/portalAccess";
import { EditorSection } from "./sections";
import type { PersonaDraft } from "./types";

// adminHref is a page of the admin console.
function adminHref(path: string): string {
  return `${(import.meta.env.BASE_URL || "/").replace(/\/$/, "")}/admin/${path}`;
}

// The portal areas a person may open, in the words the navigation uses.
const PORTAL_AREAS: { label: string; open: (p: ResolvedPortal) => boolean }[] = [
  { label: "Assets they own", open: (p) => p.assets === "own" },
  { label: "Resources", open: (p) => p.resources !== "none" },
  { label: "Knowledge pages", open: (p) => p.knowledge !== "none" },
  { label: "Catalog", open: (p) => p.catalog !== "none" },
  { label: "APIs", open: (p) => p.apis !== "none" },
  { label: "Scratch tables", open: (p) => p.scratch_tables !== "none" },
  { label: "Feedback", open: (p) => p.feedback },
  { label: "Prompts", open: (p) => p.prompts },
  { label: "Automations", open: (p) => p.automations },
  { label: "Search", open: (p) => p.search },
  { label: "Memory", open: (p) => p.memory },
  { label: "PDF export", open: (p) => p.download },
];

// SCOPE_TEXT names a resource folder scope the server reports.
function scopeText(scope: string): string {
  if (scope === "user") return "their own folder";
  if (scope === "global") return "Global";
  if (scope === "persona:*") return "every persona's folder";
  return `the ${scope.slice("persona:".length)} persona's folder`;
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid gap-1 border-b py-2 last:border-b-0 sm:grid-cols-[11rem_minmax(0,1fr)]">
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="text-sm">{children}</dd>
    </div>
  );
}

function countLine(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** referenceLines are the grants other objects hold that name the persona. */
export function referenceLines(r: PersonaReferences): { text: string; href?: string }[] {
  const out: { text: string; href?: string }[] = [];
  if (r.secrets.named.length > 0) {
    out.push({ text: `Stored secrets naming this persona: ${r.secrets.named.join(", ")}`, href: adminHref("secrets") });
  }
  if (r.secrets.unrestricted > 0) {
    out.push({ text: `${countLine(r.secrets.unrestricted, "stored secret names", "stored secrets name")} no persona, so this one may use them`, href: adminHref("secrets") });
  }
  if (r.script_grants > 0) out.push({ text: countLine(r.script_grants, "automation run grant", "automation run grants"), href: adminHref("automations") });
  if (r.script_versions > 0) {
    out.push({ text: `${countLine(r.script_versions, "automation runs", "automations run")} with roles that resolve here, so editing this persona changes what they can do`, href: adminHref("automations") });
  }
  if (r.prompts > 0) out.push({ text: countLine(r.prompts, "prompt scoped here", "prompts scoped here"), href: adminHref("prompts") });
  if (r.shares > 0) out.push({ text: countLine(r.shares, "share to this persona", "shares to this persona") });
  if (r.webhook_sources.length > 0) {
    out.push({ text: `Webhook sources writing to its folder: ${r.webhook_sources.join(", ")}`, href: adminHref("webhooks") });
  }
  if (r.api_keys > 0) out.push({ text: countLine(r.api_keys, "API key resolves here", "API keys resolve here"), href: adminHref("keys") });
  if (r.people.length > 0) out.push({ text: countLine(r.people.length, "person", "people"), href: adminHref("users") });
  return out;
}

// AccessSummary is the server's evaluation of the unsaved persona (#2257):
// what its people reach, what is granted to it elsewhere, and what no persona
// setting governs. Read-only.
export function AccessSummary({
  draft,
  report,
  isLoading,
}: {
  draft: PersonaDraft;
  report: EffectiveAccess | undefined;
  isLoading: boolean;
}) {
  return (
    <EditorSection id="summary" title="Access summary" governs="Computed by the server from the settings on this page, saved or not.">
      {!report ? (
        <p className="text-xs text-muted-foreground">{isLoading ? "Computing…" : "The summary is unavailable."}</p>
      ) : (
        <SummaryBody draft={draft} report={report} />
      )}
    </EditorSection>
  );
}

function SummaryBody({ draft, report }: { draft: PersonaDraft; report: EffectiveAccess }) {
  return (
    <div className="max-w-4xl space-y-6">
      {report.account_type === "external" && <ExternalPanel />}
      {report.is_admin && (
        <p className="rounded border bg-muted/30 px-3 py-2 text-xs">
          Administrators have every portal area, so the portal settings below are not used. Tool and connection
          rules still apply.
        </p>
      )}
      <dl>
        <Row label="Who">{whoText(draft, report)}</Row>
        <Row label="AI tools">
          Can call {report.tools.callable} of {report.tools.total} tools, on {report.connections.allowed} of{" "}
          {report.connections.total} connections.
        </Row>
        <Row label="API endpoints">{apiText(report)}</Row>
        <Row label="Scratch tables">
          <ScratchLines report={report} />
        </Row>
        <Row label="Portal">
          <PortalLines report={report} />
        </Row>
        <Row label="Resource folders">
          <FolderLines report={report} />
        </Row>
        <Row label="Sharing">{report.sharing ? "Can share what they own." : "Cannot share."}</Row>
        <Row label="Memory">
          {report.tools.items.some((t) => t.name === "memory_manage" && t.callable)
            ? "Shared with everyone of this persona through memory_manage."
            : "No memory tool."}
        </Row>
        <Row label="Admin console">{report.is_admin ? "Yes." : "No."}</Row>
      </dl>
      <GrantedElsewhere refs={referenceLines(report.references)} />
      <Ungoverned report={report} />
    </div>
  );
}

function whoText(draft: PersonaDraft, report: EffectiveAccess): string {
  const roles = draft.roles.length > 0 ? `Anyone holding ${draft.roles.join(", ")}` : "No role assigns it";
  const mapped = report.assignment.role_mapping;
  return mapped.length > 0 ? `${roles}, and role mapping from ${mapped.join(", ")}` : roles;
}

function apiText(report: EffectiveAccess): string {
  const ops = report.api_operations;
  return ops.total === 0 ? "No API connection declares operations." : `Can call ${ops.callable} of ${ops.total} operations.`;
}

function scratchLine(s: ScratchAccess): string {
  const can = s.register ? "can register" : s.list ? "can list" : "cannot list or register";
  let line = `${s.catalog}.${s.schema} on ${s.connection}: ${can}; ${countLine(s.registrations, "registered table", "registered tables")}.`;
  if (s.drop_via_sql) line += " Can drop any registered table through SQL.";
  if (s.queryable_from.length > 0) {
    line += ` Reads every registered table through ${s.queryable_from.join(", ")} if its Trino user reads the scratch catalog.`;
  }
  return line;
}

function ScratchLines({ report }: { report: EffectiveAccess }) {
  if (report.scratch.length === 0) return <>None.</>;
  return (
    <ul className="space-y-1">
      {report.scratch.map((s) => (
        <li key={s.connection}>{scratchLine(s)}</li>
      ))}
    </ul>
  );
}

function PortalLines({ report }: { report: EffectiveAccess }) {
  const effective = resolvePortal(report.portal.effective);
  const opens = PORTAL_AREAS.filter((a) => a.open(effective)).map((a) => a.label);
  const closed = PORTAL_AREAS.filter((a) => !a.open(effective)).map((a) => a.label);
  return (
    <>
      <span className="block">Can open: {opens.length > 0 ? opens.join(", ") : "nothing"}.</span>
      {closed.length > 0 && <span className="block text-muted-foreground">Cannot open: {closed.join(", ")}.</span>}
    </>
  );
}

function FolderLines({ report }: { report: EffectiveAccess }) {
  const reads = report.resource_scopes.length > 0 ? report.resource_scopes.map(scopeText).join(", ") : "no folder";
  const readers = report.assignment.folder_reader_roles.join(", ") || "none of its roles";
  return (
    <>
      <span className="block">Reads {reads}.</span>
      <span className="block text-muted-foreground">
        This persona's folder is read by anyone holding {readers} and written by role{" "}
        <span className="font-mono">{report.assignment.folder_writer_role}</span> and administrators.
      </span>
    </>
  );
}

function GrantedElsewhere({ refs }: { refs: { text: string; href?: string }[] }) {
  return (
    <div>
      <h4 className="text-sm font-semibold">Granted elsewhere</h4>
      {refs.length === 0 ? (
        <p className="mt-1 text-xs text-muted-foreground">Nothing else names this persona.</p>
      ) : (
        <ul className="mt-1 list-disc space-y-0.5 pl-5 text-sm">
          {refs.map((r) => (
            <li key={r.text}>
              {r.href ? (
                <a className="underline" href={r.href}>
                  {r.text}
                </a>
              ) : (
                r.text
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function Ungoverned({ report }: { report: EffectiveAccess }) {
  return (
    <div>
      <h4 className="text-sm font-semibold">Not governed by personas</h4>
      <ul className="mt-1 list-disc space-y-0.5 pl-5 text-sm">
        <li>
          {countLine(report.unscoped.signed_in_endpoints, "signed-in endpoint", "signed-in endpoints")}, open to any
          signed-in account, External included.
        </li>
        <li>Per-connection limits and consent prompts.</li>
        <li>Rate limits.</li>
      </ul>
    </div>
  );
}

function ExternalPanel() {
  return (
    <div className="rounded border bg-muted/30 px-3 py-2 text-xs">
      <p className="font-medium">External takes away</p>
      <p className="mt-0.5">
        Every AI tool, API keys, the people directory, knowledge pages, the catalog, the API browser, scratch tables,
        search, prompts, automations, memory, sharing, External files, signed-in endpoints, and every folder but
        their own.
      </p>
      <p className="mt-2 font-medium">You still decide</p>
      <p className="mt-0.5">Resources (none, or their own folder), Feedback, PDF export, Earlier versions, and Uploads.</p>
    </div>
  );
}
