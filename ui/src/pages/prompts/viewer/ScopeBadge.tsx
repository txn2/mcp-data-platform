import { Globe, Users, User, MessageSquare } from "lucide-react";
import { cn } from "@/lib/utils";

// ScopeBadge renders the small pill indicating a prompt's scope (global,
// persona, personal, system). Extracted verbatim from PromptViewerPage.tsx
// (#819).

export type ScopeStyle = { label: string; icon: typeof Globe; color: string };

const scopeStyles: Record<string, ScopeStyle> = {
  global: { label: "Global", icon: Globe, color: "bg-blue-500/10 text-blue-400 border-blue-500/20" },
  persona: { label: "Persona", icon: Users, color: "bg-purple-500/10 text-purple-400 border-purple-500/20" },
  personal: { label: "Personal", icon: User, color: "bg-zinc-500/10 text-zinc-400 border-zinc-500/20" },
  system: { label: "System", icon: MessageSquare, color: "bg-amber-500/10 text-amber-400 border-amber-500/20" },
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
    <span className={cn("inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap", cfg.color)}>
      <Icon className="h-3 w-3" />
      {cfg.label}
    </span>
  );
}
