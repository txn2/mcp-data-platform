import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { EffectiveAccess } from "@/api/admin/types";
import { resolvePortal } from "@/lib/portalAccess";
import type { PersonaDraft } from "./types";
import { exactRule, blockingDeny, withRule, RuleRows } from "./RuleRows";
import { IdentitySection, roleWarnings, accountTypeOf } from "./IdentitySection";
import { InstructionsSection } from "./InstructionsSection";
import { PortalAccessSection } from "./PortalAccessSection";
import { AccessSummary, referenceLines } from "./AccessSummary";
import { toPayload } from "./usePersonaEditor";

vi.mock("@/api/admin/hooks", () => ({
  usePersonas: () => ({ data: { personas: [{ name: "marketing", display_name: "Marketing", roles: [] }] } }),
}));
vi.mock("@/components/MarkdownEditor", () => ({
  MarkdownEditor: ({ value, onChange }: { value: string; onChange: (v: string) => void }) => (
    <textarea aria-label="text" value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));

function draft(over: Partial<PersonaDraft> = {}): PersonaDraft {
  return {
    name: "analyst",
    displayName: "Analyst",
    description: "",
    roles: ["analyst"],
    allowTools: [],
    denyTools: [],
    allowConnections: [],
    denyConnections: [],
    apiRoutes: [],
    priority: 10,
    descriptionPrefix: "",
    descriptionOverride: "",
    agentInstructionsSuffix: "",
    agentInstructionsOverride: "",
    serviceAccount: false,
    portal: resolvePortal(),
    external: false,
    allowSharing: true,
    ...over,
  };
}

function report(over: Partial<EffectiveAccess> = {}): EffectiveAccess {
  return {
    is_admin: false,
    admin_persona: "admin",
    account_type: "internal",
    calls_excluded_by_config: false,
    assignment: { roles: ["analyst"], role_mapping: [], folder_reader_roles: ["analyst"], folder_writer_role: "persona-admin:analyst", role_overlaps: [] },
    tools: { total: 0, callable: 0, items: [] },
    connections: { total: 0, allowed: 0, items: [] },
    api_operations: { total: 0, callable: 0, items: [] },
    scratch: [],
    portal: { stored: {}, effective: {}, capped_by_external: [] },
    sharing: true,
    resource_scopes: ["user", "persona:analyst", "global"],
    references: { secrets: { named: [], unrestricted: 0 }, script_grants: 0, script_versions: 0, prompts: 0, shares: 0, webhook_sources: [], endpoints: [], api_keys: 0, people: [] },
    unscoped: { any_caller_endpoints: 0, anyone_endpoints: 0 },
    ...over,
  };
}

// A row writes its own name; a deny pattern still wins and disables Allow
// with the pattern named (#2257).
describe("rule rows", () => {
  const lists = { allow: ["trino_query"], deny: ["*_execute"] };
  it("reads and writes the row's own entry, leaving no stale opposite", () => {
    expect(exactRule("trino_query", lists)).toBe("allow");
    expect(exactRule("search", lists)).toBe("none");
    expect(withRule("trino_query", "deny", lists)).toEqual({ allow: [], deny: ["*_execute", "trino_query"] });
    expect(withRule("trino_query", "none", lists)).toEqual({ allow: [], deny: ["*_execute"] });
    expect(blockingDeny("trino_execute", lists)).toBe("*_execute");
    expect(blockingDeny("trino_query", lists)).toBeUndefined();
  });

  it("disables Allow on a row a deny pattern matches", () => {
    const onChange = vi.fn();
    render(
      <RuleRows
        rows={[{ name: "trino_execute", detail: "trino", decision: "Denied by *_execute", usable: false }]}
        lists={lists}
        onChange={onChange}
        locked={false}
        isReadOnly={false}
        noun="tools"
      />,
    );
    const allow = screen.getByRole("button", { name: "Allow" });
    expect(allow).toBeDisabled();
    expect(allow.getAttribute("title")).toBe("Blocked by deny pattern *_execute");
    fireEvent.click(screen.getByRole("button", { name: "Deny" }));
    expect(onChange).toHaveBeenCalledWith({ allow: ["trino_query"], deny: ["*_execute", "trino_execute"] });
    fireEvent.click(screen.getByRole("button", { name: /Pattern rules \(1 allow, 1 deny\)/ }));
    expect(screen.getByText("A deny pattern wins over every allow.")).toBeTruthy();
  });
});

describe("identity", () => {
  it("makes the account type one of three", () => {
    const onUpdate = vi.fn();
    render(<IdentitySection draft={draft()} onUpdate={onUpdate} isCreate={false} isReadOnly={false} report={report()} />);
    fireEvent.click(screen.getByLabelText(/Automated caller/));
    expect(onUpdate).toHaveBeenCalledWith({ external: false, serviceAccount: true });
    fireEvent.click(screen.getByLabelText(/External people/));
    expect(onUpdate).toHaveBeenCalledWith({ external: true, serviceAccount: false });
    expect(accountTypeOf(draft({ serviceAccount: true }))).toBe("automated");
  });

  it("never offers External on the administrator persona", () => {
    render(<IdentitySection draft={draft({ name: "admin" })} onUpdate={vi.fn()} isCreate={false} isReadOnly={false} report={report()} />);
    expect(screen.getByText("Administrator persona")).toBeTruthy();
    expect(screen.getByText("The administrator persona cannot be external.")).toBeTruthy();
    expect(screen.getByLabelText(/External people/)).toBeDisabled();
  });

  it("states who else a role reaches", () => {
    const r = report({
      assignment: { ...report().assignment, role_mapping: ["oidc-analysts"], role_overlaps: [{ role: "analyst", persona: "engineer", priority: 20 }] },
    });
    const w = roleWarnings(draft({ external: true }), r);
    expect(w[0]).toContain("engineer (priority 20) also lists analyst; engineer wins");
    expect(w).toHaveLength(1);
    render(<IdentitySection draft={draft()} onUpdate={vi.fn()} isCreate={false} isReadOnly={false} report={r} />);
    expect(screen.getByText("oidc-analysts")).toBeTruthy();
  });
});

describe("instructions", () => {
  it("moves the text when the mode changes, so nothing is silently discarded", () => {
    const onUpdate = vi.fn();
    render(<InstructionsSection draft={draft({ descriptionPrefix: "Be brief." })} onUpdate={onUpdate} isReadOnly={false} />);
    fireEvent.click(screen.getByLabelText("Replace platform text"));
    expect(onUpdate).toHaveBeenCalledWith({ descriptionPrefix: "", descriptionOverride: "Be brief." });
  });
});

describe("portal access", () => {
  it("locks what External sets and shows the saved choice", () => {
    const r = report({
      account_type: "external",
      portal: { stored: {}, effective: { knowledge: "none", external: true }, capped_by_external: ["knowledge"] },
    });
    render(<PortalAccessSection draft={draft({ external: true })} onUpdate={vi.fn()} report={r} isReadOnly={false} />);
    expect(screen.getByText("Set by External: None. Saved choice: Edit.")).toBeTruthy();
    expect(screen.getByLabelText("Knowledge pages")).toBeDisabled();
    expect(screen.getByText("Their own folder")).toBeTruthy();
  });

  it("turns an external persona's uploads on (#2290)", () => {
    const onUpdate = vi.fn();
    const d = draft({ external: true, portal: { ...resolvePortal(), uploads: false } });
    render(<PortalAccessSection draft={d} onUpdate={onUpdate} report={report()} isReadOnly={false} />);
    fireEvent.click(screen.getByLabelText(/^Uploads/));
    expect(onUpdate.mock.calls[0]?.[0].portal.uploads).toBe(true);
  });

  it("offers no uploads setting to an internal persona", () => {
    render(<PortalAccessSection draft={draft()} onUpdate={vi.fn()} report={report()} isReadOnly={false} />);
    expect(screen.queryByLabelText(/^Uploads/)).toBeNull();
  });

  it("sets earlier versions and External files (#2290)", () => {
    const onUpdate = vi.fn();
    const d = draft({ portal: { ...resolvePortal(), history: false, external_files: "none" } });
    render(<PortalAccessSection draft={d} onUpdate={onUpdate} report={report()} isReadOnly={false} />);
    fireEvent.click(screen.getByLabelText(/^Earlier versions/));
    expect(onUpdate.mock.calls[0]?.[0].portal.history).toBe(true);
    expect(screen.getByLabelText("External files")).toBeTruthy();
    expect(screen.getByText(/View lists and opens them; Manage also renames/)).toBeTruthy();
  });
});

describe("access summary", () => {
  it("lists what is granted elsewhere", () => {
    const lines = referenceLines({
      ...report().references,
      secrets: { named: ["crm-token"], unrestricted: 2 },
      webhook_sources: ["stripe-events"],
      endpoints: ["store-lookup"],
      api_keys: 1,
    }).map((l) => l.text);
    expect(lines).toContain("Endpoints admitting this persona: store-lookup");
    expect(lines).toContain("Stored secrets naming this persona: crm-token");
    expect(lines).toContain("2 stored secrets name no persona, so this one may use them");
    expect(lines).toContain("Webhook sources writing to its folder: stripe-events");
    expect(lines).toContain("1 API key resolves here");
  });

  it("counts the endpoints no persona setting governs (#2259)", () => {
    render(<AccessSummary draft={draft()} report={report({ unscoped: { any_caller_endpoints: 1, anyone_endpoints: 3 } })} isLoading={false} />);
    expect(screen.getByText(/1 authenticated endpoint open to every authenticated caller/)).toBeTruthy();
    expect(screen.getByText(/3 endpoints open to anyone, with or without a bot check/)).toBeTruthy();
  });

  it("says what the scratch tables allow", () => {
    const r = report({
      scratch: [{ connection: "acme-trino", catalog: "scratch", schema: "uploads", list: true, register: true, drop_via_sql: true, registrations: 3, queryable_from: [] }],
      account_type: "external",
    });
    render(<AccessSummary draft={draft()} report={r} isLoading={false} />);
    expect(screen.getByText(/scratch.uploads on acme-trino: can register; 3 registered tables. Can drop any registered table through SQL./)).toBeTruthy();
    expect(screen.getByText("External takes away")).toBeTruthy();
  });
});

describe("the saved persona", () => {
  it("sends uploads only for an external persona, and one account type", () => {
    const up = true;
    const internal = toPayload(draft({ serviceAccount: true, portal: { ...resolvePortal(), uploads: up } }));
    expect(internal.portal).not.toHaveProperty("uploads");
    expect(internal.service_account).toBe(true);
    const ext = toPayload(draft({ external: true, serviceAccount: true, portal: { ...resolvePortal(), uploads: up } }));
    expect(ext.portal).toHaveProperty("uploads", up);
    expect(ext.service_account).toBeUndefined();
    expect(ext.portal).not.toHaveProperty("external");
  });
});
