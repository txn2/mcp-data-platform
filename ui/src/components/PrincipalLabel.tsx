import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { formatUser, parsePrincipal, principalDetail } from "@/lib/formatUser";

// PrincipalLabel is the one on-screen rendering of who made a call, shared by
// the events, calls and sessions tables. A person reads as their address; an
// automation carries its kind as a badge so a script's row is never mistaken
// for its owner sitting at a keyboard, and names that owner after it (#1523).
// The full line is the hover title, because these columns are narrow enough to
// truncate the address away.
export function PrincipalLabel({
  userId,
  email,
  className,
}: {
  userId: string;
  email?: string;
  className?: string;
}) {
  const principal = parsePrincipal(userId, email);
  const full = formatUser(userId, email);

  // The columns this sits in are capped and the label is longer than the
  // address it replaces, so both arms clip themselves: an inline span inside a
  // capped cell overflows into the next column rather than truncating.
  if (principal.kind === "user") {
    return (
      <span className={cn("block truncate", className)} title={full}>
        {full}
      </span>
    );
  }

  return (
    <span className={cn("flex items-center gap-1.5 overflow-hidden", className)} title={full}>
      <Badge variant="muted" className="shrink-0 px-1.5 py-0 text-[10px] tracking-wide uppercase">
        {principal.kind}
      </Badge>
      <span className="min-w-0 truncate">{principalDetail(principal)}</span>
    </span>
  );
}
