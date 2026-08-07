import { useState, useEffect, useCallback, useMemo } from "react";
import { MessageSquare, Plus, Search } from "lucide-react";
import { useAdminPrompts, useDeleteAdminPrompt } from "@/api/admin/hooks";
import type { Prompt } from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { AdminPromptDialog } from "./admin/AdminPromptDialog";
import { AdminPromptsTable } from "./admin/AdminPromptsTable";
import { sortPrompts, type SortDir, type SortKey } from "./admin/adminPromptSort";
import { FormError, ListSkeleton } from "./primitives";
import { PromptReviewQueue } from "./PromptReviewQueue";

interface Props {
  onNavigate: (path: string) => void;
}

// ALL_SCOPES stands in for "no scope filter": a Select item cannot carry an
// empty value, which is what the unfiltered query parameter is.
const ALL_SCOPES = "__all__";

export function AdminPromptsPage({ onNavigate: _onNavigate }: Props) {
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [scopeFilter, setScopeFilter] = useState("");
  // editing holds the prompt the dialog is open on; "new" opens it empty.
  const [editing, setEditing] = useState<Prompt | "new" | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);
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

  const sorted = useMemo(
    () => sortPrompts(data?.data ?? [], sortBy, sortDir),
    [data, sortBy, sortDir],
  );

  const handleDelete = useCallback(
    (id: string) => {
      setDeleteError(null);
      deleteMutation.mutate(id, {
        onError: (err) => setDeleteError(err instanceof Error ? err.message : "Delete failed"),
      });
    },
    [deleteMutation],
  );

  return (
    <div className="space-y-4">
      {/* Pending promotion requests (renders only when non-empty) */}
      <PromptReviewQueue />

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative max-w-md flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search prompts..."
            className="pl-9"
          />
        </div>
        <Select
          value={scopeFilter || ALL_SCOPES}
          onValueChange={(v) => setScopeFilter(v === ALL_SCOPES ? "" : v)}
        >
          <SelectTrigger aria-label="Filter by scope">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL_SCOPES}>All scopes</SelectItem>
            <SelectItem value="global">Global</SelectItem>
            <SelectItem value="persona">Persona</SelectItem>
            <SelectItem value="personal">Personal</SelectItem>
            <SelectItem value="system">System</SelectItem>
          </SelectContent>
        </Select>
        <Button onClick={() => setEditing("new")}>
          <Plus /> New Prompt
        </Button>
      </div>

      <FormError message={deleteError} />

      {isLoading ? (
        <ListSkeleton />
      ) : sorted.length === 0 ? (
        <EmptyState icon={MessageSquare}>No prompts found</EmptyState>
      ) : (
        <AdminPromptsTable
          prompts={sorted}
          sortBy={sortBy}
          sortDir={sortDir}
          onSort={handleSort}
          onEdit={setEditing}
          onDelete={handleDelete}
        />
      )}

      {editing !== null && (
        <AdminPromptDialog
          key={editing === "new" ? "new" : editing.id}
          prompt={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  );
}
