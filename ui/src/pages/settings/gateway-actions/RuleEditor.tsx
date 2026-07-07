import { useState, useCallback, useMemo } from "react";
import { useCreateEnrichmentRule, useUpdateEnrichmentRule } from "@/api/admin/hooks";
import type { EnrichmentRule, EnrichmentRuleBody } from "@/api/admin/types";
import { AlertCircle, Save } from "lucide-react";
import { Field, JSONField } from "./Field";
import { DryRunPanel } from "./DryRunPanel";

// ---------------------------------------------------------------------------
// RuleEditor — create/edit a single rule with JSON editors + dry-run panel
// ---------------------------------------------------------------------------

function emptyRuleBody(): EnrichmentRuleBody {
  return {
    tool_name: "",
    when_predicate: { kind: "always" },
    enrich_action: { source: "trino", operation: "query", parameters: {} },
    merge_strategy: { kind: "path", path: "enrichment" },
    description: "",
    enabled: true,
  };
}

export function RuleEditor({
  connectionName,
  rule,
  onClose,
}: {
  connectionName: string;
  rule: EnrichmentRule | null;
  onClose: () => void;
}) {
  const create = useCreateEnrichmentRule(connectionName);
  const update = useUpdateEnrichmentRule(connectionName);

  const initialBody = useMemo<EnrichmentRuleBody>(() => {
    if (!rule) return emptyRuleBody();
    return {
      tool_name: rule.tool_name,
      when_predicate: rule.when_predicate,
      enrich_action: rule.enrich_action,
      merge_strategy: rule.merge_strategy,
      description: rule.description ?? "",
      enabled: rule.enabled,
    };
  }, [rule]);

  const [body, setBody] = useState<EnrichmentRuleBody>(initialBody);
  const [error, setError] = useState<string | null>(null);

  const handleSave = useCallback(async () => {
    setError(null);
    try {
      if (rule) {
        await update.mutateAsync({ id: rule.id, ...body });
      } else {
        await create.mutateAsync(body);
      }
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Save failed");
    }
  }, [rule, body, create, update, onClose]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold">{rule ? "Edit rule" : "New rule"}</h3>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleSave}
            disabled={create.isPending || update.isPending}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            <Save className="h-3 w-3" />
            {rule ? "Update" : "Create"}
          </button>
        </div>
      </div>

      {error && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <AlertCircle className="h-3.5 w-3.5 mt-0.5 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      <Field label="Tool name" hint="The proxied tool this rule applies to (e.g. crm__get_contact).">
        <input
          type="text"
          className="w-full rounded-md border bg-background px-2 py-1 text-xs font-mono"
          value={body.tool_name}
          onChange={(e) => setBody({ ...body, tool_name: e.target.value })}
          placeholder={`${connectionName}__some_tool`}
        />
      </Field>

      <Field label="Description">
        <input
          type="text"
          className="w-full rounded-md border bg-background px-2 py-1 text-xs"
          value={body.description ?? ""}
          onChange={(e) => setBody({ ...body, description: e.target.value })}
          placeholder="What this rule does"
        />
      </Field>

      <Field label="Enabled">
        <label className="inline-flex items-center gap-2 text-xs">
          <input
            type="checkbox"
            checked={body.enabled}
            onChange={(e) => setBody({ ...body, enabled: e.target.checked })}
          />
          Rule fires on matching tool calls
        </label>
      </Field>

      <JSONField
        label="When predicate"
        hint='Examples: {"kind":"always"} or {"kind":"response_contains","paths":["$.email"]}'
        value={body.when_predicate}
        onChange={(v) => setBody({ ...body, when_predicate: v })}
      />

      <JSONField
        label="Enrich action"
        hint='source must be "trino" or "datahub". String parameters starting with $. are JSONPath bindings against {args, response, user}.'
        value={body.enrich_action}
        onChange={(v) => setBody({ ...body, enrich_action: v })}
      />

      <JSONField
        label="Merge strategy"
        hint='{"kind":"path","path":"warehouse_signals"} attaches the source result under response.warehouse_signals.'
        value={body.merge_strategy}
        onChange={(v) => setBody({ ...body, merge_strategy: v })}
      />

      {rule && (
        <div className="border-t pt-4">
          <DryRunPanel connectionName={connectionName} ruleId={rule.id} />
        </div>
      )}
    </div>
  );
}
