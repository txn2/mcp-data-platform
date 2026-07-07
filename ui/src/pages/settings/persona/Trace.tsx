import { Check, Ban } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Resolution, TraceStep } from "./resolve";
import { renderPattern } from "./renderPattern";

// Trace renders the step-by-step allow/deny resolution for the focused item in
// the explorer's right rail: deny bucket first, then allow, then the final
// verdict. Extracted from PersonaEditor.tsx (#766).
export function Trace({
  name,
  meta,
  resolution,
  hasAllow,
  hasDeny,
}: {
  name: string;
  meta?: { secondary: string; tertiary: string } | null;
  resolution: Resolution;
  hasAllow: boolean;
  hasDeny: boolean;
}) {
  const result = resolution.decision;
  return (
    <div>
      <div className="mb-3">
        <div className="font-mono text-xs font-semibold break-all">{name}</div>
        {meta && (
          <div className="mt-0.5 text-[10px] text-muted-foreground">
            {meta.secondary} · {meta.tertiary}
          </div>
        )}
      </div>

      <div className="space-y-2">
        <TraceBucket
          label="1. Deny patterns"
          empty="no deny patterns"
          steps={resolution.steps.filter((s) => s.bucket === "deny")}
          present={hasDeny}
        />
        <TraceBucket
          label="2. Allow patterns"
          empty="no allow patterns"
          steps={resolution.steps.filter((s) => s.bucket === "allow")}
          present={hasAllow}
          dim={result === "deny"}
        />
      </div>

      <div
        className={cn(
          "mt-3 flex items-center gap-2 rounded-md border px-3 py-2 font-mono text-[11px]",
          result === "allow"
            ? "border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-300"
            : "border-rose-200 bg-rose-50 text-rose-800 dark:border-rose-900 dark:bg-rose-950/30 dark:text-rose-300",
        )}
      >
        {result === "allow" ? (
          <Check className="h-4 w-4" />
        ) : (
          <Ban className="h-4 w-4" />
        )}
        <span>
          {result === "allow"
            ? `ALLOWED via ${resolution.matchedPattern}`
            : result === "deny"
              ? `DENIED via ${resolution.matchedPattern}`
              : "DENIED: no allow pattern matched"}
        </span>
      </div>
    </div>
  );
}

function TraceBucket({
  label,
  empty,
  steps,
  present,
  dim,
}: {
  label: string;
  empty: string;
  steps: TraceStep[];
  present: boolean;
  dim?: boolean;
}) {
  return (
    <div className={dim ? "opacity-50" : ""}>
      <div className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      {!present || steps.length === 0 ? (
        <div className="pl-2 text-[10px] italic text-muted-foreground">
          {empty}
        </div>
      ) : (
        <ul className="space-y-0.5">
          {steps.map((s, idx) => (
            <li
              key={idx}
              className={cn(
                "flex items-center gap-1.5 rounded py-0.5 pl-2 pr-1.5 font-mono text-[10px]",
                s.decisive
                  ? s.bucket === "allow"
                    ? "bg-emerald-100 text-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300"
                    : "bg-rose-100 text-rose-900 dark:bg-rose-950/40 dark:text-rose-300"
                  : s.matched
                    ? "text-foreground"
                    : "text-muted-foreground",
              )}
            >
              <span className="text-muted-foreground">
                {s.matched ? "▸" : "·"}
              </span>
              <span className="flex-1">{renderPattern(s.pattern)}</span>
              {s.decisive && (
                <span className="text-[8px] uppercase tracking-wider">
                  final
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
