import { describe, it, expect } from "vitest";
import type { UserProfile } from "@/stores/auth";
import {
  canWriteScope,
  currentLibrary,
  displayPath,
  libraryCopy,
  libraryOptions,
  moveTargets,
  personaAdminNames,
  personRoot,
  rootFor,
  rootsFor,
  targetKey,
  uploadTargets,
  withheldUploadPersonas,
  PERSON_TARGET,
} from "./scopes";

function reader(overrides: Partial<UserProfile> = {}): UserProfile {
  return {
    user_id: "analyst@example.com",
    email: "analyst@example.com",
    roles: ["dp_analyst"],
    is_admin: false,
    persona: "analyst",
    ...overrides,
  };
}

describe("persona-admin grants read from roles", () => {
  it("takes the persona name off a role however it is prefixed", () => {
    expect(personaAdminNames(["persona-admin:finance"])).toEqual(["finance"]);
    expect(personaAdminNames(["dp_persona-admin:engineering"])).toEqual(["engineering"]);
  });

  it("collects every grant and ignores the roles that are not one", () => {
    expect(personaAdminNames(["dp_persona-admin:finance", "dp_analyst", "dp_persona-admin:ops"])).toEqual([
      "finance",
      "ops",
    ]);
  });

  it("is not a grant when the role names no persona", () => {
    expect(personaAdminNames(["persona-admin:"])).toEqual([]);
    expect(personaAdminNames(["dp_admin"])).toEqual([]);
  });
});

describe("the folder a top-level key names", () => {
  it("writes to the caller's own identity from My Resources, and reads every key they own", () => {
    const mine = rootFor("user", rootsFor(reader(), []));
    expect(mine.label).toBe("My Resources");
    expect(mine.target).toEqual({ scope: "user", scope_id: "analyst@example.com" });
    // No id: the server reads the subject and the address the caller's files
    // can be keyed by.
    expect(mine.params).toEqual({ scope: "user" });
  });

  it("reads any other key as a persona, listed or not", () => {
    expect(rootFor("finance", rootsFor(reader(), [])).target).toEqual({ scope: "persona", scope_id: "finance" });
  });

  it("reads a person's key as that person's folder, named by their address", () => {
    const person = rootFor("person:sub-9", [], () => "nine@example.com");
    expect(person.label).toBe("nine@example.com");
    expect(person.params).toEqual({ scope: "user", scope_id: "sub-9" });
    expect(personRoot("sub-9").label).toBe("sub-9");
  });

  it("writes a location as a path from the top-level folder", () => {
    const [mine, global] = rootsFor(reader(), []);
    expect(displayPath(global!, "data/weekly")).toBe("/Global/data/weekly");
    expect(displayPath(mine!, "")).toBe("/My Resources");
    expect(displayPath(personRoot("sub-9", "nine@example.com"), "a")).toBe("/People/nine@example.com/a");
  });
});

describe("who may add to a library", () => {
  it("lets any reader add to their own", () => {
    expect(canWriteScope(reader(), rootFor("user", rootsFor(reader(), [])).target)).toBe(true);
  });

  it("refuses a reader the global library", () => {
    expect(canWriteScope(reader(), { scope: "global", scope_id: "" })).toBe(false);
  });

  it("refuses a reader a persona library they only belong to", () => {
    expect(canWriteScope(reader(), { scope: "persona", scope_id: "analyst" })).toBe(false);
  });

  it("grants the persona a persona-admin role names, and only that one", () => {
    const user = reader({ roles: ["dp_analyst", "dp_persona-admin:analyst"] });
    expect(canWriteScope(user, { scope: "persona", scope_id: "analyst" })).toBe(true);
    expect(canWriteScope(user, { scope: "persona", scope_id: "finance" })).toBe(false);
    expect(canWriteScope(user, { scope: "global", scope_id: "" })).toBe(false);
  });

  // The server grants a platform admin every library whatever route the request
  // arrived on (CanWriteScope, pkg/resource/permission.go), so the browser does
  // too: the authority is the identity's, not the page's (#1527).
  it("grants a platform admin every library, however their admin status is stated", () => {
    for (const admin of [
      reader({ is_admin: true }),
      reader({ roles: ["admin"] }),
      reader({ roles: ["platform-admin"] }),
    ]) {
      expect(canWriteScope(admin, { scope: "global", scope_id: "" })).toBe(true);
      expect(canWriteScope(admin, { scope: "persona", scope_id: "finance" })).toBe(true);
      expect(canWriteScope(admin, { scope: "user", scope_id: "someone@example.com" })).toBe(true);
      // The admin all-scopes tab names no library; the dialog picks there.
      expect(canWriteScope(admin, null)).toBe(true);
    }
  });

  it("refuses a reader the all-scopes tab, which names no library to check", () => {
    expect(canWriteScope(reader(), null)).toBe(false);
  });

  it("refuses an unauthenticated caller everything", () => {
    expect(canWriteScope(null, { scope: "user", scope_id: "analyst@example.com" })).toBe(false);
  });

  it("refuses a caller with no resolved id their own library rather than matching on empty", () => {
    expect(canWriteScope(reader({ user_id: "" }), { scope: "user", scope_id: "" })).toBe(false);
  });
});

describe("what a library is called", () => {
  it("names each scope and says who else sees it", () => {
    expect(libraryCopy({ scope: "user", scope_id: "analyst@example.com" }).name).toBe("My Resources");
    expect(libraryCopy({ scope: "global", scope_id: "" }).name).toBe("Global");
    expect(libraryCopy({ scope: "persona", scope_id: "analyst" }).name).toBe("analyst persona");
    expect(libraryCopy({ scope: "persona", scope_id: "analyst" }).audience).toContain("analyst");
  });

  it("says who fills a library the caller cannot", () => {
    expect(libraryCopy({ scope: "global", scope_id: "" }).source).toContain("platform administrators");
    expect(libraryCopy({ scope: "persona", scope_id: "finance" }).source).toContain(
      "finance persona's administrators",
    );
  });
});

describe("the libraries a resource can be moved to", () => {
  it("offers a persona the caller merely belongs to, which Upload does not", () => {
    const targets = moveTargets(reader(), []);
    expect(targets.map((t) => targetKey(t))).toEqual([
      "user:analyst@example.com",
      "persona:analyst",
    ]);
    // The looser arm is the whole point: belonging is enough to receive a file
    // you already own, while adding a new one still takes the admin role.
    expect(canWriteScope(reader(), { scope: "persona", scope_id: "analyst" })).toBe(false);
  });

  it("offers nothing but their own library to a reader in no persona", () => {
    const targets = moveTargets(reader({ persona: undefined, roles: [] }), []);
    expect(targets.map((t) => targetKey(t))).toEqual(["user:analyst@example.com"]);
  });

  it("names a persona once even when the caller both belongs to it and administers it", () => {
    const targets = moveTargets(reader({ persona: "finance", roles: ["dp_persona-admin:finance"] }), []);
    expect(targets.filter((t) => t.scope === "persona")).toHaveLength(1);
  });

  // A reader is never offered a persona out of the deployment's list, whatever
  // list they are handed: the fetched names are the administrator's, and the
  // page that has no administrator to fetch them for passes none.
  it("offers a reader nothing out of a persona list they hold no authority over", () => {
    const targets = moveTargets(reader(), ["finance", "ops"]);
    expect(targets.map((t) => targetKey(t))).toEqual([
      "user:analyst@example.com",
      "persona:analyst",
    ]);
  });

  it("gives an administrator every persona, the global library, and a named person", () => {
    const targets = moveTargets(reader({ is_admin: true, persona: undefined }), ["finance", "ops"]);
    expect(targets.map((t) => targetKey(t))).toEqual([
      "user:analyst@example.com",
      "persona:finance",
      "persona:ops",
      "global:",
      `user:${PERSON_TARGET}`,
    ]);
  });

  // The same targets wherever the picker is drawn: the override is the
  // identity's and not the page's, and withholding Global from the person the
  // server grants it to was the defect (#1527).
  it("keeps an administrator the personas their own claims name before the list arrives", () => {
    const targets = moveTargets(reader({ is_admin: true, persona: "analyst" }), []);
    expect(targets.map((t) => targetKey(t))).toEqual([
      "user:analyst@example.com",
      "persona:analyst",
      "global:",
      `user:${PERSON_TARGET}`,
    ]);
  });

  it("names a persona once when both the list and the caller's claims carry it", () => {
    const targets = moveTargets(reader({ is_admin: true, persona: "finance" }), ["finance", "ops"]);
    expect(targets.filter((t) => t.scope === "persona").map((t) => t.scope_id)).toEqual([
      "finance",
      "ops",
    ]);
  });

  it("offers nobody anything when nobody is signed in", () => {
    expect(moveTargets(null, ["finance"])).toEqual([]);
  });
});

describe("the library a resource is in now", () => {
  it("is the reader's own when it is keyed on them", () => {
    expect(currentLibrary({ scope: "user", scope_id: "analyst@example.com" }, reader()).label).toBe(
      "My Resources",
    );
  });

  it("names another person by the address it is keyed on", () => {
    expect(currentLibrary({ scope: "user", scope_id: "her@example.com" }, reader()).label).toBe(
      "her@example.com's resources",
    );
  });

  it("describes a library keyed on a subject identifier rather than printing it", () => {
    // A raw UUID names nobody to the person reading it.
    expect(
      currentLibrary({ scope: "user", scope_id: "550e8400-e29b-41d4-a716-446655440000" }, reader())
        .label,
    ).toBe("Another person's resources");
  });

  it("names a persona and the global library", () => {
    expect(currentLibrary({ scope: "persona", scope_id: "ops" }, reader()).label).toBe("ops persona");
    expect(currentLibrary({ scope: "global", scope_id: "" }, reader()).label).toBe("Global");
  });
});

describe("the options the Library picker offers", () => {
  it("puts the current library first and never twice", () => {
    const options = libraryOptions({ scope: "user", scope_id: "analyst@example.com" }, reader(), []);
    expect(options.map((t) => targetKey(t))).toEqual([
      "user:analyst@example.com",
      "persona:analyst",
    ]);
  });

  it("offers the current library even when the caller could not move it there", () => {
    // An administrator editing a file in somebody else's library has to be able
    // to leave it where it is.
    const options = libraryOptions(
      { scope: "user", scope_id: "her@example.com" },
      reader({ is_admin: true }),
      ["ops"],
    );
    expect(options[0]).toMatchObject({ scope: "user", scope_id: "her@example.com" });
  });

  it("is empty when there is nowhere else to put the file", () => {
    // A picker whose only entry is where the file already sits is a control
    // that cannot do anything, so the field is not shown at all.
    const own = { scope: "user", scope_id: "analyst@example.com" } as const;
    expect(libraryOptions(own, reader({ persona: undefined, roles: [] }), [])).toEqual([]);
  });
});

describe("the top-level folders a caller sees", () => {
  it("gives a reader their own, Global, then their persona", () => {
    expect(rootsFor(reader(), []).map((r) => r.key)).toEqual(["user", "global", "analyst"]);
  });

  it("names a persona the caller only administers, alongside the one they are in", () => {
    const keys = rootsFor(reader({ roles: ["dp_analyst", "dp_persona-admin:finance"] }), []).map((r) => r.key);
    expect(keys).toContain("analyst");
    expect(keys).toContain("finance");
  });

  // Listing is membership-scoped for an ordinary caller, so a persona they are
  // not in would list nothing; the deployment's persona list is not theirs.
  it("does not hand a reader the deployment's other personas", () => {
    const keys = rootsFor(reader(), ["ops", "finance"]).map((r) => r.key);
    expect(keys).not.toContain("ops");
    expect(keys).not.toContain("finance");
  });

  // An administrator may write and list every persona (resource.ListScopes), so
  // hiding one would hide a folder they own material in (#1553).
  it("gives a platform administrator every persona the deployment defines, once each", () => {
    const keys = rootsFor(reader({ is_admin: true }), ["ops", "finance", "analyst"]).map((r) => r.key);
    expect(keys).toEqual(["user", "global", "analyst", "finance", "ops"]);
  });

  it("offers My Resources and Global to a caller who is not signed in", () => {
    expect(rootsFor(null, ["ops"]).map((r) => r.key)).toEqual(["user", "global"]);
  });
});

describe("where an upload may land", () => {
  it("is the caller's own library and nothing else for an ordinary reader", () => {
    expect(uploadTargets(reader(), []).map(targetKey)).toEqual(["user:analyst@example.com"]);
  });

  // A persona a reader BELONGS to accepts a file they already own but not a new
  // upload, which is the one arm where CanMoveToLibrary is looser than
  // CanWriteScope. The move picker offers it; this must not.
  it("leaves out a persona the caller only belongs to", () => {
    expect(uploadTargets(reader(), []).map(targetKey)).not.toContain("persona:analyst");
    expect(moveTargets(reader(), []).map(targetKey)).toContain("persona:analyst");
  });

  it("includes a persona the caller administers", () => {
    const keys = uploadTargets(reader({ roles: ["dp_persona-admin:finance"] }), []).map(targetKey);
    expect(keys).toContain("persona:finance");
  });

  it("gives an administrator every persona, the global library and a named person", () => {
    const keys = uploadTargets(reader({ is_admin: true }), ["ops"]).map(targetKey);
    expect(keys).toContain("persona:ops");
    expect(keys).toContain("global:");
    expect(keys).toContain(`user:${PERSON_TARGET}`);
  });
});

// #1866: the persona library a member cannot upload into is named, so its
// absence from the destinations reads as a boundary rather than a gap.
describe("the persona libraries an upload is withheld from", () => {
  it("names the persona a reader belongs to and does not administer", () => {
    expect(withheldUploadPersonas(reader())).toEqual(["analyst"]);
  });

  it("names nothing once the caller administers that persona", () => {
    expect(withheldUploadPersonas(reader({ roles: ["dp_persona-admin:analyst"] }))).toEqual([]);
  });

  it("names nothing for an administrator, who may upload anywhere", () => {
    expect(withheldUploadPersonas(reader({ is_admin: true }))).toEqual([]);
  });

  it("names nothing for a caller with no persona or no session", () => {
    expect(withheldUploadPersonas(reader({ persona: undefined }))).toEqual([]);
    expect(withheldUploadPersonas(null)).toEqual([]);
  });
});
