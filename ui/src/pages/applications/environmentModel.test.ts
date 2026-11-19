import { describe, it, expect } from "vitest";
import type { ApplicationDetail } from "@/api/portal/hooks/applications";
import {
  counted,
  defaultSchema,
  envSchemaOf,
  hasEnvironment,
  prepareTotal,
  promoteItems,
  promotePlan,
  siteEnvOf,
  siteLabel,
} from "./environmentModel";

const base = {
  application: {
    id: "a", name: "shop", description: "", owner_email: "", connection: "db", schema: "orders", schema_adopted_version: 0,
    current_release: 1, created_by: "", created_at: "", updated_at: "", parts: [],
    environments: [
      { name: "dev", release: 2, requires_approval: false, schema: "orders_dev", updated_by: "", updated_at: "" },
      { name: "prod", release: 1, requires_approval: false, updated_by: "", updated_at: "" },
    ],
  },
  parts: [], current_release: null, pending: [], problems: [], findings: [], releases: [],
} as ApplicationDetail;

describe("environmentModel", () => {
  it("reads site part ids, schemas and counts", () => {
    expect(siteEnvOf("web/dev")).toEqual({ site: "web", env: "dev" });
    expect(siteEnvOf("web")).toEqual({ site: "web", env: "" });
    expect(siteLabel("web/dev")).toBe("web / dev");
    expect(siteLabel("web")).toBe("web");
    expect(defaultSchema("orders", "qa-2")).toBe("orders_qa_2");
    expect(counted(1, "library", "libraries")).toBe("1 library");
    expect(counted(2, "library", "libraries")).toBe("2 libraries");
    expect(prepareTotal(undefined)).toBe(0);
    expect(prepareTotal({ scripts: 1, libraries: 2, migrations: 3 })).toBe(6);
    expect(hasEnvironment(base, "dev")).toBe(true);
    expect(hasEnvironment(base, "qa")).toBe(false);
  });

  it("falls back to the environment's own record when the schema view lists none", () => {
    expect(envSchemaOf(base, "dev")?.schema).toBe("orders_dev");
    // An environment naming none runs in the application's.
    expect(envSchemaOf(base, "prod")?.schema).toBe("orders");
    expect(envSchemaOf(base, "qa")).toBeUndefined();
    expect(envSchemaOf({ ...base, application: { ...base.application, schema: "" } }, "dev")).toBeUndefined();
  });

  it("plans a promote with no releases on the page as what it can tell", () => {
    const plan = promotePlan(base, "dev", "prod");
    expect(plan).toEqual({ release: 2, replaced: 1, scripts: null, migrations: [], schema: "orders", sites: [] });
    expect(promoteItems(plan, "prod")).toEqual(["applies no migration to orders"]);
    expect(promoteItems({ ...plan, schema: "", scripts: 0, migrations: [3, 4] }, "prod")).toEqual(["runs no changed script in prod"]);
    expect(promoteItems({ ...plan, migrations: [3, 4], sites: [{ part_id: "web/prod", to: null }] }, "prod")).toEqual([
      "applies migrations 3, 4 to orders",
      "moves web / prod (coupled)",
    ]);
  });
});
