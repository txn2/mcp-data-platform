import { useState } from "react";
import { DashboardPage } from "@/pages/dashboard/DashboardPage";
import { useBranding } from "@/api/portal/hooks";
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
  const { data: branding } = useBranding();
  const title = branding?.portal_title || "MCP Data Platform";
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

      {tab === "dashboard" && <DashboardPage onNavigate={onNavigate} />}
      {tab === "help" && <HelpTab onNavigate={onNavigate} title={title} />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Help Tab — System overview with deep links to section help
// ---------------------------------------------------------------------------

const sections = [
  {
    path: "/admin/tools#help",
    label: "Tools",
    icon: Wrench,
    description:
      "MCP tools, connections, toolkits, and semantic enrichment. Explore and test tools interactively.",
  },
  {
    path: "/admin/audit#help",
    label: "Audit Log",
    icon: ScrollText,
    description:
      "Comprehensive audit logging of every tool call including timing, enrichment status, and error tracking.",
  },
  {
    path: "/admin/knowledge#help",
    label: "Knowledge",
    icon: Lightbulb,
    description:
      "Domain knowledge capture, insight lifecycle management, and automated catalog integration.",
  },
  {
    path: "/admin/personas#help",
    label: "Personas",
    icon: Users,
    description:
      "Authorization and customization layer — role-based tool filtering, prompts, and hints.",
  },
  {
    path: "/admin/settings#help",
    label: "Settings",
    icon: Settings,
    description:
      "Platform configuration viewer, YAML import/export, API key management, and config history.",
  },
];

function HelpTab({
  onNavigate,
  title,
}: {
  onNavigate: (path: string) => void;
  title: string;
}) {
  return (
    <div className="max-w-3xl space-y-8">
      {/* Platform Overview */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">What is {title}?</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {title} connects your AI assistants to your data infrastructure.
          When an assistant queries a database, searches a catalog, or reads
          from storage, the platform automatically enriches every response
          with business context &mdash; ownership, data quality, glossary
          definitions, and more &mdash; so the assistant gives more accurate,
          context-aware answers.
        </p>
      </section>

      {/* Key Capabilities */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">What You Can Do</h2>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">Automatic Enrichment</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Every response includes relevant context from your data catalog.
              Database results show who owns the data, how fresh it is, and
              what the columns mean in business terms.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">Access Control</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Each user is assigned a persona that controls which tools they
              can use. Analysts might query data but not modify it. Engineers
              might have full access. Nobody gets access they shouldn&apos;t have.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">Activity Tracking</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Every action is logged with who did it, what they did, how long
              it took, and whether it succeeded. Use the audit log to monitor
              usage, troubleshoot issues, and ensure compliance.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">Knowledge Capture</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              When users share knowledge about their data &mdash; corrections,
              tips, or context &mdash; it gets captured for review. Approved
              insights are applied to your data catalog, improving it over time.
            </p>
          </div>
        </div>
      </section>

      {/* How It Works */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">How It Works</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          When an AI assistant makes a request, it passes through several layers:
        </p>
        <div className="space-y-2">
          {[
            {
              label: "Identity Check",
              detail:
                "Confirms who the user is and ensures they have valid credentials.",
            },
            {
              label: "Access Control",
              detail:
                "Matches the user to their persona and verifies they can use the requested tool.",
            },
            {
              label: "Logging",
              detail:
                "Records the action for the audit trail, including timing and context.",
            },
            {
              label: "Enrichment",
              detail:
                "Adds business context from the data catalog to make responses more useful.",
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

      {/* Data Sources */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Connected Services</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          The platform connects to three types of services, each providing
          different capabilities:
        </p>
        <div className="overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-3 py-2 text-left font-medium">Service</th>
                <th className="px-3 py-2 text-left font-medium">What It Does</th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium">SQL Engine</td>
                <td className="px-3 py-2 text-xs">
                  Run queries, explore database schemas, and browse data catalogs
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium">Data Catalog</td>
                <td className="px-3 py-2 text-xs">
                  Search for datasets, view data lineage, and look up business definitions
                </td>
              </tr>
              <tr>
                <td className="px-3 py-2 font-medium">Object Storage</td>
                <td className="px-3 py-2 text-xs">
                  Browse and retrieve files, generate download links
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      {/* Section Deep Links */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Explore Each Section</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          Click any section to learn more about what it does:
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
    </div>
  );
}
