import { describe, it, expect } from "vitest";
import type { UserProfile } from "@/stores/auth";
import {
  canWriteScope,
  currentLibrary,
  libraryCopy,
  libraryOptions,
  moveTargets,
  personaAdminNames,
  targetForTab,
  targetKey,
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

describe("the library a scope tab names", () => {
  it("resolves the caller's own library from their identity, not from the tab key", () => {
    expect(targetForTab("user", reader())).toEqual({
      scope: "user",
      scope_id: "analyst@example.com",
    });
  });

  it("reads any other key as a persona name", () => {
    expect(targetForTab("finance", reader())).toEqual({ scope: "persona", scope_id: "finance" });
  });

  it("names no single library on the admin all-scopes tab", () => {
    expect(targetForTab("all", reader())).toBeNull();
  });
});

describe("who may add to a library", () => {
  it("lets any reader add to their own", () => {
    expect(canWriteScope(reader(), targetForTab("user", reader()))).toBe(true);
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
      "her@example.com's library",
    );
  });

  it("describes a library keyed on a subject identifier rather than printing it", () => {
    // A raw UUID names nobody to the person reading it.
    expect(
      currentLibrary({ scope: "user", scope_id: "550e8400-e29b-41d4-a716-446655440000" }, reader())
        .label,
    ).toBe("Another person's library");
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
