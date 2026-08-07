import { Check, Ban } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { cn } from "@/lib/utils";
import type { Resolution, TraceStep } from "./resolve";
import { renderPattern } from "./renderPattern";
import { BUCKET_TINT } from "./tints";

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
  const allowed = result === "allow";
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

      {/* The verdict is the trace's conclusion, so it is an Alert rather than
          another tinted step: it states an outcome, not a comparison. */}
      <Alert
        variant={allowed ? "success" : "destructive"}
        className="mt-3 px-3 py-2 font-mono text-[11px]"
      >
        {allowed ? <Check /> : <Ban />}
        <AlertDescription className="text-[11px]">
          {allowed
            ? `ALLOWED via ${resolution.matchedPattern}`
            : result === "deny"
              ? `DENIED via ${resolution.matchedPattern}`
              : "DENIED: no allow pattern matched"}
        </AlertDescription>
      </Alert>
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
        <div className="pl-2 text-[10px] italic text-muted-foreground">{empty}</div>
      ) : (
        <ul className="space-y-0.5">
          {steps.map((s, idx) => (
            <li
              key={idx}
              className={cn(
                "flex items-center gap-1.5 rounded py-0.5 pl-2 pr-1.5 font-mono text-[10px]",
                s.decisive
                  ? BUCKET_TINT[s.bucket].step
                  : s.matched
                    ? "text-foreground"
                    : "text-muted-foreground",
              )}
            >
              <span className="text-muted-foreground">{s.matched ? "▸" : "·"}</span>
              <span className="flex-1">{renderPattern(s.pattern)}</span>
              {s.decisive && (
                <span className="text-[8px] uppercase tracking-wider">final</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
