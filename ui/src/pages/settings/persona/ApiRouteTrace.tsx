import { Ban, Check, Minus } from "lucide-react";
import { cn } from "@/lib/utils";
import type { APIRouteRule } from "@/api/admin/types";
import { renderPattern } from "./renderPattern";
import { BUCKET_TINT } from "./tints";
import type { RouteResolution } from "./apiRoutes";

// The API-endpoint scope's answer to "why is this operation reachable?".
// It reads the same three outcomes the list marks, and names the rule that
// produced the decision, so an operator can see which of several rules is the
// one in force (#1479).

const OUTCOME = {
  allow: {
    icon: Check,
    tint: BUCKET_TINT.allow,
    headline: "Reachable",
    detail: "An allow rule matches this operation.",
  },
  deny: {
    icon: Ban,
    tint: BUCKET_TINT.deny,
    headline: "Denied",
    detail: "A deny rule matches this operation.",
  },
  open: {
    icon: Minus,
    tint: null,
    headline: "Reachable",
    detail:
      "No rule names this connection, so the connection grant is the only gate. Every operation on it is reachable.",
  },
} as const;

export function ApiRouteTrace({
  connection,
  method,
  path,
  resolution,
}: {
  connection: string;
  method: string;
  path: string;
  resolution: RouteResolution;
}) {
  const outcome = OUTCOME[resolution.decision];
  const Icon = outcome.icon;

  return (
    <div className="space-y-2">
      <div className="space-y-0.5">
        <div className="font-mono text-[11px] text-muted-foreground">{connection}</div>
        <div className="break-all font-mono text-[11px]">
          {method.toUpperCase()} {path}
        </div>
      </div>

      <div className="flex items-center gap-1.5">
        <Icon
          className={cn("size-3.5 shrink-0", outcome.tint?.icon ?? "text-muted-foreground")}
        />
        <span
          className={cn(
            "text-[11px] font-semibold",
            outcome.tint?.text ?? "text-muted-foreground",
          )}
        >
          {outcome.headline}
        </span>
      </div>

      <p className="text-[11px] text-muted-foreground">
        {/* A denial with no rule to point at is the set's doing, not one
            rule's, and saying "a deny rule matches" there would send the
            operator looking for a rule that does not exist. */}
        {resolution.decision === "deny" && !resolution.rule
          ? "Rules name this connection, but none of them admits this operation. Once any rule names a connection, an operation must match an allow rule to be reachable."
          : outcome.detail}
      </p>

      {resolution.rule && <MatchedRule rule={resolution.rule} />}
    </div>
  );
}

function MatchedRule({ rule }: { rule: APIRouteRule }) {
  const tint = rule.action === "deny" ? BUCKET_TINT.deny : BUCKET_TINT.allow;
  return (
    <div className={cn("space-y-0.5 rounded border px-2 py-1.5", tint.border)}>
      <div className="text-[10px] uppercase tracking-wider text-muted-foreground">
        Matched rule
      </div>
      <div className="font-mono text-[11px]">{renderPattern(rule.connection)}</div>
      <div className="font-mono text-[10px] text-muted-foreground">
        {rule.methods?.length ? rule.methods.join(" ") : "any method"}
      </div>
      <div className="break-all font-mono text-[10px] text-muted-foreground">
        {rule.paths?.length
          ? rule.paths.map((p, i) => (
              <span key={p}>
                {i > 0 && " "}
                {renderPattern(p)}
              </span>
            ))
          : "any path"}
      </div>
    </div>
  );
}
