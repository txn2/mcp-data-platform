import { useMemo, useState } from "react";
import { MessageSquareWarning } from "lucide-react";
import {
  PLATFORM_FEEDBACK_STATUSES,
  usePlatformFeedbackList,
  usePlatformFeedbackSettings,
  type PlatformFeedbackItem,
  type PlatformFeedbackStats,
} from "@/api/admin/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SearchInput } from "@/components/patterns/SearchInput";
import { relativeTime } from "@/components/provenance/parts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { sortNames } from "@/lib/sortByName";
import { STATUS_LABELS, statusLabel, statusVariant } from "./platformFeedbackFormat";

const PAGE = 50;

interface Filter {
  q: string;
  status: string;
  tool: string;
}

// PlatformFeedbackPage lists what agents reported the platform could not do
// (#2820). A repeat of an open post is counted on it, so each row is one
// problem with how many times it was reported.
export function PlatformFeedbackPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const [filter, setFilter] = useState<Filter>({ q: "", status: "", tool: "" });
  const [page, setPage] = useState(0);
  const { data, isLoading, isError } = usePlatformFeedbackList({ ...filter, limit: PAGE, offset: page * PAGE });
  const enabled = usePlatformFeedbackSettings().data?.enabled;
  const posts = useMemo(() => data?.posts ?? [], [data]);
  const change = (patch: Partial<Filter>) => {
    setFilter((f) => ({ ...f, ...patch }));
    setPage(0);
  };
  const filtered = filter.q !== "" || filter.status !== "" || filter.tool !== "";
  const total = data?.total ?? posts.length;

  let body: React.ReactNode;
  if (isError) {
    body = (
      <Alert variant="destructive">
        <AlertDescription>Platform feedback could not be listed.</AlertDescription>
      </Alert>
    );
  } else if (isLoading) {
    body = <p className="text-sm text-muted-foreground">Loading...</p>;
  } else if (posts.length === 0) {
    body = <EmptyState icon={MessageSquareWarning}>{emptyMessage(filtered, enabled)}</EmptyState>;
  } else {
    body = (
      <>
        <PostsTable posts={posts} onOpen={(id) => onNavigate(`/admin/platform-feedback/${encodeURIComponent(id)}`)} />
        <Pager shown={posts.length} total={total} page={page} onPage={setPage} />
      </>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          What agents reported the platform could not do. Repeats of an open post are counted on it.
        </p>
        <Button variant="link" size="sm" className="h-auto p-0" onClick={() => onNavigate("/admin/settings/platform-feedback")}>
          Settings
        </Button>
      </div>
      <Stats stats={data?.stats} />
      <Filters filter={filter} posts={posts} onChange={change} />
      {body}
    </div>
  );
}

function emptyMessage(filtered: boolean, enabled: boolean | undefined): string {
  if (filtered) return "No post matches.";
  if (enabled === false) return "No agent has reported anything yet. Platform feedback is off: turn it on in Settings.";
  return "No agent has reported anything yet.";
}

function Stats({ stats: st }: { stats?: PlatformFeedbackStats }) {
  const n = (v: number | undefined) => (v === undefined ? "-" : v.toLocaleString());
  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
      <StatCard label="Open" value={n(st?.open)} />
      <StatCard label="Reports this week" value={n(st?.reports_this_week)} />
      <StatCard label="Refused" value={n(st?.refused_this_week)} detail="held a secret or past a limit, this week" />
      <StatCard label="Delivery failures" value={n(st?.delivery_failures)} />
    </div>
  );
}

function Filters({ filter, posts, onChange }: { filter: Filter; posts: PlatformFeedbackItem[]; onChange: (patch: Partial<Filter>) => void }) {
  // The tools the listed posts name, and the one chosen though no post names it.
  const tools = sortNames(new Set([...posts.map((p) => p.tool).filter(Boolean), ...(filter.tool ? [filter.tool] : [])]));
  return (
    <div className="flex flex-wrap gap-2">
      <SearchInput
        aria-label="Search posts"
        placeholder="Search posts"
        value={filter.q}
        onChange={(e) => onChange({ q: e.target.value })}
        className="w-full max-w-xs"
      />
      <select
        aria-label="Status"
        className="h-9 rounded-md border bg-background px-2 text-sm"
        value={filter.status}
        onChange={(e) => onChange({ status: e.target.value })}
      >
        <option value="">All statuses</option>
        {PLATFORM_FEEDBACK_STATUSES.map((s) => (
          <option key={s} value={s}>
            {STATUS_LABELS[s]}
          </option>
        ))}
      </select>
      <select
        aria-label="Tool"
        className="h-9 rounded-md border bg-background px-2 text-sm"
        value={filter.tool}
        onChange={(e) => onChange({ tool: e.target.value })}
      >
        <option value="">All tools</option>
        {tools.map((t) => (
          <option key={t} value={t}>
            {t}
          </option>
        ))}
      </select>
    </div>
  );
}

function PostsTable({ posts, onOpen }: { posts: PlatformFeedbackItem[]; onOpen: (id: string) => void }) {
  return (
    <div className="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Trying to</TableHead>
            <TableHead>Blocked by</TableHead>
            <TableHead>Tool</TableHead>
            <TableHead>Count</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Last reported</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {posts.map((p) => (
            <TableRow key={p.id}>
              <TableCell className="max-w-xs">
                <button type="button" className="text-left text-primary hover:underline" onClick={() => onOpen(p.id)}>
                  {p.trying_to}
                </button>
              </TableCell>
              <TableCell className="max-w-sm text-sm">{p.blocked_by}</TableCell>
              <TableCell className="font-mono text-xs">
                {p.tool || <span className="font-sans text-muted-foreground">None</span>}
              </TableCell>
              <TableCell className="font-semibold tabular-nums">{p.report_count}</TableCell>
              <TableCell>
                <Badge variant={statusVariant(p.status)}>{statusLabel(p.status)}</Badge>
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">{relativeTime(p.last_reported_at)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function Pager({ shown, total, page, onPage }: { shown: number; total: number; page: number; onPage: (p: number) => void }) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-xs text-muted-foreground">
        Showing {shown} of {total}
      </span>
      <div className="flex gap-2">
        <Button size="sm" variant="outline" disabled={page === 0} onClick={() => onPage(page - 1)}>
          Previous
        </Button>
        <Button size="sm" variant="outline" disabled={(page + 1) * PAGE >= total} onClick={() => onPage(page + 1)}>
          Next
        </Button>
      </div>
    </div>
  );
}
