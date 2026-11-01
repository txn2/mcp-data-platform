import type { UserProfile } from "@/stores/auth";
import { scopeLabel } from "./shared";
import { resolvePortal } from "@/lib/portalAccess";
import { sortNames } from "@/lib/sortByName";

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

/**
 * A top-level folder of the Resources page (#1872): My Resources, Global, one
 * per persona the caller can read, and -- for a platform administrator -- one
 * per person under People.
 *
 * Each is a scope internally; the page never calls it anything but a folder.
 */
export interface ResourceRoot {
  /** The key the address names it by: "user", "global", a persona, or "person:<id>". */
  key: string;
  /** What the tree, the path bar and every location call it. */
  label: string;
  /** Where a write lands: an upload, a new folder, a folder move. */
  target: ScopeTarget;
  /**
   * What a read is narrowed by. My Resources names no id, so the server reads
   * every key the caller's own files are stored under (subject and address).
   */
  params: { scope: string; scope_id?: string };
  /**
   * The files shared with the caller (#2258): no folder of its own, listed
   * from the shares rather than by scope, and nothing can be written there.
   */
  shared?: boolean;
  /** An external person's own folder, reached through External files (#2290). */
  external?: boolean;
}

/** The key of the files shared with the caller. */
export const SHARED_ROOT_KEY = "shared-with-me";

/** The key prefix of a person's folder under People. */
export const PERSON_PREFIX = "person:";

/** The top-level folder a page opens on. */
export const DEFAULT_ROOT = "user";

/** The label of the administrator's folder of people. */
export const PEOPLE_LABEL = "People";

/** The key prefix of an external person's folder under External files. */
export const EXTERNAL_PREFIX = "external:";

/** The label of the folder of external people's folders (#2290). */
export const EXTERNAL_FILES_LABEL = "External files";

/**
 * externalFilesLevel is the caller's External files level (#2290), as the
 * server applies it: an administrator manages.
 */
export function externalFilesLevel(user: UserProfile | null): "none" | "view" | "manage" {
  if (isPlatformAdmin(user)) return "manage";
  if (!user) return "none";
  return resolvePortal(user.portal).external_files;
}

/** externalRoot is one external person's folder, named by them and their persona. */
export function externalRoot(f: { scope_id: string; email: string; name?: string; persona: string; persona_display_name?: string }): ResourceRoot {
  return {
    key: EXTERNAL_PREFIX + f.scope_id,
    label: `${f.name || f.email} (${f.persona_display_name || f.persona})`,
    target: { scope: "user", scope_id: f.scope_id },
    params: { scope: "user", scope_id: f.scope_id },
    external: true,
  };
}

/**
 * personasFor is the personas a caller sees a folder for: the one they are
 * resolved to, the ones they administer, and -- for a platform administrator --
 * every persona the deployment defines.
 *
 * An administrator's list is the whole deployment because their authority is
 * the whole deployment: CanWriteScope lets them upload into any persona and
 * ListScopes lets them list any persona, so a tree built from membership alone
 * would hide folders they own material in (#1553).
 */
function personasFor(user: UserProfile | null, personaNames: string[]): string[] {
  if (!user) return [];
  const mine = [user.persona, ...personaAdminNames(user.roles ?? [])].filter(Boolean) as string[];
  const all = isPlatformAdmin(user) ? [...personaNames, ...mine] : mine;
  return sortNames(new Set(all));
}

function personaRoot(name: string): ResourceRoot {
  return {
    key: name,
    label: name,
    target: { scope: "persona", scope_id: name },
    params: { scope: "persona", scope_id: name },
  };
}

/**
 * rootsFor is the top-level folders in the order the tree lists them: the
 * caller's own, Global, then each persona. People is not among them: it is a
 * folder of folders, loaded when it is opened.
 */
export function rootsFor(user: UserProfile | null, personaNames: string[]): ResourceRoot[] {
  const external = resolvePortal(user?.portal).external;
  return [
    {
      key: "user",
      label: "My Resources",
      target: { scope: "user", scope_id: user?.user_id ?? "" },
      params: { scope: "user" },
    },
    {
      key: SHARED_ROOT_KEY,
      label: "Shared with me",
      target: { scope: "user", scope_id: "" },
      params: { scope: "user" },
      shared: true,
    },
    // A person of an external persona never reads Global (#2257).
    ...(external
      ? []
      : [
          {
            key: "global",
            label: "Global",
            target: { scope: "global" as const, scope_id: "" },
            params: { scope: "global" },
          },
        ]),
    // A persona at the own level reaches no persona folder (#2225), and a
    // person of an external persona reads only their own folder (#2290).
    ...(resourcesLevel(user) === "own" || external ? [] : personasFor(user, personaNames).map(personaRoot)),
  ];
}

/**
 * resourcesLevel is the caller's Resources level (#2225), as the server applies
 * it to every resources route: an administrator's is always the highest.
 */
function resourcesLevel(user: UserProfile | null): string {
  if (isPlatformAdmin(user)) return "persona";
  return resolvePortal(user?.portal).resources;
}

/** personRoot is one person's folder under People, named by their address. */
export function personRoot(scopeID: string, email = ""): ResourceRoot {
  return {
    key: PERSON_PREFIX + scopeID,
    label: email || scopeID,
    target: { scope: "user", scope_id: scopeID },
    params: { scope: "user", scope_id: scopeID },
  };
}

/**
 * rootFor resolves the key an address names. A persona the tree does not list
 * is still resolved, because the persona list arrives after the page does and a
 * pasted link has to keep its place.
 */
export function rootFor(
  key: string,
  roots: ResourceRoot[],
  emailOf?: (id: string) => string,
  externalOf?: (id: string) => ResourceRoot | undefined,
): ResourceRoot {
  const listed = roots.find((r) => r.key === key);
  if (listed) return listed;
  if (key.startsWith(EXTERNAL_PREFIX)) {
    const id = key.slice(EXTERNAL_PREFIX.length);
    return externalOf?.(id) ?? { ...personRoot(id), key, external: true };
  }
  if (key.startsWith(PERSON_PREFIX)) {
    const id = key.slice(PERSON_PREFIX.length);
    return personRoot(id, emailOf?.(id) ?? "");
  }
  return personaRoot(key);
}

/**
 * rootOfResource is the top-level folder a file is filed in, which is where a
 * search hit shows it is and where Show in folder opens (#2293): a search runs
 * over every library the caller reads, not the folder in view. The caller's own
 * files are stored under their subject or their address; anyone else's personal
 * file is that person's folder under People, or under External files when it
 * is one.
 */
export function rootOfResource(
  r: { scope: string; scope_id?: string },
  user: UserProfile | null,
  roots: ResourceRoot[],
  emailOf?: (id: string) => string,
  externalOf?: (id: string) => ResourceRoot | undefined,
): ResourceRoot {
  const id = r.scope_id ?? "";
  if (r.scope === "global") return rootFor("global", roots);
  if (r.scope === "persona") return rootFor(id, roots);
  if (user && (id === user.user_id || (!!user.email && id === user.email))) return rootFor(DEFAULT_ROOT, roots);
  const external = externalOf?.(id);
  if (external) return external;
  return rootFor(PERSON_PREFIX + id, roots, emailOf);
}

/**
 * displayPath writes a location the way the page shows it: a path from the
 * top-level folder, `/Global/data/weekly`, with a person's folder under People.
 */
export function displayPath(root: ResourceRoot, path: string): string {
  const head = root.key.startsWith(PERSON_PREFIX)
    ? `/${PEOPLE_LABEL}/${root.label}`
    : root.external
      ? `/${EXTERNAL_FILES_LABEL}/${root.label}`
      : `/${root.label}`;
  return path ? `${head}/${path}` : head;
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
 * withheldUploadPersonas lists the persona libraries this caller belongs to but
 * may not upload into (#1866): membership is enough to move a file they own
 * into the library (CanMoveToLibrary), and not to add a new one to it
 * (CanWriteScope), which takes the persona's admin role. The upload dialogs
 * name them, so a library missing from the destination list reads as a
 * permission boundary with a way through it rather than a missing feature.
 */
export function withheldUploadPersonas(user: UserProfile | null): string[] {
  if (!user?.persona || isPlatformAdmin(user)) return [];
  const persona = user.persona;
  return holdsScope(user, { scope: "persona", scope_id: persona }) ? [] : [persona];
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
  // The Resources level narrows it (#2225): Read writes nothing, Own folder
  // writes only the caller's own.
  const level = resourcesLevel(user);
  if (level === "none") return false;
  // Read writes nothing, but for a person of an external persona whose
  // persona lets them upload, to their own folder (#2258).
  if (level === "shared") return uploads(user) && target.scope === "user" && holdsScope(user, target);
  if (level === "own" && target.scope !== "user") return false;
  return holdsScope(user, target);
}

/** uploads reports whether the caller's external persona lets them upload. */
function uploads(user: UserProfile): boolean {
  const p = resolvePortal(user.portal);
  return p.external && p.uploads;
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
    source: "Only you can add to My Resources.",
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
  // because either source can name the same persona, and offered in name
  // order between "My Resources" and the administrator's entries (#2389).
  const personas = sortNames(new Set<string>([
    ...(admin ? personaNames : []),
    ...([user.persona, ...personaAdminNames(user.roles ?? [])].filter(Boolean) as string[]),
  ]));
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
      label: "A person's folder...",
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
