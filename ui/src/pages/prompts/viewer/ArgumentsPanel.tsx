import { Braces } from "lucide-react";
import type { Prompt } from "@/api/admin/types";
import { cn } from "@/lib/utils";

// ArgumentsPanel renders the read-only summary table of a prompt's declared
// arguments (name, required/optional badge, description). Extracted verbatim
// from PromptViewerPage.tsx (#819).

export function ArgumentsPanel({ args }: { args: Prompt["arguments"] }) {
  if (!args || args.length === 0) return null;
  const required = args.filter((a) => a.required).length;
  const optional = args.length - required;
  return (
    <div className="rounded-lg border bg-card overflow-hidden">
      <div className="flex items-center justify-between border-b bg-muted/40 px-3 py-2">
        <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <Braces className="h-3.5 w-3.5" />
          <span>Arguments ({args.length})</span>
        </div>
        <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
          <span>{required} required</span>
          <span className="opacity-50">·</span>
          <span>{optional} optional</span>
        </div>
      </div>
      <ul className="divide-y">
        {args.map((a) => (
          <li key={a.name} className="px-3 py-2 grid grid-cols-1 md:grid-cols-[minmax(0,1fr)_2fr] gap-x-4 gap-y-1 items-baseline">
            <div className="flex items-center gap-2 flex-wrap">
              <code className="text-xs font-mono text-foreground bg-muted/60 rounded px-1.5 py-0.5 break-all">
                {`{{${a.name}}}`}
              </code>
              <span
                className={cn(
                  "inline-flex items-center rounded-full border px-1.5 py-0 text-[10px] font-medium uppercase tracking-wide",
                  a.required
                    ? "bg-rose-500/10 text-rose-400 border-rose-500/20"
                    : "bg-zinc-500/10 text-zinc-400 border-zinc-500/20",
                )}
              >
                {a.required ? "required" : "optional"}
              </span>
            </div>
            <div className="text-xs text-muted-foreground break-words">
              {a.description || <span className="italic opacity-60">No description</span>}
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
