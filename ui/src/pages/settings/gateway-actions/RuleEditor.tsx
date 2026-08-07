import { useState, useCallback, useMemo, useId } from "react";
import { useCreateEnrichmentRule, useUpdateEnrichmentRule } from "@/api/admin/hooks";
import type { EnrichmentRule, EnrichmentRuleBody } from "@/api/admin/types";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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

  const ids = useId();
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
          <Button type="button" variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={handleSave}
            disabled={create.isPending || update.isPending}
          >
            <Save />
            {rule ? "Update" : "Create"}
          </Button>
        </div>
      </div>

      {error && (
        <Alert variant="destructive" className="px-3 py-2">
          <AlertCircle />
          <AlertDescription className="text-xs">{error}</AlertDescription>
        </Alert>
      )}

      <Field
        label="Tool name"
        hint="The proxied tool this rule applies to (e.g. crm__get_contact)."
        htmlFor={`${ids}-tool`}
      >
        <Input
          id={`${ids}-tool`}
          type="text"
          className="h-8 px-2 font-mono text-xs"
          value={body.tool_name}
          onChange={(e) => setBody({ ...body, tool_name: e.target.value })}
          placeholder={`${connectionName}__some_tool`}
        />
      </Field>

      <Field label="Description" htmlFor={`${ids}-description`}>
        <Input
          id={`${ids}-description`}
          type="text"
          className="h-8 px-2 text-xs"
          value={body.description ?? ""}
          onChange={(e) => setBody({ ...body, description: e.target.value })}
          placeholder="What this rule does"
        />
      </Field>

      <Field label="Enabled">
        {/* A native checkbox: no checkbox primitive is vendored, and the one
            binary control in this editor does not justify the dependency. */}
        <Label className="text-xs font-normal">
          <input
            type="checkbox"
            checked={body.enabled}
            onChange={(e) => setBody({ ...body, enabled: e.target.checked })}
          />
          Rule fires on matching tool calls
        </Label>
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
