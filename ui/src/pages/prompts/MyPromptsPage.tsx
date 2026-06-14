import { useState, useEffect, useCallback, useMemo } from "react";
import {
  Search,
  Plus,
  Globe,
  Users,
  User,
  MessageSquare,
  X,
  Save,
  ChevronDown,
  ChevronUp,
  ChevronsUpDown,
} from "lucide-react";
import { useMyPrompts, useCreateMyPrompt, useSearchMyPrompts, useSharedPrompts } from "@/api/portal/hooks";
import type { SharedPromptItem } from "@/api/portal/hooks";
import type { Prompt } from "@/api/admin/types";
import { SharePermissionBadge } from "@/components/SharePermissionBadge";
import { cn } from "@/lib/utils";
import { extractPromptArguments } from "./promptArguments";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { PromptNameField } from "./PromptNameField";
import { PromptStatusBadge } from "./PromptStatusBadge";
import { TagsField } from "./TagsField";
import { validatePromptName, isPromptNameConflict } from "./promptName";

interface Props {
  onNavigate: (path: string) => void;
}

type ScopeStyle = { label: string; icon: typeof Globe; color: string };

const scopeStyles: Record<string, ScopeStyle> = {
  global: { label: "Global", icon: Globe, color: "bg-blue-500/10 text-blue-400 border-blue-500/20" },
  persona: { label: "Persona", icon: Users, color: "bg-purple-500/10 text-purple-400 border-purple-500/20" },
  personal: { label: "Personal", icon: User, color: "bg-zinc-500/10 text-zinc-400 border-zinc-500/20" },
  system: { label: "System", icon: MessageSquare, color: "bg-amber-500/10 text-amber-400 border-amber-500/20" },
};

const defaultScopeStyle: ScopeStyle = scopeStyles["personal"]!;

function getScopeStyle(scope: string): ScopeStyle {
  const match = scopeStyles[scope];
  return match !== undefined ? match : defaultScopeStyle;
}

function ScopeBadge({ scope }: { scope: string }) {
  const cfg = getScopeStyle(scope);
  const Icon = cfg.icon;
  return (
    <span className={cn("inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap", cfg.color)}>
      <Icon className="h-3 w-3" />
      {cfg.label}
    </span>
  );
}

type Tab = "personal" | "available" | "shared";
type SortKey = "name" | "scope" | "description" | "category";
type SortDir = "asc" | "desc";

interface FormData {
  name: string;
  display_name: string;
  description: string;
  content: string;
  category: string;
  tags: string[];
  arguments: Prompt["arguments"];
}

const emptyForm: FormData = {
  name: "",
  display_name: "",
  description: "",
  content: "",
  category: "",
  tags: [],
  arguments: [],
};

function sortValue(p: Prompt, key: SortKey): string {
  switch (key) {
    case "name": return (p.display_name || p.name || "").toLowerCase();
    case "scope": return p.scope || "";
    case "description": return (p.description || "").toLowerCase();
    case "category": return (p.category || "").toLowerCase();
    default: return "";
  }
}

export function MyPromptsPage({ onNavigate }: Props) {
  const [tab, setTab] = useState<Tab>("personal");
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState<FormData>(emptyForm);
  const [mutationError, setMutationError] = useState<string | null>(null);
  const [nameConflict, setNameConflict] = useState<string | null>(null);
  const [sortBy, setSortBy] = useState<SortKey>("name");
  const [sortDir, setSortDir] = useState<SortDir>("asc");

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(timer);
  }, [search]);

  const { data, isLoading } = useMyPrompts();
  const { data: sharedPrompts = [], isLoading: sharedLoading } = useSharedPrompts();
  const createMutation = useCreateMyPrompt();
  const searching = debouncedSearch.trim().length > 0;
  // Semantic ranking covers personal/available scopes; on the Shared tab the
  // search box filters the shared list client-side instead.
  const searchResults = useSearchMyPrompts(tab === "shared" ? "" : debouncedSearch);

  const personal = data?.personal ?? [];
  const available = data?.available ?? [];
  const items = tab === "personal" ? personal : available;
  const isPersonalTab = tab === "personal";
  const isSharedTab = tab === "shared";

  const filteredSharedPrompts = sharedPrompts.filter((s) => {
    const q = debouncedSearch.trim().toLowerCase();
    if (!q) return true;
    return (
      (s.prompt.display_name || s.prompt.name || "").toLowerCase().includes(q) ||
      (s.prompt.description || "").toLowerCase().includes(q)
    );
  });

  const handleSort = useCallback((key: SortKey) => {
    setSortBy((prev) => {
      if (prev === key) {
        setSortDir((d) => (d === "asc" ? "desc" : "asc"));
        return prev;
      }
      setSortDir("asc");
      return key;
    });
  }, []);

  // Browse mode: the active tab's prompts sorted by the chosen column.
  const sorted = useMemo(() => {
    const list = [...items];
    list.sort((a, b) => {
      const av = sortValue(a, sortBy);
      const bv = sortValue(b, sortBy);
      const cmp = av.localeCompare(bv);
      return sortDir === "asc" ? cmp : -cmp;
    });
    return list;
  }, [items, sortBy, sortDir]);

  // Search mode: approved prompts across the caller's visibility ranked by
  // relevance. The server applies visibility before ranking and returns results
  // already ordered, so the rank order is preserved (no client re-sort).
  const ranked = useMemo(
    () => (searchResults.data?.data ?? []).map((s) => s.prompt),
    [searchResults.data],
  );

  const displayItems = searching ? ranked : sorted;
  const listLoading = searching ? searchResults.isLoading : isLoading;

  function openCreate() {
    setForm(emptyForm);
    setCreating(true);
  }

  function handleContentChange(next: string) {
    setForm((prev) => ({
      ...prev,
      content: next,
      arguments: extractPromptArguments(next, prev.arguments),
    }));
  }

  function updateArgField(name: string, patch: Partial<Prompt["arguments"][number]>) {
    setForm((prev) => ({
      ...prev,
      arguments: prev.arguments.map((a) => (a.name === name ? { ...a, ...patch } : a)),
    }));
  }

  function handleCreate() {
    setMutationError(null);
    setNameConflict(null);
    createMutation.mutate(form, {
      onSuccess: (p) => {
        setCreating(false);
        setMutationError(null);
        if (p?.id) onNavigate(`/prompts/${p.id}`);
      },
      onError: (err) => {
        const msg = err instanceof Error ? err.message : "Operation failed";
        if (isPromptNameConflict(msg)) {
          setNameConflict("That name is already taken.");
        } else {
          setMutationError(msg);
        }
      },
    });
  }

  function openPrompt(p: Prompt) {
    onNavigate(`/prompts/${p.id}`);
  }

  const colDefs: { key: SortKey; label: string; width?: string; mdOnly?: boolean }[] = [
    { key: "name", label: "Name" },
    { key: "scope", label: "Scope", width: "w-[110px]" },
    { key: "description", label: "Description" },
    { key: "category", label: "Category", width: "w-[120px]", mdOnly: true },
  ];

  function renderSortHeader(col: typeof colDefs[number]) {
    const active = sortBy === col.key;
    return (
      <th
        key={col.key}
        onClick={() => handleSort(col.key)}
        className={cn(
          "px-4 py-2 text-left font-medium text-muted-foreground cursor-pointer select-none hover:bg-muted/80",
          col.width,
          col.mdOnly && "hidden md:table-cell",
        )}
      >
        <span className="inline-flex items-center gap-1">
          {col.label}
          {active ? (
            sortDir === "asc" ? <ChevronUp className="h-3 w-3 text-foreground" /> : <ChevronDown className="h-3 w-3 text-foreground" />
          ) : (
            <ChevronsUpDown className="h-3 w-3 text-muted-foreground/50" />
          )}
        </span>
      </th>
    );
  }

  return (
    <div className="space-y-4">
      {/* Toolbar */}
      <div className="flex items-center gap-3">
        <div className="flex rounded-md border bg-muted/50">
          <button
            onClick={() => { setTab("personal"); setMutationError(null); }}
            className={cn("px-3 py-1.5 text-sm font-medium rounded-md", tab === "personal" ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground")}
          >
            Personal ({personal.length})
          </button>
          <button
            onClick={() => { setTab("available"); setMutationError(null); }}
            className={cn("px-3 py-1.5 text-sm font-medium rounded-md", tab === "available" ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground")}
          >
            Available ({available.length})
          </button>
          <button
            onClick={() => { setTab("shared"); setMutationError(null); }}
            className={cn("px-3 py-1.5 text-sm font-medium rounded-md", tab === "shared" ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground")}
          >
            Shared ({sharedPrompts.length})
          </button>
        </div>
        <div className="relative max-w-md flex-1">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input type="text" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search prompts by meaning..." className="w-full rounded-md border bg-background pl-9 pr-3 py-2 text-sm outline-none ring-ring focus:ring-2" />
        </div>
        {isPersonalTab && (
          <button onClick={openCreate} className="ml-auto inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90">
            <Plus className="h-4 w-4" /> New Prompt
          </button>
        )}
      </div>

      {/* Create form (inline) */}
      {creating && (
        <div className="rounded-lg border bg-card p-4 space-y-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold">Create Prompt</h3>
            <button onClick={() => setCreating(false)} className="text-muted-foreground hover:text-foreground"><X className="h-4 w-4" /></button>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <PromptNameField
              value={form.name}
              onChange={(v) => { setForm({ ...form, name: v }); setNameConflict(null); }}
              serverError={nameConflict}
            />
            <div>
              <label className="text-xs text-muted-foreground">Display Name</label>
              <input value={form.display_name} onChange={(e) => setForm({ ...form, display_name: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" placeholder="My Prompt" />
            </div>
            <div className="col-span-2">
              <label className="text-xs text-muted-foreground">Description</label>
              <textarea
                value={form.description}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
                rows={3}
                placeholder="What this prompt does"
                className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none resize-y"
              />
            </div>
            <div className="col-span-2">
              <label className="text-xs text-muted-foreground">Content (Markdown)</label>
              <MarkdownEditor
                value={form.content}
                onChange={handleContentChange}
                minHeight="12rem"
                placeholder="Prompt content with {{arg}} placeholders..."
              />
              <p className="text-[11px] text-muted-foreground mt-1">
                Use <code className="font-mono">{"{{name}}"}</code> (preferred) or <code className="font-mono">{"{name}"}</code> to declare an argument. Rows auto-appear below as you type.
              </p>
            </div>
            <div className="col-span-2">
              <label className="text-xs text-muted-foreground">Arguments</label>
              {form.arguments.length === 0 ? (
                <div className="rounded-md border bg-muted/20 px-3 py-3 text-xs text-muted-foreground">
                  No arguments yet. Add a <code className="font-mono">{"{{placeholder}}"}</code> in the content above.
                </div>
              ) : (
                <div className="rounded-md border bg-background overflow-hidden">
                  <div className="grid grid-cols-[minmax(0,160px)_minmax(0,1fr)_110px] gap-3 px-3 py-2 border-b bg-muted/40 text-[11px] font-medium text-muted-foreground uppercase tracking-wide">
                    <div>Name</div>
                    <div>Description</div>
                    <div className="text-right">Required</div>
                  </div>
                  <ul className="divide-y">
                    {form.arguments.map((a) => (
                      <li key={a.name} className="grid grid-cols-[minmax(0,160px)_minmax(0,1fr)_110px] gap-3 px-3 py-2 items-start">
                        <code className="text-xs font-mono text-foreground bg-muted/60 rounded px-1.5 py-0.5 break-all mt-1">
                          {`{{${a.name}}}`}
                        </code>
                        <textarea
                          value={a.description}
                          onChange={(e) => updateArgField(a.name, { description: e.target.value })}
                          placeholder="What this argument is for"
                          rows={2}
                          className="w-full rounded-md border bg-background px-2 py-1 text-xs outline-none ring-ring focus:ring-2 resize-y"
                        />
                        <label className="inline-flex items-center justify-end gap-2 text-xs cursor-pointer select-none mt-1.5">
                          <input
                            type="checkbox"
                            checked={a.required}
                            onChange={(e) => updateArgField(a.name, { required: e.target.checked })}
                            className="h-3.5 w-3.5"
                          />
                          <span className={cn("font-medium", a.required ? "text-rose-400" : "text-muted-foreground")}>
                            {a.required ? "Required" : "Optional"}
                          </span>
                        </label>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
            <div>
              <label className="text-xs text-muted-foreground">Category</label>
              <input value={form.category} onChange={(e) => setForm({ ...form, category: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" placeholder="workflow" />
            </div>
            <TagsField tags={form.tags} onChange={(tags) => setForm({ ...form, tags })} />
          </div>
          {mutationError && (
            <div className="rounded-md bg-red-500/10 border border-red-500/20 px-3 py-2 text-xs text-red-400">{mutationError}</div>
          )}
          <div className="flex justify-end gap-2 pt-2">
            <button onClick={() => setCreating(false)} className="rounded-md border px-3 py-1.5 text-sm hover:bg-muted">Cancel</button>
            <button onClick={handleCreate} disabled={!form.content || createMutation.isPending || validatePromptName(form.name) !== null} className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50">
              <Save className="h-3.5 w-3.5" /> {createMutation.isPending ? "Saving..." : "Create"}
            </button>
          </div>
        </div>
      )}

      {/* Relevance hint (search mode): results are ranked by similarity across
          every scope the caller can see, so the active tab does not bound them. */}
      {searching && !isSharedTab && (
        <p className="text-xs text-muted-foreground">
          Ranked by relevance to &ldquo;{debouncedSearch.trim()}&rdquo; across all prompts you can see.
        </p>
      )}

      {/* Shared tab: prompts others shared with the current user. */}
      {isSharedTab ? (
        <SharedPromptsTable
          items={filteredSharedPrompts}
          isLoading={sharedLoading}
          onOpen={(id) => onNavigate(`/prompts/${id}`)}
        />
      ) : (
      <>
      {/* Table */}
      {listLoading ? (
        <div className="flex items-center justify-center py-12 text-muted-foreground">{searching ? "Searching..." : "Loading..."}</div>
      ) : displayItems.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <MessageSquare className="h-12 w-12 mb-2 opacity-30" />
          <p className="text-sm font-medium">
            {searching
              ? `No prompts match "${debouncedSearch.trim()}"`
              : items.length === 0
                ? (isPersonalTab ? "No personal prompts yet" : "No available prompts")
                : "No prompts match your search"}
          </p>
        </div>
      ) : (
        <div className="rounded-lg border bg-card overflow-hidden">
          <table className="w-full text-sm table-fixed">
            <colgroup>
              <col className="w-[28%]" />
              <col className="w-[110px]" />
              <col />
              <col className="hidden md:table-column w-[120px]" />
            </colgroup>
            <thead className="border-b bg-muted/50">
              <tr>
                {colDefs.map(renderSortHeader)}
              </tr>
            </thead>
            <tbody className="divide-y">
              {displayItems.map((p) => (
                <tr
                  key={p.id}
                  className="hover:bg-muted/30 cursor-pointer"
                  onClick={() => openPrompt(p)}
                >
                  <td className="px-4 py-2 align-top">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-medium break-words">{p.display_name || p.name}</span>
                      {p.scope !== "system" && <PromptStatusBadge status={p.status} />}
                      {p.review_requested && (
                        <span className="inline-flex items-center rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-400">
                          promotion requested
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-2 align-top"><ScopeBadge scope={p.scope} /></td>
                  <td className="px-4 py-2 align-top text-muted-foreground">
                    <div className="break-words whitespace-normal">{p.description}</div>
                  </td>
                  <td className="px-4 py-2 align-top text-xs text-muted-foreground hidden md:table-cell break-words">{p.category || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      </>
      )}
    </div>
  );
}

function SharedPromptsTable({
  items,
  isLoading,
  onOpen,
}: {
  items: SharedPromptItem[];
  isLoading: boolean;
  onOpen: (id: string) => void;
}) {
  if (isLoading) {
    return <div className="flex items-center justify-center py-12 text-muted-foreground">Loading...</div>;
  }
  if (items.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
        <MessageSquare className="h-12 w-12 mb-2 opacity-30" />
        <p className="text-sm font-medium">No shared prompts</p>
        <p className="text-xs mt-1">
          Prompts others share with you will appear here, runnable as <code>shared-&lt;name&gt;</code>.
        </p>
      </div>
    );
  }
  return (
    <div className="rounded-lg border bg-card overflow-hidden">
      <table className="w-full text-sm table-fixed">
        <thead>
          <tr className="border-b bg-muted/50">
            <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[40%]">Name</th>
            <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[25%]">Shared By</th>
            <th className="px-4 py-2.5 text-center font-medium text-muted-foreground w-[12%]">Access</th>
            <th className="px-4 py-2.5 text-left font-medium text-muted-foreground w-[12%]">Shared</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <tr
              key={item.share_id}
              onClick={() => onOpen(item.prompt.id)}
              className="border-b last:border-0 cursor-pointer transition-colors hover:bg-accent/50"
            >
              <td className="px-4 py-2.5 max-w-0">
                <div className="flex items-center gap-2">
                  <MessageSquare className="h-4 w-4 text-muted-foreground shrink-0" />
                  <div className="min-w-0 flex-1">
                    <span className="font-medium truncate block">{item.prompt.display_name || item.prompt.name}</span>
                    {item.prompt.description && (
                      <span className="text-xs text-muted-foreground truncate block">{item.prompt.description}</span>
                    )}
                  </div>
                </div>
              </td>
              <td className="px-4 py-2.5 max-w-0">
                <span className="text-muted-foreground truncate block">{item.shared_by}</span>
              </td>
              <td className="px-4 py-2.5 text-center">
                <SharePermissionBadge permission={item.permission} />
              </td>
              <td className="px-4 py-2.5 text-muted-foreground">
                {new Date(item.shared_at).toLocaleDateString()}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
