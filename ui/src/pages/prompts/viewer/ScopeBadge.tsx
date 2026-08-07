import { BookOpen, User, MessageSquare } from "lucide-react";
import { Badge } from "@/components/ui/badge";

// ScopeBadge renders the small pill placing a prompt in the user's two-bucket
// model (#1010): personal prompts are "Personal", approved shared prompts
// (global or persona scope) are "Library", system prompts are "System". The
// underlying scope taxonomy is an authoring/admin concept and is deliberately
// not shown here; it appears only in the promote and admin flows.

export type ScopeStyle = {
  label: string;
  icon: typeof User;
  variant: "info" | "muted" | "warning";
};

const scopeStyles: Record<string, ScopeStyle> = {
  global: { label: "Library", icon: BookOpen, variant: "info" },
  persona: { label: "Library", icon: BookOpen, variant: "info" },
  personal: { label: "Personal", icon: User, variant: "muted" },
  system: { label: "System", icon: MessageSquare, variant: "warning" },
};

const defaultScopeStyle: ScopeStyle = scopeStyles["personal"]!;

function getScopeStyle(scope: string): ScopeStyle {
  const match = scopeStyles[scope];
  return match !== undefined ? match : defaultScopeStyle;
}

export function ScopeBadge({ scope }: { scope: string }) {
  const cfg = getScopeStyle(scope);
  const Icon = cfg.icon;
  return (
    <Badge variant={cfg.variant}>
      <Icon />
      {cfg.label}
    </Badge>
  );
}
