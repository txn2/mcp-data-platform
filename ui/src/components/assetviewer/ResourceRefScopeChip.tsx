import { Badge } from "@/components/ui/badge";

const SCOPE_VARIANTS: Record<string, "success" | "info" | "warning"> = {
  global: "success",
  persona: "info",
  user: "warning",
};

// ScopeChip shows how widely a managed file is visible on its own, which is a
// different question from how widely an asset referencing it makes it visible.
//
// It sits in its own module because the reference panel and the picker that
// feeds it both render one, and the panel is at the file-length ceiling: a
// shared chip imported by both beats either file importing the other.
export function ScopeChip({ scope, scopeId }: { scope?: string; scopeId?: string }) {
  if (!scope) return null;
  const label = scope === "persona" && scopeId ? `persona: ${scopeId}` : scope;
  return (
    <Badge variant={SCOPE_VARIANTS[scope] ?? "muted"} className="text-[11px]">
      {label}
    </Badge>
  );
}
