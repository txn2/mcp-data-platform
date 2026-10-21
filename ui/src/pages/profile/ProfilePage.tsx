import { LogOut, UserRound } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { SettingsCard } from "@/pages/settings/panels";
import { initials } from "@/lib/initials";
import { useAuthStore } from "@/stores/auth";

/**
 * signInMethod names how this session was signed in, from how the portal
 * authenticated it: a browser session from single sign-on, or an API key.
 */
function signInMethod(method: "cookie" | "apikey" | null): string {
  if (method === "cookie") return "Single sign-on";
  if (method === "apikey") return "API key";
  return "Unknown";
}

/**
 * ProfilePage says who the portal has signed in (#2215): the account, its
 * persona and roles, whether it administers the platform, and how it signed
 * in. It reads the /me payload the app already holds and changes nothing; no
 * token or key value is shown.
 */
export function ProfilePage() {
  const user = useAuthStore((s) => s.user);
  const authMethod = useAuthStore((s) => s.authMethod);
  const logout = useAuthStore((s) => s.logout);
  if (!user) return null;
  const identity = user.email || user.user_id;

  return (
    <div className="max-w-2xl space-y-4">
      <SettingsCard icon={UserRound} title="Profile" description="The account this portal session is signed in as.">
        <div className="flex items-center gap-3 border-b px-4 py-3">
          <span
            aria-hidden
            className="flex size-10 items-center justify-center rounded-full bg-primary/15 text-sm font-semibold text-primary"
          >
            {initials(identity)}
          </span>
          <span className="min-w-0 truncate font-medium">{identity}</span>
        </div>
        <dl className="grid grid-cols-[10rem_1fr] gap-x-4 gap-y-3 px-4 py-4 text-sm">
          <dt className="text-muted-foreground">Email</dt>
          <dd className="min-w-0 break-all">{user.email || "None"}</dd>
          <dt className="text-muted-foreground">User ID</dt>
          <dd className="min-w-0 break-all font-mono text-xs leading-5">{user.user_id}</dd>
          <dt className="text-muted-foreground">Persona</dt>
          <dd>{user.persona || "None"}</dd>
          <dt className="text-muted-foreground">Roles</dt>
          <dd className="flex flex-wrap gap-1">
            {user.roles.length > 0
              ? user.roles.map((r) => (
                  <Badge key={r} variant="secondary">
                    {r}
                  </Badge>
                ))
              : "None"}
          </dd>
          <dt className="text-muted-foreground">Administrator</dt>
          <dd>{user.is_admin ? "Yes" : "No"}</dd>
          <dt className="text-muted-foreground">Signed in with</dt>
          <dd>{signInMethod(authMethod)}</dd>
        </dl>
        <div className="flex justify-end border-t px-4 py-3">
          <Button variant="outline" size="sm" onClick={logout}>
            <LogOut />
            Sign Out
          </Button>
        </div>
      </SettingsCard>
    </div>
  );
}
