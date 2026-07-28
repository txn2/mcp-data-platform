import { useAuthStore } from "@/stores/auth";
import { useResolvedDark } from "@/stores/theme";
import { useBranding } from "@/api/portal/hooks";
import { resolvePortalLogo } from "@/lib/portalLogo";
import { ShieldOff } from "lucide-react";

const DEFAULT_PORTAL_TITLE = "MCP Data Platform";

/**
 * Shown when the caller authenticated but their roles map to no persona, which
 * the portal API reports as 403 from /me.
 *
 * This is deliberately not the sign-in form. Signing in again is exactly the
 * wrong remedy: the identity provider already accepted this account and would
 * return them straight back here. What they need is the name of the account
 * that was refused, so they can tell an administrator which one to grant, and a
 * way to leave it for a different account.
 */
export function AccessDenied() {
  const deniedEmail = useAuthStore((s) => s.deniedEmail);
  const logout = useAuthStore((s) => s.logout);
  const { data: branding } = useBranding();
  const isDark = useResolvedDark();

  const portalLogo = resolvePortalLogo(branding ?? undefined, isDark);
  const portalTitle = branding?.portal_title || DEFAULT_PORTAL_TITLE;

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/40">
      <div className="w-full max-w-sm rounded-lg border bg-card p-6 shadow-sm">
        <div className="mb-5 flex items-start gap-3">
          <img
            src={portalLogo}
            alt=""
            className="h-10 w-10 shrink-0"
            onError={(e) => {
              (e.target as HTMLImageElement).style.display = "none";
            }}
          />
          <div className="min-w-0">
            <h1 className="text-xl font-semibold leading-tight">{portalTitle}</h1>
            <p className="mt-1 text-sm text-muted-foreground">You do not have access yet.</p>
          </div>
        </div>

        <div className="mb-4 flex gap-2 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-700 dark:bg-amber-950 dark:text-amber-300">
          <ShieldOff className="mt-0.5 h-4 w-4 shrink-0" />
          <span>
            Your account is not assigned to a persona. Ask an administrator to grant your account
            access.
          </span>
        </div>

        {deniedEmail && (
          <p className="mb-4 break-all rounded-md border px-3 py-2 text-sm">
            <span className="text-muted-foreground">Signed in as</span> {deniedEmail}
          </p>
        )}

        <button
          type="button"
          onClick={logout}
          className="w-full rounded-md border bg-secondary px-4 py-2 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
        >
          Sign out and switch accounts
        </button>

        <p className="mt-3 text-xs text-muted-foreground">
          Access is granted by role. Once an administrator assigns your account a role that a
          persona lists, this page becomes the portal.
        </p>
      </div>
    </div>
  );
}
