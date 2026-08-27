import type { UserProfile } from "@/stores/auth";
import { scopeLabel } from "./shared";

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

/**
 * One library a resource may be moved into, and how the picker names it.
 *
 * PERSON_TARGET is the administrator's open-ended option: a named person's
 * library, addressed by email rather than chosen from a list, because the
 * platform has no roster of every user's library to enumerate.
 */
export interface MoveTarget extends ScopeTarget {
  label: string;
}

/** The select value that stands for "a named person's library". */
export const PERSON_TARGET = "__person__";

/**
 * moveTargets lists the libraries this caller may move a resource into, in the
 * order the picker offers them.
 *
 * It mirrors CanMoveToLibrary (pkg/resource/permission.go), which is looser than
 * CanWriteScope on exactly one arm: a persona you BELONG to accepts a file you
 * already own, while uploading into it still takes that persona's admin role. So
 * the reader's own resolved persona appears here and does not appear on the
 * Upload control.
 *
 * The platform-admin override is withheld on the portal surface, the same way
 * canWriteScope withholds it: these are the libraries a reader can see, and
 * publishing somebody's file to everyone signed in should not sit inside the
 * Edit dialog of a page they reached by browsing. The administrator moves it
 * from Admin > Resources, where every target is offered.
 *
 * personaNames is the deployment's full persona list, which only the
 * administrator's section fetches; the portal surface passes none and derives
 * its personas from the caller's own claims.
 */
export function moveTargets(
  user: UserProfile | null,
  personaNames: string[],
  surface: Surface = "admin",
): MoveTarget[] {
  if (!user) return [];
  const admin = surface === "admin" && isPlatformAdmin(user);
  const targets: MoveTarget[] = [];

  if (user.user_id) {
    targets.push({
      scope: "user",
      scope_id: user.user_id,
      label: "My Resources",
    });
  }

  // Deduplicated because a persona-admin of the persona they belong to would
  // otherwise be offered it twice.
  const personas = admin
    ? personaNames
    : [
        ...new Set(
          [user.persona, ...personaAdminNames(user.roles ?? [])].filter(
            Boolean,
          ),
        ),
      ];
  for (const name of personas as string[]) {
    targets.push({
      scope: "persona",
      scope_id: name,
      label: `${name} persona`,
    });
  }

  if (admin) {
    targets.push({ scope: "global", scope_id: "", label: "Global" });
    targets.push({
      scope: "user",
      scope_id: PERSON_TARGET,
      label: "A person's library...",
    });
  }
  return targets;
}

/**
 * targetKey identifies a library as one string, so a select can hold it in a
 * single value. The scope alone is not enough (two personas differ only by id)
 * and the id alone is not either (a persona and a person could share a name).
 */
export function targetKey(t: ScopeTarget): string {
  return `${t.scope}:${t.scope_id}`;
}

/**
 * currentLibrary names the library a resource is in now, as a picker option.
 *
 * It is always offered even when the caller could not move the resource there,
 * because leaving the file where it is has to be expressible.
 *
 * A user library that is not the caller's own is named by the address it is
 * keyed on when that is an address; a scope id that is a subject identifier is
 * described rather than printed, because a raw UUID names nobody to the person
 * reading it.
 */
export function currentLibrary(target: ScopeTarget, user: UserProfile | null): MoveTarget {
  if (target.scope === "persona") return { ...target, label: `${target.scope_id} persona` };
  return { ...target, label: scopeLabel(target.scope, target.scope_id, user) };
}

/**
 * libraryOptions is what the Library picker shows: the libraries this caller may
 * move to, with the library the resource is in now first and never duplicated.
 *
 * An empty result means there is nowhere to move this resource, and the field is
 * not shown at all -- a picker whose only entry is where the file already sits
 * is a control that cannot do anything.
 */
export function libraryOptions(
  target: ScopeTarget,
  user: UserProfile | null,
  personaNames: string[],
  surface: Surface = "admin",
): MoveTarget[] {
  const current = currentLibrary(target, user);
  const rest = moveTargets(user, personaNames, surface).filter(
    (t) => targetKey(t) !== targetKey(current),
  );
  if (rest.length === 0) return [];
  return [current, ...rest];
}
