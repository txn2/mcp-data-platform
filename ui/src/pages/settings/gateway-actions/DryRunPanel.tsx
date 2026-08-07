import { useState, useCallback, useId } from "react";
import { useDryRunEnrichmentRule } from "@/api/admin/hooks";
import type { DryRunResponse } from "@/api/admin/types";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { AlertCircle, Check, Play } from "lucide-react";
import { Field } from "./Field";

// ---------------------------------------------------------------------------
// DryRunPanel — sample input + result preview
// ---------------------------------------------------------------------------

export function DryRunPanel({
  connectionName,
  ruleId,
}: {
  connectionName: string;
  ruleId: string;
}) {
  const dryRun = useDryRunEnrichmentRule(connectionName);
  const ids = useId();
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
        <Button type="button" size="sm" onClick={handleRun} disabled={dryRun.isPending}>
          <Play />
          {dryRun.isPending ? "Running..." : "Run"}
        </Button>
      </div>

      <Field label="Sample args (JSON)" htmlFor={`${ids}-args`}>
        {/* field-sizing-fixed: ui/textarea would otherwise size to its
            content and ignore the three rows asked for. */}
        <Textarea
          id={`${ids}-args`}
          rows={3}
          value={argsJSON}
          onChange={(e) => setArgsJSON(e.target.value)}
          className="field-sizing-fixed min-h-0 px-2 py-1 font-mono text-xs"
        />
      </Field>

      <Field label="Sample response (JSON)" htmlFor={`${ids}-response`}>
        <Textarea
          id={`${ids}-response`}
          rows={3}
          value={respJSON}
          onChange={(e) => setRespJSON(e.target.value)}
          className="field-sizing-fixed min-h-0 px-2 py-1 font-mono text-xs"
        />
      </Field>

      <Field label="User email" htmlFor={`${ids}-user`}>
        <Input
          id={`${ids}-user`}
          type="text"
          value={userEmail}
          onChange={(e) => setUserEmail(e.target.value)}
          className="h-8 px-2 text-xs"
        />
      </Field>

      {parseError && (
        <Alert variant="destructive" className="px-3 py-2">
          <AlertDescription className="text-xs">{parseError}</AlertDescription>
        </Alert>
      )}

      {result && <DryRunResult result={result} />}
    </div>
  );
}

// DryRunResult reports what the rule did with the sample input: which sources
// fired, what the run warned about, and the response the client would receive.
function DryRunResult({ result }: { result: DryRunResponse }) {
  return (
    <div className="space-y-2">
      {result.fired && result.fired.length > 0 && (
        <div className="rounded-md border px-3 py-2 text-xs">
          <div className="mb-1 font-semibold">Trace</div>
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
                <span>
                  {f.source}.{f.op}
                </span>
                <span className="text-muted-foreground">{f.duration_ms}ms</span>
                {f.error && <span className="text-destructive">{f.error}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
      {result.warnings && result.warnings.length > 0 && (
        <Alert variant="warning" className="px-3 py-2">
          <AlertTitle className="text-xs">Warnings</AlertTitle>
          <AlertDescription>
            <ul className="list-inside list-disc space-y-0.5 text-xs">
              {result.warnings.map((w, i) => (
                <li key={i}>{w}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      <div>
        <div className="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Merged response
        </div>
        <pre className="overflow-x-auto rounded-md border bg-muted/30 p-2 font-mono text-xs">
          {JSON.stringify(result.response, null, 2)}
        </pre>
      </div>
    </div>
  );
}
