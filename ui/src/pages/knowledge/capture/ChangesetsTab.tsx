import { useState, useMemo } from "react";
import { useChangesets, useAuditFilters } from "@/api/admin/hooks";
import type { Changeset } from "@/api/admin/types";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { Pager } from "@/components/patterns/Pager";
import { SearchInput } from "@/components/patterns/SearchInput";
import { ChangesetDrawer } from "./ChangesetDrawer";
import { ChangesetsTable } from "./ChangesetsTable";
import { PER_PAGE } from "./helpers";

const ROLLED_BACK_OPTIONS = [
  { value: "", label: "All" },
  { value: "false", label: "Active" },
  { value: "true", label: "Rolled Back" },
];

export function ChangesetsTab() {
  const [page, setPage] = useState(1);
  const [entityUrnFilter, setEntityUrnFilter] = useState("");
  const [rolledBackFilter, setRolledBackFilter] = useState("");
  const [selectedChangeset, setSelectedChangeset] = useState<Changeset | null>(null);
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
  const total = data?.total ?? 0;

  // Either facet restarts the list at its first page: page 4 of the old filter
  // is not page 4 of the new one.
  const onFilter = (set: (v: string) => void) => (v: string) => {
    set(v);
    setPage(1);
  };

  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        <SearchInput
          className="w-64"
          inputClassName="h-8"
          value={entityUrnFilter}
          onChange={(e) => onFilter(setEntityUrnFilter)(e.target.value)}
          placeholder="Filter by Entity URN..."
          aria-label="Filter by entity URN"
        />
        <FilterSelect
          label="Filter by rollback state"
          value={rolledBackFilter}
          onChange={onFilter(setRolledBackFilter)}
          options={ROLLED_BACK_OPTIONS}
        />
      </div>

      <ChangesetsTable
        changesets={data?.data}
        loading={isLoading}
        userLabels={ul}
        onSelect={setSelectedChangeset}
      />

      {total > PER_PAGE && (
        <Pager page={page} perPage={PER_PAGE} total={total} onPage={setPage} />
      )}

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
