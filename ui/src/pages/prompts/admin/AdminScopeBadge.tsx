import { Globe, MessageSquare, User, Users } from "lucide-react";
import { Badge } from "@/components/ui/badge";

// The admin view distinguishes all four scopes, so the palette is categorical
// (persona is purple, not a semantic state) and rides the outline variant
// rather than the semantic ones. The reader-facing ScopeBadge in viewer/ is a
// different, deliberately coarser taxonomy.

type ScopeStyle = { label: string; icon: typeof Globe; color: string };

const scopeStyles: Record<string, ScopeStyle> = {
  global: { label: "Global", icon: Globe, color: "text-blue-600 dark:text-blue-300" },
  persona: { label: "Persona", icon: Users, color: "text-purple-600 dark:text-purple-300" },
  personal: { label: "Personal", icon: User, color: "text-muted-foreground" },
  system: { label: "System", icon: MessageSquare, color: "text-amber-600 dark:text-amber-300" },
};

const defaultScopeStyle: ScopeStyle = scopeStyles["personal"]!;

function getScopeStyle(scope: string): ScopeStyle {
  const match = scopeStyles[scope];
  return match !== undefined ? match : defaultScopeStyle;
}

export function AdminScopeBadge({ scope }: { scope: string }) {
  const cfg = getScopeStyle(scope);
  const Icon = cfg.icon;
  return (
    <Badge variant="outline" className={cfg.color}>
      <Icon />
      {cfg.label}
    </Badge>
  );
}
