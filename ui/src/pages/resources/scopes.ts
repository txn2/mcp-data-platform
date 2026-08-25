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
 * Which Resources surface is asking.
 *
 * "admin" is the administrator's section, where the platform-admin override
 * applies and every library is writable. "portal" is the reader's own
 * Resources page, where it does not: a platform admin reading their own portal
 * is offered Upload on their own library alone, and adds to a persona's or the
 * global library from Admin > Resources instead.
 *
 * The two surfaces differ because the portal's tabs are the libraries a reader
 * can SEE, and a reader browsing their own material is not administering the
 * platform. Offering the same Upload on every one of those tabs put publishing
 * to everyone signed in one click away from browsing, with nothing on screen
 * marking the difference.
 *
 * This narrows no authority: the same admin adds to the same libraries on
 * Admin > Resources, which is where adminReachNote points them.
 */
export type Surface = "portal" | "admin";

/**
 * canWriteScope answers, for the browser, the question CanWriteScope answers
 * for the request (pkg/resource/permission.go): may this caller add to this
 * library, on the surface they are asking from? Deriving the Upload control
 * from the same rule is what keeps the portal from offering an upload the
 * server will refuse — or, worse, silently redirect into the caller's own
 * library.
 *
 * A null target is the admin "all" tab, which names no single library; the
 * dialog's own scope picker chooses there.
 */
export function canWriteScope(
  user: UserProfile | null,
  target: ScopeTarget | null,
  surface: Surface = "admin",
): boolean {
  if (!user) return false;
  if (surface === "admin" && isPlatformAdmin(user)) return true;
  if (!target) return false;
  return holdsScope(user, target);
}

/**
 * holdsScope is the authority a caller has over one library as themselves,
 * with no platform-admin override: their own library, and a persona they carry
 * the admin role for. Global is nobody's by this rule — reaching it is the
 * override, which only the administrator's section applies.
 */
function holdsScope(user: UserProfile, target: ScopeTarget): boolean {
  switch (target.scope) {
    case "user":
      return target.scope_id !== "" && target.scope_id === user.user_id;
    case "persona":
      return personaAdminNames(user.roles ?? []).includes(target.scope_id);
    case "global":
      return false;
  }
}

/**
 * adminReachNote is the sentence that follows the read-only note for a caller
 * whose platform-admin authority WOULD let them add here, on a surface that
 * does not offer it. Empty for everyone else.
 *
 * A control withheld from someone who holds the authority has to say where the
 * authority is exercised, or it reads as the platform having lost track of who
 * they are.
 */
export function adminReachNote(user: UserProfile | null, surface: Surface): string {
  if (surface !== "portal" || !user || !isPlatformAdmin(user)) return "";
  return "Add to it from Admin > Resources.";
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
