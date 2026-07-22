import { useCallback, useState } from "react";
import { Check, Copy, Terminal } from "lucide-react";
import type { Prompt } from "@/api/admin/types";

// InvocationHelp shows how to run this prompt from a chat client (#1010):
// the stable bare name (#1008) and a copyable natural-language invocation.
// Point-of-use help so the portal stands alone without the client's prompt
// picker.
export function InvocationHelp({ prompt }: { prompt: Prompt }) {
  const [copied, setCopied] = useState(false);

  const requiredArgs = (prompt.arguments ?? []).filter((a) => a.required);
  const argHint = requiredArgs.length > 0
    ? ` with ${requiredArgs.map((a) => `${a.name} <${a.name}>`).join(", ")}`
    : "";
  const invocation = `Run the ${prompt.name} prompt${argHint}`;

  const copy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(invocation);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // best-effort
    }
  }, [invocation]);

  return (
    <div className="rounded-lg border bg-card p-4 space-y-2">
      <div className="flex items-center gap-2 text-sm font-semibold">
        <Terminal className="h-4 w-4 text-muted-foreground" />
        Run from chat
      </div>
      <p className="text-xs text-muted-foreground">
        Ask your agent by name; it resolves prompts against this library:
      </p>
      <div className="flex items-center gap-2">
        <code className="flex-1 rounded-md border bg-muted/40 px-3 py-2 text-xs font-mono break-all">
          {invocation}
        </code>
        <button
          onClick={copy}
          className="inline-flex items-center gap-1.5 rounded-md border px-3 py-2 text-xs font-medium hover:bg-accent whitespace-nowrap"
          title="Copy invocation"
        >
          {copied ? <Check className="h-3.5 w-3.5 text-green-500" /> : <Copy className="h-3.5 w-3.5" />}
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      {requiredArgs.length > 0 && (
        <p className="text-[11px] text-muted-foreground">
          Replace each <code className="font-mono">&lt;value&gt;</code> with your input; optional
          arguments can be added the same way.
        </p>
      )}
    </div>
  );
}
