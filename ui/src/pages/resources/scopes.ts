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

/**
 * isPlatformAdmin mirrors the function of the same name in
 * pkg/resource/permission.go: the persona-resolved admin flag the server sends
 * on /me, or either unprefixed platform role.
 *
 * Exported because it is also what decides whether a page fetches the
 * deployment's persona list: the libraries an administrator may move a
 * resource into include every persona, which the caller's own claims do not
 * name.
 */
export function isPlatformAdmin(user: UserProfile | null): boolean {
  if (!user) return false;
  const roles = user.roles ?? [];
  return user.is_admin || roles.includes("admin") || roles.includes("platform-admin");
}

/** One entry in the library picker: the key a view is addressed by, and its name. */
export interface LibraryChoice {
  key: string;
  label: string;
}

/** The key of the unnarrowed library, which is what a page opens on. */
export const ALL_LIBRARIES = "all";

/**
 * personasFor is the personas a caller sees a library for: the one they are
 * resolved to, the ones they administer, and -- for a platform administrator --
 * every persona the deployment defines.
 *
 * An administrator's list is the whole deployment because their authority is
 * the whole deployment: CanWriteScope lets them upload into any persona and
 * ListScopes lets them list any persona, so a picker built from membership
 * alone would hide libraries they own material in (#1553).
 */
function personasFor(user: UserProfile | null, personaNames: string[]): string[] {
  if (!user) return [];
  const mine = [user.persona, ...personaAdminNames(user.roles ?? [])].filter(Boolean) as string[];
  const all = isPlatformAdmin(user) ? [...personaNames, ...mine] : mine;
  return [...new Set(all)].sort((a, b) => a.localeCompare(b));
}

/**
 * libraryChoices is what the library picker offers, in the order it offers
 * them: everything the caller can reach, their own, each persona, then the
 * global one.
 *
 * All heads the list and is where a page opens. A reader's libraries are few
 * and mostly full of other people's material, so the useful first view is all
 * of it at once; narrowing to one is the deliberate act.
 */
export function libraryChoices(
  user: UserProfile | null,
  personaNames: string[],
): LibraryChoice[] {
  return [
    { key: ALL_LIBRARIES, label: "All" },
    { key: "user", label: "Mine" },
    ...personasFor(user, personaNames).map((name) => ({ key: name, label: name })),
    { key: "global", label: "Global" },
  ];
}

/**
 * uploadTargets lists the libraries this caller may add a NEW file to, which is
 * CanWriteScope's answer rather than CanMoveToLibrary's: uploading into a
 * persona takes that persona's admin role, while moving a file you already own
 * into a persona you belong to does not (see moveTargets).
 *
 * It is what the upload dialog offers when the view names no single library --
 * the All view, which every page now opens on. Without it the Upload control
 * would be admin-only there, and an ordinary reader would have to narrow to
 * their own library before they could add anything to it.
 */
export function uploadTargets(
  user: UserProfile | null,
  personaNames: string[],
): MoveTarget[] {
  if (!user) return [];
  return moveTargets(user, personaNames).filter((t) => canWriteScope(user, t));
}

/**
 * canUpload answers whether the Upload control is offered for the view in
 * hand: write authority over the one library it names, or over any library at
 * all when it names none.
 */
export function canUpload(
  user: UserProfile | null,
  target: ScopeTarget | null,
  personaNames: string[],
): boolean {
  if (target) return canWriteScope(user, target);
  return uploadTargets(user, personaNames).length > 0;
}

/**
 * targetForTab names the library a picker entry is showing. The All entry
 * spans every library and names none, so it resolves to null.
 */
export function targetForTab(tab: string, user: UserProfile | null): ScopeTarget | null {
  if (tab === ALL_LIBRARIES) return null;
  if (tab === "global") return { scope: "global", scope_id: "" };
  if (tab === "user") return { scope: "user", scope_id: user?.user_id ?? "" };
  return { scope: "persona", scope_id: tab };
}

/**
 * canWriteScope answers, for the browser, the question CanWriteScope answers
 * for the request (pkg/resource/permission.go): may this caller add to this
 * library? Deriving the Upload control from the same rule is what keeps the
 * page from offering an upload the server will refuse — or, worse, silently
 * redirect into the caller's own library.
 *
 * It reads the caller's identity and nothing else. Which page is asking is not
 * part of the question: the server grants a platform administrator every
 * library whatever route the request came in on, and a control that recognizes
 * that authority well enough to say where else to exercise it, while refusing
 * to offer it here, is a defect rather than a safeguard (#1527).
 *
 * A null target is the admin "all" tab, which names no single library; the
 * dialog's own scope picker chooses there.
 */
export function canWriteScope(user: UserProfile | null, target: ScopeTarget | null): boolean {
  if (!user) return false;
  if (isPlatformAdmin(user)) return true;
  if (!target) return false;
  return holdsScope(user, target);
}

/**
 * holdsScope is the authority a caller has over one library as themselves,
 * with no platform-admin override: their own library, and a persona they carry
 * the admin role for. Global is nobody's by this rule — reaching it is the
 * platform-admin override, which canWriteScope applies above.
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
 * A platform administrator is offered every target the server would accept:
 * every persona, the global library, and a named person's library. That holds
 * wherever the picker is drawn — the authority is the identity's, not the
 * page's (#1527).
 *
 * personaNames is the deployment's full persona list, which a page fetches only
 * for a platform administrator; everyone else's personas are the ones their own
 * claims name, so a caller handed a list they hold no authority over is still
 * offered nothing from it.
 */
export function moveTargets(user: UserProfile | null, personaNames: string[]): MoveTarget[] {
  if (!user) return [];
  const admin = isPlatformAdmin(user);
  const targets: MoveTarget[] = [];

  if (user.user_id) {
    targets.push({
      scope: "user",
      scope_id: user.user_id,
      label: "My Resources",
    });
  }

  // The caller's own personas are kept alongside the fetched list rather than
  // replaced by it, so an administrator whose persona list has not arrived yet
  // is still offered the personas they belong to and administer. Deduplicated
  // because either source can name the same persona.
  const personas = new Set<string>([
    ...(admin ? personaNames : []),
    ...([user.persona, ...personaAdminNames(user.roles ?? [])].filter(Boolean) as string[]),
  ]);
  for (const name of personas) {
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
): MoveTarget[] {
  const current = currentLibrary(target, user);
  const rest = moveTargets(user, personaNames).filter(
    (t) => targetKey(t) !== targetKey(current),
  );
  if (rest.length === 0) return [];
  return [current, ...rest];
}
