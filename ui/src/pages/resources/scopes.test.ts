import { describe, it, expect } from "vitest";
import type { UserProfile } from "@/stores/auth";
import { canWriteScope, libraryCopy, personaAdminNames, targetForTab } from "./scopes";

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
