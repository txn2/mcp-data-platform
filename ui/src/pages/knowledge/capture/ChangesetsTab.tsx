import { useState, useMemo } from "react";
import { useChangesets, useAuditFilters } from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { Changeset } from "@/api/admin/types";
import { formatUser } from "@/lib/formatUser";
import { ChangesetDrawer } from "./ChangesetDrawer";
import { PER_PAGE, formatCategory } from "./helpers";

export function ChangesetsTab() {
  const [page, setPage] = useState(1);
  const [entityUrnFilter, setEntityUrnFilter] = useState("");
  const [rolledBackFilter, setRolledBackFilter] = useState("");
  const [selectedChangeset, setSelectedChangeset] = useState<Changeset | null>(
    null,
  );
  const { data: filters } = useAuditFilters();
  const ul = filters?.user_labels ?? {};

  const params = useMemo(
    () => ({
      page,
      perPage: PER_PAGE,
      entityUrn: entityUrnFilter || undefined,
      rolledBack: rolledBackFilter || undefined,
    }),
    [page, entityUrnFilter, rolledBackFilter],
  );

  const { data, isLoading } = useChangesets(params);
  const totalPages = data ? Math.ceil(data.total / PER_PAGE) : 0;

  return (
    <>
      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
        <input
          type="text"
          value={entityUrnFilter}
          onChange={(e) => {
            setEntityUrnFilter(e.target.value);
            setPage(1);
          }}
          placeholder="Filter by Entity URN..."
          className="w-64 rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
        <select
          value={rolledBackFilter}
          onChange={(e) => {
            setRolledBackFilter(e.target.value);
            setPage(1);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="">All</option>
          <option value="false">Active</option>
          <option value="true">Rolled Back</option>
        </select>
      </div>

      {/* Table */}
      <div className="overflow-auto rounded-lg border bg-card">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="px-3 py-2 text-left font-medium">Created At</th>
              <th className="px-3 py-2 text-left font-medium">Target URN</th>
              <th className="px-3 py-2 text-left font-medium">Change Type</th>
              <th className="px-3 py-2 text-left font-medium">Applied By</th>
              <th className="px-3 py-2 text-center font-medium">Status</th>
            </tr>
          </thead>
          <tbody>
            {isLoading && (
              <tr>
                <td
                  colSpan={5}
                  className="px-3 py-8 text-center text-muted-foreground"
                >
                  Loading...
                </td>
              </tr>
            )}
            {data?.data.map((changeset) => (
              <tr
                key={changeset.id}
                onClick={() => setSelectedChangeset(changeset)}
                className="cursor-pointer border-b transition-colors hover:bg-muted/50"
              >
                <td className="px-3 py-2 text-xs">
                  {new Date(changeset.created_at).toLocaleString()}
                </td>
                <td className="max-w-xs truncate px-3 py-2 font-mono text-xs">
                  {changeset.target_urn}
                </td>
                <td className="px-3 py-2 text-xs">
                  {formatCategory(changeset.change_type)}
                </td>
                <td
                  className="px-3 py-2 text-xs"
                  title={changeset.applied_by}
                >
                  {formatUser(changeset.applied_by, ul[changeset.applied_by])}
                </td>
                <td className="px-3 py-2 text-center">
                  <StatusBadge
                    variant={changeset.rolled_back ? "error" : "success"}
                  >
                    {changeset.rolled_back ? "Rolled Back" : "Active"}
                  </StatusBadge>
                </td>
              </tr>
            ))}
            {data?.data.length === 0 && (
              <tr>
                <td
                  colSpan={5}
                  className="px-3 py-8 text-center text-muted-foreground"
                >
                  No changesets found
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted-foreground">
            Showing {(page - 1) * PER_PAGE + 1}-
            {Math.min(page * PER_PAGE, data?.total ?? 0)} of{" "}
            {data?.total ?? 0}
          </span>
          <div className="flex gap-2">
            <button
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page <= 1}
              className="rounded-md border px-3 py-1 text-xs disabled:opacity-50"
            >
              Previous
            </button>
            <button
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page >= totalPages}
              className="rounded-md border px-3 py-1 text-xs disabled:opacity-50"
            >
              Next
            </button>
          </div>
        </div>
      )}

      {/* Detail Drawer */}
      {selectedChangeset && (
        <ChangesetDrawer
          changeset={selectedChangeset}
          onClose={() => setSelectedChangeset(null)}
          userLabels={ul}
        />
      )}
    </>
  );
}
