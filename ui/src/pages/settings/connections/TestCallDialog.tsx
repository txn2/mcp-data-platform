import { useState } from "react";
import { useTestCall, type TestOutcome } from "@/api/portal/hooks/callHandlers";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

// TestCallDialog is the test panel (#2436): every handler on a connection that
// would match a call, in order, and what the call would get, without calling
// the upstream and without spending a counter or an allowance. A handler's own
// reads do run, as they would for the real call. A transform needs a result to
// change: paste one to see what the caller would get.

const TOOLS = ["api_invoke_endpoint", "api_export", "trino_query", "trino_export", "graphql_query", "graphql_export"];

const STEP_WORDS: Record<string, { label: string; variant: "success" | "warning" | "danger" | "muted" | "info" }> = {
  allow: { label: "Allows", variant: "success" },
  deny: { label: "Refuses", variant: "danger" },
  require_approval: { label: "Needs approval", variant: "warning" },
  error: { label: "Failed", variant: "warning" },
  transformed: { label: "Changes the result", variant: "info" },
  original: { label: "Returns the original", variant: "muted" },
};

export function TestCallDialog({ connection, onClose }: { connection: string; onClose: () => void }) {
  const test = useTestCall(connection);
  const [asWhom, setAsWhom] = useState<"user" | "persona">("user");
  const [who, setWho] = useState("");
  const [tool, setTool] = useState("api_invoke_endpoint");
  const [args, setArgs] = useState('{\n  "method": "POST",\n  "path": "/",\n  "body": {}\n}');
  const [at, setAt] = useState("");
  const [result, setResult] = useState("");
  const [problem, setProblem] = useState("");

  const run = () => {
    setProblem("");
    let parsed: Record<string, unknown>;
    let pasted: unknown;
    try {
      parsed = args.trim() ? (JSON.parse(args) as Record<string, unknown>) : {};
    } catch {
      setProblem("The arguments are not JSON.");
      return;
    }
    try {
      pasted = result.trim() ? (JSON.parse(result) as unknown) : undefined;
    } catch {
      setProblem("The result to change is not JSON.");
      return;
    }
    test.mutate({
      tool,
      arguments: parsed,
      ...(asWhom === "user" ? { as_user: who.trim() } : { as_persona: who.trim() }),
      at: at ? new Date(at).toISOString() : undefined,
      result: pasted,
    });
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-4xl">
        <DialogHeader>
          <DialogTitle>Test a call on {connection}</DialogTitle>
          <DialogDescription>
            Shows every handler that would match and what the call would get. The upstream is not called, and no counter
            or allowance is spent. A handler&apos;s own reads do run.
          </DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,420px)_minmax(0,1fr)]">
          <div className="space-y-3">
            <div className="space-y-1">
              <Label htmlFor="test-as">As</Label>
              <div className="flex gap-2">
                <select
                  aria-label="As a person or a persona"
                  className="h-9 rounded-md border bg-background px-2 text-sm"
                  value={asWhom}
                  onChange={(e) => setAsWhom(e.target.value as "user" | "persona")}
                >
                  <option value="user">A person</option>
                  <option value="persona">A persona</option>
                </select>
                <Input
                  id="test-as"
                  placeholder={asWhom === "user" ? "name@example.com" : "marketing"}
                  value={who}
                  onChange={(e) => setWho(e.target.value)}
                />
              </div>
            </div>
            <div className="space-y-1">
              <Label htmlFor="test-tool">Tool</Label>
              <select
                id="test-tool"
                className="h-9 w-full rounded-md border bg-background px-2 text-sm"
                value={tool}
                onChange={(e) => setTool(e.target.value)}
              >
                {TOOLS.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1">
              <Label htmlFor="test-args">Arguments</Label>
              <Textarea id="test-args" rows={9} className="font-mono text-xs" value={args} onChange={(e) => setArgs(e.target.value)} />
              <p className="text-xs text-muted-foreground">
                As the caller would send them. A body may be an object or a string of JSON; both are read the same.
              </p>
            </div>
            <div className="space-y-1">
              <Label htmlFor="test-at">At</Label>
              <Input id="test-at" type="datetime-local" value={at} onChange={(e) => setAt(e.target.value)} />
              <p className="text-xs text-muted-foreground">Empty: now.</p>
            </div>
            <div className="space-y-1">
              <Label htmlFor="test-result">Result to change (optional)</Label>
              <Textarea id="test-result" rows={4} className="font-mono text-xs" value={result} onChange={(e) => setResult(e.target.value)} />
            </div>
            <div className="flex justify-end">
              <Button size="sm" onClick={run} disabled={test.isPending}>
                Test
              </Button>
            </div>
          </div>
          <div className="min-w-0 space-y-3">
            {problem && (
              <Alert variant="destructive">
                <AlertDescription>{problem}</AlertDescription>
              </Alert>
            )}
            {test.error && (
              <Alert variant="destructive">
                <AlertDescription>{test.error.message}</AlertDescription>
              </Alert>
            )}
            {test.data && <Outcome outcome={test.data} who={who.trim()} />}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

// headlineOf is what a tested call would get, in one line and a tone.
export function headlineOf(o: TestOutcome, who: string): { tone: string; title: string; text: string } {
  if (o.outcome === "denied") {
    return { tone: "danger", title: "Refused", text: `${o.denied_by ?? "A policy"}: “${o.reason ?? ""}” A refusal wins over every approval and allow.` };
  }
  if (o.outcome === "held") {
    return { tone: "warning", title: "Held for approval", text: o.message ?? `The call would not be sent. ${who || "The caller"} would be asked to request approval.` };
  }
  if (o.handlers.length === 0 && !o.transforms && !o.watches) {
    return { tone: "muted", title: "No handler applies", text: "The call runs as the persona allows." };
  }
  return { tone: "success", title: "Runs", text: `Every matching policy allows it.${afterText(o)}` };
}

// afterText is what follows a call that runs: the transforms and watches.
function afterText(o: TestOutcome): string {
  const after: string[] = [];
  if (o.transforms) after.push(`${o.transforms} transform${o.transforms === 1 ? "" : "s"} would change the result`);
  if (o.watches) after.push(`${o.watches} watch${o.watches === 1 ? "" : "es"} would queue a run`);
  return after.length > 0 ? ` ${after.join("; ")}.` : "";
}

// Outcome is what the tested call would get, then each handler it reached.
export function Outcome({ outcome: o, who }: { outcome: TestOutcome; who: string }) {
  const failed = o.handlers.some((h) => h.outcome === "error");
  const headline = headlineOf(o, who);
  const tones: Record<string, string> = {
    danger: "bg-red-100 text-red-800 dark:bg-red-500/10 dark:text-red-300",
    warning: "bg-amber-100 text-amber-800 dark:bg-amber-500/10 dark:text-amber-300",
    success: "bg-emerald-100 text-emerald-800 dark:bg-emerald-500/10 dark:text-emerald-300",
    muted: "bg-muted",
  };
  return (
    <div className="space-y-3" data-testid="test-outcome">
      <div className={`space-y-1 rounded-md p-3 text-sm ${tones[headline.tone]}`}>
        <p className="font-semibold">{headline.title}</p>
        <p className="break-words">{headline.text}</p>
        {failed && o.outcome !== "denied" && <p className="text-xs">A handler failed; its failure mode decided the call.</p>}
      </div>
      <ol className="divide-y text-sm">
        {o.handlers.map((h, i) => (
          <li key={`${h.id}-${i}`} className="grid grid-cols-[20px_minmax(0,1fr)_auto_52px] items-start gap-2 py-2">
            <span className="text-muted-foreground">{i + 1}</span>
            <div className="min-w-0">
              <p className="font-medium">
                {h.script} <span className="font-normal text-muted-foreground">{h.kind}</span>
              </p>
              {h.says && <p className="break-words text-muted-foreground">“{h.says}”</p>}
            </div>
            <Badge variant={STEP_WORDS[h.outcome]?.variant ?? "muted"}>{STEP_WORDS[h.outcome]?.label ?? h.outcome}</Badge>
            <span className="text-right text-muted-foreground">{h.ms} ms</span>
          </li>
        ))}
        {o.not_applying.map((h) => (
          <li key={h.id} className="py-2 text-muted-foreground">
            {h.script}: does not apply ({(h.operations ?? []).length > 0 ? `${(h.operations ?? []).join(", ")} only` : "another caller"})
          </li>
        ))}
      </ol>
      {o.transform_error && (
        <Alert variant="destructive">
          <AlertDescription>{o.transform_error}</AlertDescription>
        </Alert>
      )}
      {o.result !== undefined && (
        <div className="space-y-1">
          <p className="text-sm font-medium">The caller gets</p>
          <pre className="max-w-full overflow-x-auto whitespace-pre-wrap break-words rounded border p-2 font-mono text-xs">
            {JSON.stringify(o.result, null, 2)}
          </pre>
        </div>
      )}
    </div>
  );
}
