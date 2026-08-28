import { FilterSelect } from "@/components/patterns/FilterSelect";
import type { AuditFiltersResponse, SessionKind } from "@/api/admin/types";
import { principalOptions } from "@/lib/formatUser";
import { kindLabel, SESSION_KINDS } from "./kind";
import {
  DEFAULT_SESSION_WINDOW,
  SESSION_WINDOW_OPTIONS,
  type SessionWindow,
} from "./window";

// SessionFilters is the session list's facet bar. The user facet reuses the
// audit index's distinct users, which is where a session's caller comes from
// in the first place; the other three are the session-level questions the
// event list cannot answer.
//
// The caller's own list omits the user facet. The server scopes those reads to
// the authenticated caller and would ignore any other value, so offering the
// control would be offering one that cannot do anything.

export interface SessionFilterState {
  // window bounds the events the rollup reads. It is a filter like the
  // others rather than a hidden default, so widening it is the reader's
  // choice and nothing is silently withheld.
  window: SessionWindow;
  userId: string;
  kind: SessionKind | "";
  hasAssets: boolean;
  hasFailures: boolean;
}

export const NO_SESSION_FILTERS: SessionFilterState = {
  window: DEFAULT_SESSION_WINDOW,
  userId: "",
  kind: "",
  hasAssets: false,
  hasFailures: false,
};

export function SessionFilters({
  filters,
  value,
  onChange,
  showUserFacet = true,
}: {
  // The distinct users the audit index holds, for the caller facet.
  filters?: AuditFiltersResponse;
  value: SessionFilterState;
  onChange: (patch: Partial<SessionFilterState>) => void;
  /** Whether to offer the caller facet. False on a reader's own sessions. */
  showUserFacet?: boolean;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <FilterSelect
        label="Filter by time window"
        title="Sessions with activity inside this window. The list rolls up every event in range, so a wider window is a heavier query."
        value={value.window}
        onChange={(w) => onChange({ window: w as SessionWindow })}
        options={SESSION_WINDOW_OPTIONS}
      />
      {showUserFacet && (
        <FilterSelect
          label="Filter by user"
          className="max-w-56"
          value={value.userId}
          onChange={(userId) => onChange({ userId })}
          options={[
            { value: "", label: "All Users" },
            ...principalOptions(filters?.users, filters?.user_labels),
          ]}
        />
      )}
      <FilterSelect
        label="Filter by session kind"
        title="Where the session id came from: an agent's handle, a portal run, a script run, or a transport session."
        value={value.kind}
        onChange={(kind) => onChange({ kind: kind as SessionKind | "" })}
        options={[
          { value: "", label: "All Kinds" },
          ...SESSION_KINDS.map((k) => ({ value: k, label: kindLabel(k) })),
        ]}
      />
      <FilterSelect
        label="Filter by outcome"
        value={value.hasFailures ? "true" : ""}
        onChange={(v) => onChange({ hasFailures: v === "true" })}
        options={[
          { value: "", label: "Any Outcome" },
          { value: "true", label: "With Failures" },
        ]}
      />
      <FilterSelect
        label="Filter by output"
        title="Sessions that saved at least one asset."
        value={value.hasAssets ? "true" : ""}
        onChange={(v) => onChange({ hasAssets: v === "true" })}
        options={[
          { value: "", label: "Any Output" },
          { value: "true", label: "Produced Assets" },
        ]}
      />
    </div>
  );
}
