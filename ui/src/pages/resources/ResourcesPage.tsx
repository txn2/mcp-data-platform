import { useState, useEffect } from "react";
import {
  Search,
  FileUp,
  Globe,
  Users,
  User,
  FolderOpen,
  File,
  FileText,
  Loader2,
  ArrowDownWideNarrow,
} from "lucide-react";
import { useInfiniteResources } from "@/api/resources/hooks";
import { useAuthStore } from "@/stores/auth";
import { usePersonas } from "@/api/admin/hooks";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { formatBytes } from "@/lib/format";
import { markdownToPlainText } from "@/lib/markdownText";
import { RESOURCE_POSITIONING } from "@/lib/positioning";
import type { Resource } from "@/api/resources/types";
import { CATEGORIES, scopeIcon, scopeLabel } from "./modals/shared";
import { UploadModal } from "./modals/UploadModal";
import { DetailModal } from "./modals/DetailModal";
import { EditModal } from "./modals/EditModal";
import { DeleteConfirm } from "./modals/DeleteConfirm";

interface Props {
  admin?: boolean;
  onNavigate?: (path: string) => void;
}

function categoryColor(cat: string) {
  switch (cat) {
    case "samples":
      return "bg-sky-100 text-sky-700 dark:bg-sky-950 dark:text-sky-300";
    case "playbooks":
      return "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300";
    case "templates":
      return "bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300";
    case "references":
      return "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300";
    default:
      return "bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300";
  }
}

// NEVER_READ_DAYS is how long a resource must have existed unread before the
// table flags it. A file uploaded yesterday with no reads is not dead weight.
const NEVER_READ_DAYS = 30;

// lastReadLabel renders a resource's read recency for the admin table: a date
// when it has been read, and "Never" when it has not — flagged once the
// resource is old enough for that to mean something.
function lastReadLabel(r: Resource): { text: string; stale: boolean } {
  if (r.last_read_at) {
    return { text: new Date(r.last_read_at).toLocaleDateString(), stale: false };
  }
  const ageDays = (Date.now() - new Date(r.created_at).getTime()) / 86_400_000;
  return { text: "Never", stale: ageDays >= NEVER_READ_DAYS };
}

function scopeBadgeColor(scope: string) {
  switch (scope) {
    case "global":
      return "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300";
    case "persona":
      return "bg-orange-100 text-orange-700 dark:bg-orange-950 dark:text-orange-300";
    default:
      return "bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300";
  }
}


// ResourceRow renders one library entry. It is a component rather than an
// inline map body so the page function stays within the line budget.
function ResourceRow({
  resource: r,
  admin,
  onOpen,
}: {
  resource: Resource;
  admin: boolean;
  onOpen: () => void;
}) {
  const ScopeIcon = scopeIcon(r.scope);
  const lastRead = lastReadLabel(r);
  return (
    <tr
      onClick={onOpen}
      className="border-b last:border-0 cursor-pointer transition-colors hover:bg-accent/50"
    >
      <td className="px-4 py-2.5 max-w-0">
        <div className="flex items-center gap-2">
          <File className="h-4 w-4 text-muted-foreground shrink-0" />
          <div className="min-w-0 flex-1">
            <span className="font-medium truncate block">{r.display_name}</span>
            <span className="text-xs text-muted-foreground truncate block">
              {markdownToPlainText(r.description)}
            </span>
          </div>
        </div>
      </td>
      {admin && (
        <td className="px-4 py-2.5">
          <span className={`text-xs px-1.5 py-0.5 rounded-full font-medium whitespace-nowrap inline-flex items-center gap-0.5 ${scopeBadgeColor(r.scope)}`}>
            <ScopeIcon className="h-2.5 w-2.5" />
            {scopeLabel(r.scope, r.scope_id)}
          </span>
        </td>
      )}
      <td className="px-4 py-2.5">
        <span className={`text-xs px-1.5 py-0.5 rounded-full font-medium whitespace-nowrap ${categoryColor(r.category)}`}>
          {r.category}
        </span>
      </td>
      <td className="px-4 py-2.5 text-xs text-muted-foreground truncate">{r.mime_type}</td>
      <td className="px-4 py-2.5 max-w-0">
        <div className="flex flex-wrap gap-1">
          {(r.tags ?? []).slice(0, 3).map((t) => (
            <span key={t} className="text-xs px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground truncate max-w-[80px]">
              {t}
            </span>
          ))}
          {(r.tags ?? []).length > 3 && (
            <span className="text-xs text-muted-foreground">+{(r.tags ?? []).length - 3}</span>
          )}
        </div>
      </td>
      <td className="px-4 py-2.5 text-right text-muted-foreground">{formatBytes(r.size_bytes)}</td>
      <td className="px-4 py-2.5 text-xs text-muted-foreground truncate">{r.uploader_email || r.uploader_sub}</td>
      <td className="px-4 py-2.5 text-xs text-muted-foreground">{new Date(r.updated_at).toLocaleDateString()}</td>
      {admin && (
        <td
          className={`px-4 py-2.5 text-xs ${lastRead.stale ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground"}`}
          data-testid={`resource-last-read-${r.id}`}
          title={lastRead.stale ? `No reads in the ${NEVER_READ_DAYS} days since upload` : undefined}
        >
          {lastRead.text}
        </td>
      )}
      <td className="px-2 py-2.5">
        <button
          onClick={(e) => { e.stopPropagation(); onOpen(); }}
          className="rounded p-1 text-muted-foreground hover:text-foreground hover:bg-accent"
          title="View details"
        >
          <FileText className="h-3.5 w-3.5" />
        </button>
      </td>
    </tr>
  );
}

// --- Main Page ---

export function ResourcesPage({ admin }: Props) {
  const userPersona = useAuthStore((s) => s.user?.persona);
  const { data: personaData } = usePersonas(!!admin);
  const personaNames = (personaData?.personas ?? []).map((p) => p.name);

  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState("");

  useEffect(() => {
    const timer = setTimeout(() => setSearch(searchInput), 300);
    return () => clearTimeout(timer);
  }, [searchInput]);
  const [activeTab, setActiveTab] = useState<string>(admin ? "all" : "user");
  const [sort, setSort] = useState<"updated" | "last_read">("updated");
  const [showUpload, setShowUpload] = useState(false);
  const [detail, setDetail] = useState<Resource | null>(null);
  const [editing, setEditing] = useState<Resource | null>(null);
  const [deleting, setDeleting] = useState<Resource | null>(null);

  // User mode: filter by tab scope. Admin mode: "all" tab fetches without scope filter.
  const queryParams: Record<string, string | undefined> = {
    category: category || undefined,
    q: search || undefined,
    sort,
  };
  if (activeTab !== "all") {
    queryParams.scope = activeTab === "user" ? "user" : activeTab === "global" ? "global" : "persona";
    if (activeTab !== "user" && activeTab !== "global") {
      queryParams.scope_id = activeTab;
    }
  }

  const { data, isLoading, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useInfiniteResources(queryParams);

  const resources = data?.data ?? [];
  const total = data?.total ?? 0;
  // A narrowed view that returns nothing means the filter missed, not that the
  // scope is empty; the two need different empty states.
  const filtering = search !== "" || category !== "";

  // Build tabs based on mode.
  // User: My Resources + persona tab (if assigned) + Global
  // Admin: All + per-persona tabs + Global
  const tabs: { key: string; label: string; icon: typeof Globe }[] = [];
  if (admin) {
    tabs.push({ key: "all", label: "All Resources", icon: FolderOpen });
    tabs.push({ key: "global", label: "Global", icon: Globe });
    for (const name of personaNames) {
      tabs.push({ key: name, label: name, icon: Users });
    }
    tabs.push({ key: "user", label: "User", icon: User });
  } else {
    tabs.push({ key: "user", label: "My Resources", icon: User });
    if (userPersona) {
      tabs.push({ key: userPersona, label: userPersona, icon: Users });
    }
    tabs.push({ key: "global", label: "Global", icon: Globe });
  }

  return (
    <div className="space-y-4">
      {/* Tabs */}
      <div className="flex items-center gap-2 border-b pb-px overflow-x-auto">
        {tabs.map((tab) => {
          const Icon = tab.icon;
          return (
            <button
              key={tab.key}
              onClick={() => setActiveTab(tab.key)}
              className={`flex items-center gap-1.5 px-3 py-2 text-sm font-medium border-b-2 transition-colors whitespace-nowrap ${
                activeTab === tab.key
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground hover:border-muted-foreground/30"
              }`}
            >
              <Icon className="h-3.5 w-3.5" />
              {tab.label}
            </button>
          );
        })}
      </div>

      {/* Filters + Upload */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-[200px]">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            type="text"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder="Search resources..."
            className="w-full rounded-md border bg-background pl-9 pr-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          />
        </div>
        <select
          value={category}
          onChange={(e) => setCategory(e.target.value)}
          className="rounded-md border bg-background px-3 py-2 text-sm"
        >
          <option value="">All categories</option>
          {CATEGORIES.map((c) => (
            <option key={c} value={c}>{c}</option>
          ))}
        </select>
        {admin && (
          <div className="relative">
            <ArrowDownWideNarrow className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <select
              value={sort}
              onChange={(e) => setSort(e.target.value as "updated" | "last_read")}
              data-testid="resources-sort"
              aria-label="Sort resources"
              className="rounded-md border bg-background pl-8 pr-3 py-2 text-sm"
            >
              <option value="updated">Recently updated</option>
              <option value="last_read">Recently read</option>
            </select>
          </div>
        )}
        <button
          onClick={() => setShowUpload(true)}
          className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 transition-colors"
        >
          <FileUp className="h-4 w-4" />
          Upload
        </button>
      </div>

      {/* Results */}
      {isLoading ? (
        <div className="flex items-center justify-center py-12 text-muted-foreground">
          <Loader2 className="h-5 w-5 animate-spin mr-2" />
          Loading...
        </div>
      ) : resources.length === 0 ? (
        <div data-testid="resources-empty" className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <FolderOpen className="h-12 w-12 mb-2 opacity-30" />
          {filtering ? (
            // A filter that matched nothing is not an empty library, and saying
            // so would send someone off to upload a file they already have.
            <p className="text-sm font-medium">No resources match this search</p>
          ) : (
            <>
              <p className="text-sm font-medium">No resources yet</p>
              <p className="text-xs mt-1 max-w-lg text-center">{RESOURCE_POSITIONING}</p>
              <button
                onClick={() => setShowUpload(true)}
                className="mt-3 inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
              >
                <FileUp className="h-4 w-4" />
                Upload Resource
              </button>
            </>
          )}
        </div>
      ) : (
        <div className="rounded-lg border bg-card overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width: admin ? "25%" : "30%"}}>Name</th>
                {admin && <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width:"10%"}}>Scope</th>}
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width:"9%"}}>Category</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width:"13%"}}>Type</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width:"12%"}}>Tags</th>
                <th className="px-4 py-2.5 text-right font-medium text-muted-foreground" style={{width:"7%"}}>Size</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width: admin ? "13%" : "15%"}}>Uploader</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width:"8%"}}>Updated</th>
                {admin && <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width:"8%"}}>Last read</th>}
                <th className="px-4 py-2.5" style={{width:"3%"}} />
              </tr>
            </thead>
            <tbody>
              {resources.map((r) => (
                <ResourceRow key={r.id} resource={r} admin={!!admin} onOpen={() => setDetail(r)} />
              ))}
            </tbody>
          </table>
        </div>
      )}

      <InfiniteFooter
        hasMore={hasNextPage}
        isLoadingMore={isFetchingNextPage}
        onLoadMore={fetchNextPage}
      />

      {total > resources.length && (
        <p className="text-sm text-muted-foreground text-center">
          Showing {resources.length} of {total} resources
        </p>
      )}

      {/* Upload Modal */}
      {showUpload && <UploadModal onClose={() => setShowUpload(false)} admin={!!admin} personaNames={personaNames} />}

      {/* Detail Modal */}
      {detail && (
        <DetailModal
          resource={detail}
          admin={!!admin}
          onClose={() => setDetail(null)}
          onEdit={() => { setEditing(detail); setDetail(null); }}
          onDelete={() => { setDeleting(detail); setDetail(null); }}
        />
      )}

      {/* Edit Modal */}
      {editing && <EditModal resource={editing} onClose={() => setEditing(null)} />}

      {/* Delete Confirm */}
      {deleting && <DeleteConfirm resource={deleting} onClose={() => setDeleting(null)} />}
    </div>
  );
}
