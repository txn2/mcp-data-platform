import { useState, useEffect, useCallback, useMemo } from "react";
import {
  Search,
  Plus,
  Trash2,
  Globe,
  Users,
  User,
  MessageSquare,
  X,
  Save,
  ToggleLeft,
  ToggleRight,
  ChevronRight,
  ChevronDown,
  ChevronUp,
  ChevronsUpDown,
  Edit,
  Copy,
} from "lucide-react";
import {
  useAdminPrompts,
  useCreateAdminPrompt,
  useUpdateAdminPrompt,
  useDeleteAdminPrompt,
} from "@/api/admin/hooks";
import type { Prompt } from "@/api/admin/types";
import { cn } from "@/lib/utils";

interface Props {
  onNavigate: (path: string) => void;
}

const scopeConfig: Record<string, { label: string; icon: typeof Globe; color: string }> = {
  global: { label: "Global", icon: Globe, color: "bg-blue-500/10 text-blue-400 border-blue-500/20" },
  persona: { label: "Persona", icon: Users, color: "bg-purple-500/10 text-purple-400 border-purple-500/20" },
  personal: { label: "Personal", icon: User, color: "bg-zinc-500/10 text-zinc-400 border-zinc-500/20" },
  system: { label: "System", icon: MessageSquare, color: "bg-amber-500/10 text-amber-400 border-amber-500/20" },
};

function ScopeBadge({ scope }: { scope: string }) {
  const cfg = scopeConfig[scope] ?? scopeConfig.personal;
  const Icon = cfg.icon;
  return (
    <span className={cn("inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium", cfg.color)}>
      <Icon className="h-3 w-3" />
      {cfg.label}
    </span>
  );
}

type SortKey = "name" | "scope" | "description" | "owner" | "status";
type SortDir = "asc" | "desc";

type FormMode = "closed" | "create" | "edit";

interface FormData {
  id?: string;
  name: string;
  display_name: string;
  description: string;
  content: string;
  category: string;
  scope: Prompt["scope"];
  personas: string;
  owner_email: string;
  enabled: boolean;
}

const emptyForm: FormData = {
  name: "",
  display_name: "",
  description: "",
  content: "",
  category: "",
  scope: "global",
  personas: "",
  owner_email: "",
  enabled: true,
};

const columns: { key: SortKey; label: string; width?: string }[] = [
  { key: "name", label: "Name" },
  { key: "scope", label: "Scope", width: "w-[100px]" },
  { key: "description", label: "Description" },
  { key: "owner", label: "Owner", width: "w-[160px]" },
  { key: "status", label: "Status", width: "w-[70px]" },
];

function sortValue(p: Prompt, key: SortKey): string {
  switch (key) {
    case "name": return (p.display_name || p.name || "").toLowerCase();
    case "scope": return p.scope || "";
    case "description": return (p.description || "").toLowerCase();
    case "owner": return (p.owner_email || "").toLowerCase();
    case "status": return p.enabled ? "a" : "z";
    default: return "";
  }
}

export function AdminPromptsPage({ onNavigate }: Props) {
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [scopeFilter, setScopeFilter] = useState("");
  const [formMode, setFormMode] = useState<FormMode>("closed");
  const [form, setForm] = useState<FormData>(emptyForm);
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [mutationError, setMutationError] = useState<string | null>(null);
  const [sortBy, setSortBy] = useState<SortKey>("name");
  const [sortDir, setSortDir] = useState<SortDir>("asc");

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(timer);
  }, [search]);

  const { data, isLoading } = useAdminPrompts({
    search: debouncedSearch || undefined,
    scope: scopeFilter || undefined,
  });
  const createMutation = useCreateAdminPrompt();
  const updateMutation = useUpdateAdminPrompt();
  const deleteMutation = useDeleteAdminPrompt();

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

  const sorted = useMemo(() => {
    const list = [...(data?.data ?? [])];
    list.sort((a, b) => {
      const av = sortValue(a, sortBy);
      const bv = sortValue(b, sortBy);
      const cmp = av.localeCompare(bv);
      return sortDir === "asc" ? cmp : -cmp;
    });
    return list;
  }, [data, sortBy, sortDir]);

  function openCreate() {
    setForm(emptyForm);
    setFormMode("create");
    setExpandedId(null);
  }

  function openEdit(p: Prompt) {
    setForm({
      id: p.id,
      name: p.name,
      display_name: p.display_name,
      description: p.description,
      content: p.content,
      category: p.category,
      scope: p.scope,
      personas: (p.personas ?? []).join(", "),
      owner_email: p.owner_email,
      enabled: p.enabled,
    });
    setFormMode("edit");
  }

  function toggleExpand(id: string) {
    setExpandedId((prev) => (prev === id ? null : id));
  }

  const isMutating = createMutation.isPending || updateMutation.isPending || deleteMutation.isPending;

  function handleSubmit() {
    setMutationError(null);
    const personas = form.personas
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    const onError = (err: unknown) => {
      setMutationError(err instanceof Error ? err.message : "Operation failed");
    };

    if (formMode === "create") {
      createMutation.mutate(
        {
          name: form.name,
          display_name: form.display_name,
          description: form.description,
          content: form.content,
          category: form.category,
          scope: form.scope,
          personas,
          owner_email: form.owner_email,
          enabled: form.enabled,
        },
        { onSuccess: () => { setFormMode("closed"); setMutationError(null); }, onError },
      );
    } else if (formMode === "edit" && form.id) {
      updateMutation.mutate(
        {
          id: form.id,
          name: form.name,
          display_name: form.display_name,
          description: form.description,
          content: form.content,
          category: form.category,
          scope: form.scope,
          personas,
          owner_email: form.owner_email,
          enabled: form.enabled,
        },
        { onSuccess: () => { setFormMode("closed"); setMutationError(null); }, onError },
      );
    }
  }

  function handleDelete(id: string) {
    setMutationError(null);
    deleteMutation.mutate(id, {
      onSuccess: () => {
        setDeleteConfirm(null);
        if (expandedId === id) setExpandedId(null);
      },
      onError: (err) => {
        setMutationError(err instanceof Error ? err.message : "Delete failed");
      },
    });
  }

  function SortHeader({ col }: { col: typeof columns[number] }) {
    const active = sortBy === col.key;
    return (
      <th
        onClick={() => handleSort(col.key)}
        className={cn(
          "px-4 py-2 text-left font-medium text-muted-foreground cursor-pointer select-none hover:bg-muted/80",
          col.width,
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
        <div className="relative max-w-md flex-1">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search prompts..."
            className="w-full rounded-md border bg-background pl-9 pr-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          />
        </div>
        <select
          value={scopeFilter}
          onChange={(e) => setScopeFilter(e.target.value)}
          className="rounded-md border bg-background px-3 py-2 text-sm outline-none"
        >
          <option value="">All scopes</option>
          <option value="global">Global</option>
          <option value="persona">Persona</option>
          <option value="personal">Personal</option>
          <option value="system">System</option>
        </select>
        <button
          onClick={openCreate}
          className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
        >
          <Plus className="h-4 w-4" />
          New Prompt
        </button>
      </div>

      {/* Form */}
      {formMode !== "closed" && (
        <div className="rounded-lg border bg-card p-4 space-y-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold">
              {formMode === "create" ? "Create Prompt" : "Edit Prompt"}
            </h3>
            <button onClick={() => setFormMode("closed")} className="text-muted-foreground hover:text-foreground">
              <X className="h-4 w-4" />
            </button>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-xs text-muted-foreground">Name</label>
              <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" placeholder="my-prompt" />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">Display Name</label>
              <input value={form.display_name} onChange={(e) => setForm({ ...form, display_name: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" placeholder="My Prompt" />
            </div>
            <div className="col-span-2">
              <label className="text-xs text-muted-foreground">Description</label>
              <input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" placeholder="What this prompt does" />
            </div>
            <div className="col-span-2">
              <label className="text-xs text-muted-foreground">Content</label>
              <textarea value={form.content} onChange={(e) => setForm({ ...form, content: e.target.value })} rows={4} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none font-mono" placeholder="Prompt content with {arg} placeholders..." />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">Scope</label>
              <select value={form.scope} onChange={(e) => setForm({ ...form, scope: e.target.value as Prompt["scope"] })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none">
                <option value="global">Global</option>
                <option value="persona">Persona</option>
                <option value="personal">Personal</option>
              </select>
            </div>
            <div>
              <label className="text-xs text-muted-foreground">Category</label>
              <input value={form.category} onChange={(e) => setForm({ ...form, category: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" placeholder="workflow" />
            </div>
            {form.scope === "persona" && (
              <div className="col-span-2">
                <label className="text-xs text-muted-foreground">Personas (comma-separated)</label>
                <input value={form.personas} onChange={(e) => setForm({ ...form, personas: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" placeholder="analyst, data-engineer" />
              </div>
            )}
            <div>
              <label className="text-xs text-muted-foreground">Owner Email</label>
              <input value={form.owner_email} onChange={(e) => setForm({ ...form, owner_email: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" placeholder="user@example.com" />
            </div>
            <div className="flex items-center gap-2 pt-4">
              <button onClick={() => setForm({ ...form, enabled: !form.enabled })} className="text-muted-foreground hover:text-foreground">
                {form.enabled ? <ToggleRight className="h-5 w-5 text-green-500" /> : <ToggleLeft className="h-5 w-5" />}
              </button>
              <span className="text-xs text-muted-foreground">{form.enabled ? "Enabled" : "Disabled"}</span>
            </div>
          </div>
          {mutationError && (
            <div className="rounded-md bg-red-500/10 border border-red-500/20 px-3 py-2 text-xs text-red-400">{mutationError}</div>
          )}
          <div className="flex justify-end gap-2 pt-2">
            <button onClick={() => setFormMode("closed")} className="rounded-md border px-3 py-1.5 text-sm hover:bg-muted">Cancel</button>
            <button onClick={handleSubmit} disabled={!form.name || !form.content || isMutating} className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50">
              <Save className="h-3.5 w-3.5" />
              {isMutating ? "Saving..." : formMode === "create" ? "Create" : "Save"}
            </button>
          </div>
        </div>
      )}

      {/* Table */}
      {isLoading ? (
        <div className="flex items-center justify-center py-12 text-muted-foreground">Loading...</div>
      ) : sorted.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <MessageSquare className="h-12 w-12 mb-2 opacity-30" />
          <p className="text-sm font-medium">No prompts found</p>
        </div>
      ) : (
        <div className="rounded-lg border bg-card overflow-hidden">
          <table className="w-full text-sm">
            <thead className="border-b bg-muted/50">
              <tr>
                <th className="w-8 px-2" />
                {columns.map((col) => <SortHeader key={col.key} col={col} />)}
                <th className="px-4 py-2 text-right font-medium text-muted-foreground w-[80px]">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {sorted.map((p) => {
                const isExpanded = expandedId === p.id;
                return (
                  <>
                    <tr key={p.id} className="hover:bg-muted/30 cursor-pointer" onClick={() => toggleExpand(p.id)}>
                      <td className="px-2 py-2 text-muted-foreground">
                        {isExpanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                      </td>
                      <td className="px-4 py-2">
                        <div className="font-medium truncate">{p.display_name || p.name}</div>
                      </td>
                      <td className="px-4 py-2"><ScopeBadge scope={p.scope} /></td>
                      <td className="px-4 py-2 truncate text-muted-foreground">{p.description}</td>
                      <td className="px-4 py-2 text-xs text-muted-foreground truncate">{p.owner_email || "\u2014"}</td>
                      <td className="px-4 py-2">
                        <span className={cn("text-xs font-medium", p.enabled ? "text-green-500" : "text-zinc-500")}>
                          {p.enabled ? "Active" : "Disabled"}
                        </span>
                      </td>
                      <td className="px-4 py-2 text-right">
                        {p.scope === "system" ? (
                          <span className="text-[10px] text-muted-foreground">read-only</span>
                        ) : deleteConfirm === p.id ? (
                          <div className="inline-flex gap-1" onClick={(e) => e.stopPropagation()}>
                            <button onClick={() => handleDelete(p.id)} className="text-xs text-red-500 hover:text-red-400">Confirm</button>
                            <button onClick={() => setDeleteConfirm(null)} className="text-xs text-muted-foreground hover:text-foreground">Cancel</button>
                          </div>
                        ) : (
                          <div className="inline-flex gap-1" onClick={(e) => e.stopPropagation()}>
                            <button onClick={() => openEdit(p)} className="text-muted-foreground hover:text-foreground" title="Edit"><Edit className="h-3.5 w-3.5" /></button>
                            <button onClick={() => setDeleteConfirm(p.id)} className="text-muted-foreground hover:text-red-500" title="Delete"><Trash2 className="h-3.5 w-3.5" /></button>
                          </div>
                        )}
                      </td>
                    </tr>
                    {isExpanded && (
                      <tr key={`${p.id}-detail`} className="bg-muted/20">
                        <td colSpan={7} className="px-6 py-3">
                          <div className="space-y-2">
                            <div className="grid grid-cols-3 gap-4 text-xs">
                              <div><span className="text-muted-foreground">ID:</span> <span className="font-mono select-all">{p.id}</span></div>
                              <div><span className="text-muted-foreground">Name:</span> <span className="font-mono select-all">{p.name}</span></div>
                              <div><span className="text-muted-foreground">Category:</span> <span>{p.category || "\u2014"}</span></div>
                            </div>
                            {p.personas?.length > 0 && (
                              <div className="text-xs"><span className="text-muted-foreground">Personas:</span> {p.personas.join(", ")}</div>
                            )}
                            {p.arguments?.length > 0 && (
                              <div className="text-xs"><span className="text-muted-foreground">Arguments:</span> {p.arguments.map((a) => `{${a.name}}${a.required ? "*" : ""}`).join(", ")}</div>
                            )}
                            <div>
                              <div className="flex items-center justify-between mb-1">
                                <span className="text-xs text-muted-foreground">Prompt Content</span>
                                <button onClick={() => navigator.clipboard.writeText(p.content)} className="text-muted-foreground hover:text-foreground" title="Copy content"><Copy className="h-3 w-3" /></button>
                              </div>
                              <pre className="text-xs whitespace-pre-wrap font-mono bg-muted/50 rounded p-3 max-h-48 overflow-auto border">{p.content}</pre>
                            </div>
                          </div>
                        </td>
                      </tr>
                    )}
                  </>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
