import { describe, it, expect, beforeEach } from "vitest";
import { renderHook } from "@testing-library/react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { useAuthStore } from "@/stores/auth";
import { approvalFact, approvesOnSave, ownedByViewer, useViewerIdentity } from "./approval";

// What the surfaces say about an approval nobody reviewed (#1367), stated once
// so the listing, the contract summary, the editor and the version history
// cannot describe the same approval three different ways.

function contractOf(over: Partial<ScriptContract> = {}): ScriptContract {
  return {
    id: "script-001",
    name: "daily",
    scope: "personal",
    owner_email: "sarah.chen@example.com",
    status: "active",
    enabled: true,
    params: [],
    approval: { approved: false },
    ...over,
  };
}

beforeEach(() => {
  useAuthStore.setState({ user: null });
});

describe("approvalFact", () => {
  it("names an automatic approval as one rather than as somebody's decision", () => {
    const fact = approvalFact(
      contractOf({
        approval: {
          approved: true,
          version: 4,
          approved_by: "sarah.chen@example.com",
          approved_at: "2026-08-15T09:00:00Z",
          automatic: true,
        },
      }),
    );
    expect(fact).toContain("v4 automatically");
    expect(fact).toContain("nobody reviewed it");
    expect(fact).not.toContain("by sarah.chen@example.com");
  });

  it("names the reviewer of a decision a person made", () => {
    const fact = approvalFact(
      contractOf({
        approval: {
          approved: true,
          version: 4,
          approved_by: "admin@acme.example.com",
          approved_at: "2026-08-15T09:00:00Z",
        },
      }),
    );
    expect(fact).toContain("by admin@acme.example.com");
    expect(fact).not.toContain("automatically");
  });

  it("says nothing is approved when nothing is", () => {
    expect(approvalFact(contractOf())).toBe("nothing approved");
  });
});

describe("approvesOnSave", () => {
  it("is the owner's own personal script and nothing else", () => {
    const mine = contractOf();
    expect(approvesOnSave(mine, "sarah.chen@example.com")).toBe(true);

    // An administrator reading somebody else's personal script is not its
    // owner: their edit captures THEIR roles, so it goes to review.
    expect(approvesOnSave(mine, "admin@acme.example.com")).toBe(false);
    // A caller the platform cannot name matches no owner.
    expect(approvesOnSave(mine, "")).toBe(false);
    // A shared script has an audience that agreed to nothing.
    expect(approvesOnSave(contractOf({ scope: "global" }), "sarah.chen@example.com")).toBe(false);
    expect(approvesOnSave(contractOf({ scope: "persona" }), "sarah.chen@example.com")).toBe(false);
  });
});

describe("ownedByViewer", () => {
  it("refuses to match an unidentified caller against an unidentified owner", () => {
    expect(ownedByViewer(contractOf({ owner_email: undefined }), "")).toBe(false);
  });
});

describe("useViewerIdentity", () => {
  it("falls back to the user id when the credential carries no email", () => {
    useAuthStore.setState({
      user: { user_id: "u-77", roles: [], is_admin: false },
    });
    expect(readIdentity()).toBe("u-77");

    useAuthStore.setState({
      user: { user_id: "u-77", email: "sarah.chen@example.com", roles: [], is_admin: false },
    });
    expect(readIdentity()).toBe("sarah.chen@example.com");

    useAuthStore.setState({ user: null });
    expect(readIdentity()).toBe("");
  });
});

// readIdentity calls the hook the way a component would.
function readIdentity(): string {
  return renderHook(() => useViewerIdentity()).result.current;
}
