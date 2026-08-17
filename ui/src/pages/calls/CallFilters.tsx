import { FilterSelect } from "@/components/patterns/FilterSelect";
import { SearchInput } from "@/components/patterns/SearchInput";
import type { AuditFiltersResponse, CallKind, CallOutcome } from "@/api/admin/types";
import { formatUser } from "@/lib/formatUser";
import { OUTCOME_DESCRIPTION } from "./outcome";

// CallFilters is the catalog's facet bar. Kind, connection and outcome are the
// three questions a reader narrows by; the queue toggle is the reviewer's view
// of the same list, and the search box matches what a person wrote (the
// purpose) as well as what ran (the statement).
//
// The caller's own list omits the user facet. The server scopes those reads to
// the authenticated caller and would ignore any other value, so offering the
// control would be offering one that cannot do anything.

export interface CallFilterState {
  kind: CallKind | "";
  connection: string;
  outcome: CallOutcome | "";
  userId: string;
  promotable: boolean;
  q: string;
}

export const NO_CALL_FILTERS: CallFilterState = {
  kind: "",
  connection: "",
  outcome: "",
  userId: "",
  promotable: false,
  q: "",
};

const OUTCOMES: CallOutcome[] = ["satisfied", "ran", "superseded", "failed"];

export function CallFilters({
  filters,
  connections,
  value,
  onChange,
  showUserFacet = true,
}: {
  /** The distinct users the audit index holds, for the caller facet. */
  filters?: AuditFiltersResponse;
  /** Connection names to offer as a facet. */
  connections?: string[];
  value: CallFilterState;
  onChange: (patch: Partial<CallFilterState>) => void;
  /** Whether to offer the caller facet. False on a reader's own calls. */
  showUserFacet?: boolean;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <SearchInput
        className="w-64"
        aria-label="Search calls by purpose or statement"
        placeholder="Search purpose or statement"
        value={value.q}
        onChange={(e) => onChange({ q: e.target.value })}
      />
      <FilterSelect
        label="Filter by kind"
        title="A query against a query engine, or an invocation through the API gateway."
        value={value.kind}
        onChange={(kind) => onChange({ kind: kind as CallKind | "" })}
        options={[
          { value: "", label: "All kinds" },
          { value: "sql", label: "SQL" },
          { value: "api", label: "API" },
        ]}
      />
      {(connections?.length ?? 0) > 0 && (
        <FilterSelect
          label="Filter by connection"
          value={value.connection}
          onChange={(connection) => onChange({ connection })}
          options={[
            { value: "", label: "All connections" },
            ...(connections ?? []).map((c) => ({ value: c, label: c })),
          ]}
        />
      )}
      <FilterSelect
        label="Filter by outcome"
        title={OUTCOME_DESCRIPTION.satisfied}
        value={value.outcome}
        onChange={(outcome) => onChange({ outcome: outcome as CallOutcome | "" })}
        options={[
          { value: "", label: "All outcomes" },
          ...OUTCOMES.map((o) => ({ value: o, label: o })),
        ]}
      />
      {showUserFacet && (
        <FilterSelect
          label="Filter by user"
          value={value.userId}
          onChange={(userId) => onChange({ userId })}
          options={[
            { value: "", label: "All Users" },
            ...(filters?.users ?? []).map((u) => ({
              value: u,
              label: filters?.user_labels?.[u] || formatUser(u),
            })),
          ]}
        />
      )}
      <FilterSelect
        label="Filter by review state"
        title="Awaiting review keeps the records that answered something and have not been promoted or declined, most re-run first."
        value={value.promotable ? "true" : ""}
        onChange={(v) => onChange({ promotable: v === "true" })}
        options={[
          { value: "", label: "Any review state" },
          { value: "true", label: "Awaiting review" },
        ]}
      />
    </div>
  );
}
