import { useState } from "react";
import { Button } from "@/components/ui/button";
import type { EffectiveAccess, ToolAccessItem, ConnectionAccessItem, OperationDecision } from "@/api/admin/types";
import { ApiRulesSection } from "./ApiRulesSection";
import { ApiScopeGroups } from "./ApiScopeGroups";
import { useApiRouteScope } from "./useApiRouteScope";
import { RuleRows, type RuleRow } from "./RuleRows";
import { EditorSection } from "./sections";
import type { PersonaDraft } from "./types";

// The tool lists a persona can start from (#2257). None makes anyone an
// administrator: that is the administrator persona's, by name.
const STARTING_POINTS: { name: string; hint: string; allow: string[]; deny: string[] }[] = [
  { name: "All tools", hint: "Every tool", allow: ["*"], deny: [] },
  {
    name: "Read only",
    hint: "search, browse, get, list, describe",
    allow: ["*_search", "*_browse", "*_get_*", "*_list_*", "*_describe_*"],
    deny: [],
  },
  {
    name: "Analyst",
    hint: "Query and catalog, nothing that changes data",
    allow: ["trino_*", "search", "fetch", "s3_get_*", "s3_list_*"],
    deny: ["*_delete_*", "*_execute"],
  },
  { name: "Clear", hint: "No tools", allow: [], deny: [] },
];

const EXTERNAL_LOCK = "External calls no AI tool. The rules are kept for if the persona becomes internal again.";

function toolDecision(t: ToolAccessItem): string {
  switch (t.decision) {
    case "external":
      return "External: not callable";
    case "deny":
      return `Denied by ${t.matched}`;
    case "none":
      return "No rule allows it";
  }
  if (!t.callable) return `Allowed by ${t.matched}, but none of its connections is`;
  return t.exact ? "Allowed" : `Allowed by ${t.matched}`;
}

function toolDetail(t: ToolAccessItem): string {
  if (t.connections.length === 0) return t.kind ? `${t.kind}, no connection` : "Platform tool";
  const allowed = t.connections.filter((c) => c.allowed).length;
  return `${t.kind ?? ""} on ${allowed} of ${t.connections.length} connections`.trim();
}

export function ToolsSection({
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
  const rows: RuleRow[] = (report?.tools.items ?? []).map((t) => ({
    name: t.name,
    detail: toolDetail(t),
    decision: toolDecision(t),
    usable: t.callable,
  }));
  const locked = draft.external;
  return (
    <EditorSection
      id="tools"
      title="Tools"
      governs="Which MCP tools the persona's people can list and call, and the REST gateway that runs through them."
    >
      <div className="mb-3 flex flex-wrap items-center gap-1.5">
        <span className="text-xs text-muted-foreground">Starting points:</span>
        {STARTING_POINTS.map((s) => (
          <Button
            key={s.name}
            type="button"
            size="sm"
            variant="outline"
            className="h-7 text-xs"
            title={s.hint}
            disabled={isReadOnly || locked}
            onClick={() => onUpdate({ allowTools: s.allow, denyTools: s.deny })}
          >
            {s.name}
          </Button>
        ))}
      </div>
      <RuleRows
        rows={rows}
        lists={{ allow: draft.allowTools, deny: draft.denyTools }}
        onChange={(l) => onUpdate({ allowTools: l.allow, denyTools: l.deny })}
        locked={locked}
        lockedNote={EXTERNAL_LOCK}
        isReadOnly={isReadOnly}
        noun="tools"
      />
    </EditorSection>
  );
}

function connectionRow(c: ConnectionAccessItem, report: EffectiveAccess): RuleRow {
  const badges: RuleRow["badges"] = [];
  if (c.scratch) {
    badges.push({ text: `Scratch: ${c.scratch.catalog}.${c.scratch.schema}`, tone: "info" });
    if (report.scratch.some((s) => s.connection === c.name && s.drop_via_sql)) {
      badges.push({ text: "Can drop any registered table through SQL", tone: "warn" });
    }
  }
  for (const ch of c.used_by_channels) badges.push({ text: `Notification channel ${ch}`, tone: "info" });
  const decision =
    c.decision === "allow" ? (c.exact ? "Allowed" : `Allowed by ${c.matched}`) : c.decision === "deny" ? `Denied by ${c.matched}` : "No rule allows it";
  return { name: c.name, detail: c.kind, decision, usable: c.decision === "allow", badges };
}

export function ConnectionsSection({
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
  const rows = report ? report.connections.items.map((c) => connectionRow(c, report)) : [];
  return (
    <EditorSection
      id="connections"
      title="Connections"
      governs="Which data sources the persona's tool calls reach, and what search, the catalog and scratch tables show them. A connection no rule allows is closed."
    >
      <RuleRows
        rows={rows}
        lists={{ allow: draft.allowConnections, deny: draft.denyConnections }}
        onChange={(l) => onUpdate({ allowConnections: l.allow, denyConnections: l.deny })}
        locked={draft.external}
        lockedNote={EXTERNAL_LOCK}
        isReadOnly={isReadOnly}
        noun="connections"
      />
    </EditorSection>
  );
}

// BLOCKED names what keeps an operation from being called whatever its rules.
const BLOCKED: Partial<Record<OperationDecision, string>> = {
  connection: "Connection not allowed",
  tool: "api_invoke_endpoint not allowed",
  external: "External",
};

export function ApiEndpointsSection({
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
  const [selected, setSelected] = useState<string | null>(null);
  const [hovered, setHovered] = useState<string | null>(null);
  const [highlight, setHighlight] = useState<number | null>(null);
  const api = useApiRouteScope({ draft, onUpdate, scope: "api", selected, hovered, highlightIndex: highlight });
  const blocked = (connection: string, op: { method: string; path: string }) =>
    BLOCKED[
      report?.api_operations.items.find(
        (o) => o.connection === connection && o.method === op.method.toUpperCase() && o.path === op.path,
      )?.decision ?? "open"
    ];
  return (
    <EditorSection
      id="api"
      title="API endpoints"
      governs="The methods and paths api_invoke_endpoint may call on API connections. The REST gateway follows the same rules. An operation is callable only when its connection is allowed above and api_invoke_endpoint is a tool the persona can call."
    >
      <fieldset disabled={isReadOnly || draft.external} className="space-y-4">
        <ApiRulesSection
          rules={draft.apiRoutes}
          connectionNames={api.connections.map((c) => c.name)}
          onAdd={api.addRule}
          onRemove={(i) => {
            setHighlight(null);
            api.removeRule(i);
          }}
          highlightIndex={highlight}
          onHighlight={setHighlight}
        />
        <div className="rounded border">
          <ApiScopeGroups
            connections={api.connections}
            resolve={api.resolveFor}
            statusFilter="all"
            search=""
            selected={selected}
            setSelected={setSelected}
            setHovered={setHovered}
            handlers={{ setOperation: api.setOperationRule, setConnection: api.setConnectionRule }}
            isLoading={api.isLoading}
            governedBy={api.governedBy}
            blocked={blocked}
          />
        </div>
      </fieldset>
    </EditorSection>
  );
}
