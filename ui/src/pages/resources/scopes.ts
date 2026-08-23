import type { UserProfile } from "@/stores/auth";

/** A resource library, named the way the create API names it. */
export interface ScopeTarget {
  scope: "user" | "persona" | "global";
  scope_id: string;
}

// The role substring that marks a persona-admin grant. Roles reach the portal
// with whatever prefix the identity provider applies ("dp_persona-admin:ops"),
// so the name is cut out of the role rather than matched against it — the same
// reading resource.PersonaAdminRoles performs server-side
// (pkg/resource/permission.go).
const PERSONA_ADMIN_INFIX = "persona-admin:";

/** personaAdminNames lists the personas a role set grants admin authority over. */
export function personaAdminNames(roles: string[]): string[] {
  const names: string[] = [];
  for (const role of roles) {
    const at = role.indexOf(PERSONA_ADMIN_INFIX);
    if (at === -1) continue;
    const name = role.slice(at + PERSONA_ADMIN_INFIX.length);
    if (name) names.push(name);
  }
  return names;
}

// Mirrors isPlatformAdmin in pkg/resource/permission.go: the persona-resolved
// admin flag the server sends on /me, or either unprefixed platform role.
function isPlatformAdmin(user: UserProfile): boolean {
  const roles = user.roles ?? [];
  return user.is_admin || roles.includes("admin") || roles.includes("platform-admin");
}

/**
 * targetForTab names the library a scope tab is showing. The admin "all" tab
 * spans every library and names none, so it resolves to null.
 */
export function targetForTab(tab: string, user: UserProfile | null): ScopeTarget | null {
  if (tab === "all") return null;
  if (tab === "global") return { scope: "global", scope_id: "" };
  if (tab === "user") return { scope: "user", scope_id: user?.user_id ?? "" };
  return { scope: "persona", scope_id: tab };
}

/**
 * canWriteScope answers, for the browser, the question CanWriteScope answers
 * for the request (pkg/resource/permission.go): may this caller add to this
 * library? Deriving the Upload control from the same rule is what keeps the
 * portal from offering an upload the server will refuse — or, worse, silently
 * redirect into the caller's own library.
 *
 * A null target is the admin "all" tab, which names no single library; the
 * dialog's own scope picker chooses there.
 */
export function canWriteScope(user: UserProfile | null, target: ScopeTarget | null): boolean {
  if (!user) return false;
  if (isPlatformAdmin(user)) return true;
  if (!target) return false;
  switch (target.scope) {
    case "user":
      return target.scope_id !== "" && target.scope_id === user.user_id;
    case "persona":
      return personaAdminNames(user.roles ?? []).includes(target.scope_id);
    case "global":
      return false;
  }
}

/** How one library is described to the person looking at it. */
export interface LibraryCopy {
  /** The library's name, as the destination line states it. */
  name: string;
  /** Who can see a file added to it. */
  audience: string;
  /** Where its material comes from, for a library the caller cannot add to. */
  source: string;
}

/** libraryCopy states what a library is, for the destination line and for the
 * read-only note that stands in for the Upload control. */
export function libraryCopy(target: ScopeTarget | null): LibraryCopy {
  if (target?.scope === "global") {
    return {
      name: "Global",
      audience: "Everyone signed in can see it.",
      source: "Published by platform administrators for everyone signed in.",
    };
  }
  if (target?.scope === "persona") {
    return {
      name: `${target.scope_id} persona`,
      audience: `Everyone in the ${target.scope_id} persona can see it.`,
      source: `Published by the ${target.scope_id} persona's administrators.`,
    };
  }
  return {
    name: "My Resources",
    audience: "Only you can see it.",
    source: "Only you can add to your own library.",
  };
}
