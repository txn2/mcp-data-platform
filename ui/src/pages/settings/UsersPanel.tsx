import { useState, useCallback } from "react";
import {
  useInfiniteDirectoryUsers,
  useDeleteUser,
  useSystemInfo,
} from "@/api/admin/hooks";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Plus, Contact, ChevronUp, Search } from "lucide-react";
import { PanelShell } from "./panels";
import { AddUserForm } from "./users/AddUserForm";
import { UserRow } from "./users/UserRow";

// ---------------------------------------------------------------------------
// UsersPanel — directory of known people (#614)
// ---------------------------------------------------------------------------

export function UsersPanel() {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";

  const [query, setQuery] = useState("");
  // The directory paginates so a deployment with more than one page of users can
  // reach all of them (#972); `query` narrows server-side.
  const { data, isLoading, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useInfiniteDirectoryUsers(query.trim() || undefined);
  const users = data?.data ?? [];

  const [showForm, setShowForm] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);
  const deleteMutation = useDeleteUser();

  const handleDelete = useCallback(
    (email: string) => {
      deleteMutation.mutate(email, { onSuccess: () => setDeleteConfirm(null) });
    },
    [deleteMutation],
  );

  return (
    <PanelShell
      title="Users"
      description="People known to the platform, for sharing assets and prompts. Anyone who signs in is recorded automatically; add others by email so they can be shared with before they log in."
      action={
        !isReadOnly && (
          <Button
            type="button"
            size="sm"
            variant={showForm ? "outline" : "default"}
            onClick={() => setShowForm((prev) => !prev)}
          >
            {showForm ? (
              <>
                <ChevronUp />
                Cancel
              </>
            ) : (
              <>
                <Plus />
                Add User
              </>
            )}
          </Button>
        )
      }
    >
      {showForm && <AddUserForm onDone={() => setShowForm(false)} />}

      <div className="border-b px-5 py-2">
        <div className="relative max-w-sm">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search by name or email"
            aria-label="Search users"
            className="h-8 pl-8"
          />
        </div>
      </div>

      <div className="flex-1 overflow-auto">
        {isLoading ? (
          <p className="py-16 text-center text-sm text-muted-foreground">
            Loading...
          </p>
        ) : users.length === 0 ? (
          <div className="p-5">
            <EmptyState icon={Contact}>
              <p>{query ? "No matching users" : "No users yet"}</p>
              {!isReadOnly && !query && (
                <p className="mt-1 text-xs">
                  People appear here as they sign in, or add one above.
                </p>
              )}
            </EmptyState>
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/30 text-xs text-muted-foreground hover:bg-muted/30">
                <TableHead className="px-5">Name</TableHead>
                <TableHead className="px-5">Email</TableHead>
                <TableHead className="px-5">Status</TableHead>
                <TableHead className="px-5">Last Seen</TableHead>
                {!isReadOnly && <TableHead className="w-28 px-5">Actions</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.map((u) => (
                <UserRow
                  key={u.email}
                  user={u}
                  isReadOnly={isReadOnly}
                  deleteConfirm={deleteConfirm === u.email}
                  deleting={deleteMutation.isPending}
                  onRequestDelete={() => setDeleteConfirm(u.email)}
                  onCancelDelete={() => setDeleteConfirm(null)}
                  onConfirmDelete={() => handleDelete(u.email)}
                />
              ))}
            </TableBody>
          </Table>
        )}
        {users.length > 0 && (
          <div className="p-3">
            <InfiniteFooter
              hasMore={hasNextPage}
              isLoadingMore={isFetchingNextPage}
              onLoadMore={fetchNextPage}
            />
          </div>
        )}
      </div>
    </PanelShell>
  );
}
