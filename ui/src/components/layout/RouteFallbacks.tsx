import { Compass, ShieldAlert } from "lucide-react";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

// The two answers the shell gives a path that produced no page, kept together
// because the difference between them is the whole point and is easy to blur.
//
// One is about the reader: the page exists and is not theirs. The other is
// about the address: there is no page there at all. Neither is
// `components/AccessDenied`, which is the whole-page refusal for an account
// that maps to no persona and never reaches the shell.

/**
 * AdminOnlyNotice is the defense-in-depth answer to an admin route reached by
 * a non-admin: the rail does not offer these routes, so this is only ever seen
 * on a typed URL or a stale link.
 *
 * It answers for every path under /admin, including ones no page is mounted
 * at. Telling a non-admin which administrator routes are real and which are
 * not is a small enumeration nobody needs from a refusal.
 */
export function AdminOnlyNotice() {
  return (
    <Alert variant="destructive" className="mx-auto max-w-md">
      <ShieldAlert />
      <AlertTitle>Access denied</AlertTitle>
      <AlertDescription>
        You need admin privileges to view this section. Ask an administrator to
        grant your account an admin role.
      </AlertDescription>
    </Alert>
  );
}

/**
 * PageNotFound is what a path with no page renders (#1359).
 *
 * It exists because the alternative is not an error, it is a false statement:
 * a shell that matched no page renders its chrome around an empty content
 * area, which reads as "you have none of these" rather than "there is nothing
 * here". The path is named because a reader who guessed it or was sent a stale
 * link needs to see which one failed, and the way out is offered because a
 * dead end with no exit is the second half of the same problem.
 */
export function PageNotFound({
  route,
  onNavigate,
}: {
  route: string;
  onNavigate: (path: string) => void;
}) {
  return (
    <EmptyState
      icon={Compass}
      className="mx-auto max-w-md"
      action={
        <Button type="button" variant="secondary" size="sm" onClick={() => onNavigate("/")}>
          Go to your assets
        </Button>
      }
    >
      <p className="text-foreground">There is no page at this address.</p>
      <p className="mt-1 break-all font-mono text-xs">{route}</p>
      <p className="mt-2">
        The link may be out of date, or the address may have a typo in it. Nothing was
        looked up, so this says nothing about what you have.
      </p>
    </EmptyState>
  );
}
