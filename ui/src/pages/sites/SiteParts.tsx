import { Building2, ExternalLink, Globe, Lock, User, Users } from "lucide-react";
import type { SiteEnvironment, SiteGrant } from "@/api/portal/hooks/sites";
import { ExternalBadge } from "@/components/ExternalBadge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";

// The small pieces the site pages share (#2604): an environment's access badge,
// an address link, a refusal, and the chips naming who an environment is
// shared with.

/** AccessBadge says whether an environment is private or public, in words and an icon. */
export function AccessBadge({ env }: { env: Pick<SiteEnvironment, "public"> }) {
  return env.public ? (
    <Badge variant="info">
      <Globe aria-hidden />
      Public
    </Badge>
  ) : (
    <Badge variant="muted">
      <Lock aria-hidden />
      Private
    </Badge>
  );
}

export function AddressLink({ href, text }: { href: string; text: string }) {
  return (
    <a className="inline-flex min-w-0 items-center gap-1 break-all font-mono text-sm text-primary hover:underline" href={href} target="_blank" rel="noreferrer" title="Open in a new tab">
      {text}
      <ExternalLink className="h-3 w-3 shrink-0" aria-hidden />
    </a>
  );
}

export function Failure({ text, testId }: { text: string | null; testId?: string }) {
  if (!text) return null;
  return (
    <Alert variant="destructive" data-testid={testId}>
      <AlertDescription>{text}</AlertDescription>
    </Alert>
  );
}

/** The everyone-internal entry an environment's chips and the Access table show. */
export const EVERYONE_INTERNAL = "Everyone internal";

/** Who a grant names, for a chip or a table row. */
export function grantName(g: SiteGrant): string {
  return g.persona || g.email;
}

/** Whether a view grant opens env: a grant naming no environments opens every private one. */
export function grantOpens(g: SiteGrant, env: string): boolean {
  return g.level === "view" && (g.environments.length === 0 || g.environments.includes(env));
}

export function ShareChip({ label, kind, external }: { label: string; kind: "person" | "persona" | "internal"; external?: boolean }) {
  const Icon = kind === "persona" ? Users : kind === "internal" ? Building2 : User;
  return (
    <span className="inline-flex max-w-full items-center gap-1.5 rounded-full border bg-card px-2.5 py-0.5 text-xs">
      <Icon className="h-3 w-3 shrink-0" aria-hidden />
      <span className="break-all">{label}</span>
      {external && <ExternalBadge />}
    </span>
  );
}
