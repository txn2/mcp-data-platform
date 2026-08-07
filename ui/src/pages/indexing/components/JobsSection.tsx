import { useMemo, useState } from "react";
import { Activity } from "lucide-react";

import { type IndexJob, type IndexKindSummary } from "@/api/admin/indexjobs";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { JobTable } from "./jobtable";
import { Section } from "./panels";

// JOB_STATUSES is the drill-down's status facet, in queue order.
const JOB_STATUSES = ["pending", "running", "succeeded", "failed"];

// JOB_FETCH_LIMIT mirrors the `limit` the page requests. At the cap the table
// is a window on recent history rather than the whole of it, and the header
// says so.
const JOB_FETCH_LIMIT = 500;

// JobsSection is the dashboard's drill-down: every job the page fetched,
// narrowed by kind and state. The filters are its own state — nothing above
// reads them — so the page above stays a composition of panels.
export function JobsSection({
  jobs,
  kinds,
}: {
  jobs: IndexJob[];
  kinds: IndexKindSummary[];
}) {
  const [kindFilter, setKindFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");

  const filteredJobs = useMemo(
    () =>
      jobs.filter(
        (j) =>
          (kindFilter === "" || j.source_kind === kindFilter) &&
          (statusFilter === "" || j.status === statusFilter),
      ),
    [jobs, kindFilter, statusFilter],
  );

  const hint =
    jobs.length >= JOB_FETCH_LIMIT ? `${jobs.length} most recent` : `${jobs.length} jobs`;

  return (
    <Section title="Jobs" hint={hint}>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Activity className="h-4 w-4 text-muted-foreground" />
        <FilterSelect
          label="Filter by kind"
          value={kindFilter}
          onChange={setKindFilter}
          options={[
            { value: "", label: "All kinds" },
            ...kinds.map((k) => ({ value: k.kind, label: k.kind })),
          ]}
        />
        <FilterSelect
          label="Filter by status"
          value={statusFilter}
          onChange={setStatusFilter}
          options={[
            { value: "", label: "All statuses" },
            ...JOB_STATUSES.map((s) => ({ value: s, label: s })),
          ]}
        />
        <span className="text-[11px] text-muted-foreground">
          routine reconciler syncs are collapsed
        </span>
      </div>
      <JobTable jobs={filteredJobs} resetKey={`${kindFilter}::${statusFilter}`} />
    </Section>
  );
}
