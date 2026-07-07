import { useState, useCallback } from "react";
import { useDryRunEnrichmentRule } from "@/api/admin/hooks";
import type { DryRunResponse } from "@/api/admin/types";
import { AlertCircle, Check, Play } from "lucide-react";
import { Field } from "./Field";

// ---------------------------------------------------------------------------
// DryRunPanel — sample input + result preview
// ---------------------------------------------------------------------------

export function DryRunPanel({ connectionName, ruleId }: { connectionName: string; ruleId: string }) {
  const dryRun = useDryRunEnrichmentRule(connectionName);
  const [argsJSON, setArgsJSON] = useState('{"id": 7}');
  const [respJSON, setRespJSON] = useState('{"email": "x@x.com"}');
  const [userEmail, setUserEmail] = useState("admin@example.com");
  const [result, setResult] = useState<DryRunResponse | null>(null);
  const [parseError, setParseError] = useState<string | null>(null);

  const handleRun = useCallback(async () => {
    setParseError(null);
    setResult(null);
    let args: Record<string, unknown>;
    let resp: unknown;
    try {
      args = JSON.parse(argsJSON);
      resp = JSON.parse(respJSON);
    } catch (err) {
      setParseError(err instanceof Error ? err.message : "Invalid JSON");
      return;
    }
    try {
      const r = await dryRun.mutateAsync({
        id: ruleId,
        body: { args, response: resp, user: { email: userEmail } },
      });
      setResult(r);
    } catch (err) {
      setParseError(err instanceof Error ? err.message : "Dry-run failed");
    }
  }, [argsJSON, respJSON, userEmail, ruleId, dryRun]);

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Dry run
        </h4>
        <button
          type="button"
          onClick={handleRun}
          disabled={dryRun.isPending}
          className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          <Play className="h-3 w-3" />
          {dryRun.isPending ? "Running..." : "Run"}
        </button>
      </div>

      <Field label="Sample args (JSON)">
        <textarea
          className="w-full rounded-md border bg-background px-2 py-1 text-xs font-mono"
          rows={3}
          value={argsJSON}
          onChange={(e) => setArgsJSON(e.target.value)}
        />
      </Field>

      <Field label="Sample response (JSON)">
        <textarea
          className="w-full rounded-md border bg-background px-2 py-1 text-xs font-mono"
          rows={3}
          value={respJSON}
          onChange={(e) => setRespJSON(e.target.value)}
        />
      </Field>

      <Field label="User email">
        <input
          type="text"
          className="w-full rounded-md border bg-background px-2 py-1 text-xs"
          value={userEmail}
          onChange={(e) => setUserEmail(e.target.value)}
        />
      </Field>

      {parseError && (
        <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          {parseError}
        </div>
      )}

      {result && (
        <div className="space-y-2">
          {result.fired && result.fired.length > 0 && (
            <div className="rounded-md border px-3 py-2 text-xs">
              <div className="font-semibold mb-1">Trace</div>
              <ul className="space-y-1 font-mono text-xs">
                {result.fired.map((f) => (
                  <li key={f.rule_id} className="flex items-center gap-2">
                    {f.skipped ? (
                      <span className="text-muted-foreground">⊘</span>
                    ) : f.error ? (
                      <AlertCircle className="h-3 w-3 text-destructive" />
                    ) : (
                      <Check className="h-3 w-3 text-emerald-500" />
                    )}
                    <span>{f.source}.{f.op}</span>
                    <span className="text-muted-foreground">{f.duration_ms}ms</span>
                    {f.error && <span className="text-destructive">{f.error}</span>}
                  </li>
                ))}
              </ul>
            </div>
          )}
          {result.warnings && result.warnings.length > 0 && (
            <div className="rounded-md border border-amber-500/30 bg-amber-50 px-3 py-2 text-xs dark:bg-amber-900/20">
              <div className="font-semibold mb-1">Warnings</div>
              <ul className="list-disc list-inside space-y-0.5">
                {result.warnings.map((w, i) => (
                  <li key={i}>{w}</li>
                ))}
              </ul>
            </div>
          )}
          <div>
            <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1">
              Merged response
            </div>
            <pre className="rounded-md border bg-muted/30 p-2 text-xs font-mono overflow-x-auto">
              {JSON.stringify(result.response, null, 2)}
            </pre>
          </div>
        </div>
      )}
    </div>
  );
}
