import { useState } from "react";
import { DashboardPage } from "@/pages/dashboard/DashboardPage";
import { Wrench, ScrollText, Lightbulb, Users, Settings } from "lucide-react";

type Tab = "dashboard" | "help";

const TAB_ITEMS: { key: Tab; label: string }[] = [
  { key: "dashboard", label: "Dashboard" },
  { key: "help", label: "Help" },
];

export function HomePage({
  initialTab,
  onNavigate,
}: {
  initialTab?: string;
  onNavigate: (path: string) => void;
}) {
  const [tab, setTab] = useState<Tab>(
    initialTab === "help" ? "help" : "dashboard",
  );

  return (
    <div className="space-y-4">
      <div className="flex gap-1 border-b">
        {TAB_ITEMS.map((t) => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            className={`px-4 py-2 text-sm font-medium transition-colors ${
              tab === t.key
                ? "border-b-2 border-primary text-primary"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === "dashboard" && <DashboardPage />}
      {tab === "help" && <HelpTab onNavigate={onNavigate} />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Help Tab — System overview with deep links to section help
// ---------------------------------------------------------------------------

const sections = [
  {
    path: "/tools#help",
    label: "Tools",
    icon: Wrench,
    description:
      "MCP tools, connections, toolkits, and semantic enrichment. Explore and test tools interactively.",
  },
  {
    path: "/audit#help",
    label: "Audit Log",
    icon: ScrollText,
    description:
      "Comprehensive audit logging of every tool call including timing, enrichment status, and error tracking.",
  },
  {
    path: "/knowledge#help",
    label: "Knowledge",
    icon: Lightbulb,
    description:
      "Domain knowledge capture, insight lifecycle management, and automated catalog integration.",
  },
  {
    path: "/personas#help",
    label: "Personas",
    icon: Users,
    description:
      "Authorization and customization layer — role-based tool filtering, prompts, and hints.",
  },
  {
    path: "/settings#help",
    label: "Settings",
    icon: Settings,
    description:
      "Platform configuration viewer, YAML import/export, API key management, and config history.",
  },
];

function HelpTab({ onNavigate }: { onNavigate: (path: string) => void }) {
  return (
    <div className="max-w-3xl space-y-8">
      {/* Platform Overview */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">
          MCP Data Platform Overview
        </h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          The MCP Data Platform is a semantic data platform server that composes
          multiple MCP toolkits (Trino, DataHub, S3) with a required semantic
          layer. It provides <strong>bidirectional cross-injection</strong> where
          tool responses automatically include critical context from other
          services, giving AI assistants richer, more accurate information.
        </p>
      </section>

      {/* Key Capabilities */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Key Capabilities</h2>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">Semantic Enrichment</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Every tool response is enriched with metadata from the semantic
              layer. Trino query results include DataHub context (owners, tags,
              glossary terms). DataHub searches include query availability from
              Trino.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">
              Role-Based Authorization
            </h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Personas control which tools each user can access via allow/deny
              patterns. Authentication supports OIDC, API keys, and OAuth 2.1.
              Tool access is fail-closed by default.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">
              Comprehensive Audit Trail
            </h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Every tool call is logged with full context: user, persona,
              parameters, duration, enrichment status, and response size. Audit
              data supports filtering, search, and CSV/JSON export.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">Knowledge Capture</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Domain knowledge shared during sessions is captured as insights,
              reviewed by admins, and applied to the data catalog. Corrections,
              business context, and data quality observations are tracked.
            </p>
          </div>
        </div>
      </section>

      {/* Architecture */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Architecture</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          Requests flow through a middleware chain before reaching the toolkits.
          Each layer adds capabilities:
        </p>
        <div className="space-y-2">
          {[
            {
              label: "Authentication",
              detail:
                "Validates user identity via OIDC tokens, API keys, or OAuth 2.1 bearer tokens.",
            },
            {
              label: "Authorization",
              detail:
                "Maps the user to a persona and filters tool access based on allow/deny patterns.",
            },
            {
              label: "Audit",
              detail:
                "Logs every tool call asynchronously with timing and context metadata.",
            },
            {
              label: "Rule Enforcement",
              detail:
                "Injects operational guidance and tuning rules into tool responses.",
            },
            {
              label: "Semantic Enrichment",
              detail:
                "Appends metadata from the semantic layer (DataHub) and query engine (Trino).",
            },
          ].map((step, i) => (
            <div key={step.label} className="flex gap-3 rounded-lg border p-3">
              <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary/10 text-xs font-semibold text-primary">
                {i + 1}
              </span>
              <div>
                <p className="text-sm font-medium">{step.label}</p>
                <p className="text-xs text-muted-foreground">{step.detail}</p>
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* Toolkits */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Toolkits</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          The platform composes three toolkit families, each providing
          specialized MCP tools:
        </p>
        <div className="overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-3 py-2 text-left font-medium">Toolkit</th>
                <th className="px-3 py-2 text-left font-medium">Purpose</th>
                <th className="px-3 py-2 text-left font-medium">
                  Example Tools
                </th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium">Trino</td>
                <td className="px-3 py-2 text-xs">
                  SQL query execution, schema exploration, catalog browsing
                </td>
                <td className="px-3 py-2 font-mono text-xs">
                  trino_query, trino_describe_table, trino_list_catalogs
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium">DataHub</td>
                <td className="px-3 py-2 text-xs">
                  Metadata catalog search, lineage, glossary, data products
                </td>
                <td className="px-3 py-2 font-mono text-xs">
                  datahub_search, datahub_get_entity, datahub_get_lineage
                </td>
              </tr>
              <tr>
                <td className="px-3 py-2 font-medium">S3</td>
                <td className="px-3 py-2 text-xs">
                  Object storage operations, bucket management, presigned URLs
                </td>
                <td className="px-3 py-2 font-mono text-xs">
                  s3_list_objects, s3_get_object, s3_put_object
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      {/* Section Deep Links */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Section Guides</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          Each section has detailed help documentation. Click to learn more:
        </p>
        <div className="grid gap-3 sm:grid-cols-2">
          {sections.map((s) => (
            <button
              key={s.path}
              onClick={() => onNavigate(s.path)}
              className="flex items-start gap-3 rounded-lg border p-4 text-left transition-colors hover:border-primary/50 hover:bg-muted/50"
            >
              <s.icon className="mt-0.5 h-5 w-5 shrink-0 text-primary" />
              <div>
                <p className="text-sm font-medium">{s.label}</p>
                <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
                  {s.description}
                </p>
              </div>
            </button>
          ))}
        </div>
      </section>

      {/* Configuration Modes */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Configuration Modes</h2>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">
              File Mode{" "}
              <code className="rounded bg-muted px-1 py-0.5 text-xs">
                config_mode: file
              </code>
            </h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              All configuration (personas, connections, toolkits) is loaded from{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                platform.yaml
              </code>{" "}
              at startup. Admin write operations return 405. Changes require a
              server restart.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">
              Database Mode{" "}
              <code className="rounded bg-muted px-1 py-0.5 text-xs">
                config_mode: database
              </code>
            </h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Configuration is stored in the database with full CRUD via the
              admin API. Changes take effect immediately. Initial data is seeded
              from YAML on first run.
            </p>
          </div>
        </div>
      </section>

      {/* Admin API */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Admin API</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          This admin portal communicates with the platform via the Admin API,
          served at{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">
            /api/v1/admin/
          </code>
          . All endpoints require an API key with admin role. The API provides
          system info, tool management, audit log queries, knowledge management,
          and persona CRUD operations.
        </p>
      </section>
    </div>
  );
}
