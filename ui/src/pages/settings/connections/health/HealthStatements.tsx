import { Fragment, useState } from "react";
import { ChevronRight, Copy, Info } from "lucide-react";
import type { AppDBHealthReport, AppDBStatement } from "@/api/admin/hooks";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { formatSQL, ms, plural, sqlTokens } from "./healthFormat";

// Slowest statements (#2528): the application's statements pg_stat_statements
// ranks, one line each, sortable, each opening in place to the whole
// statement laid out and highlighted, with its totals. The platform's own
// statements are left out by the server, and the ones of other roles this
// connection cannot read are counted rather than listed.

type SortKey = "total" | "mean" | "calls";

const SORTS: { key: SortKey; label: string; by: (s: AppDBStatement) => number }[] = [
  { key: "total", label: "Total time", by: (s) => s.total_ms },
  { key: "mean", label: "Time per call", by: (s) => s.mean_ms },
  { key: "calls", label: "Calls", by: (s) => s.calls },
];

const PG_STAT_STATEMENTS_DOCS = "https://www.postgresql.org/docs/current/pgstatstatements.html";

export function SlowStatements({ r }: { r: AppDBHealthReport }) {
  const [sort, setSort] = useState<SortKey>("total");
  const [open, setOpen] = useState<string | null>(null);
  const st = r.statements;
  const by = SORTS.find((s) => s.key === sort)!.by;
  const rows = [...st.top].sort((a, b) => by(b) - by(a));
  const total = st.top.reduce((n, s) => n + s.total_ms, 0) || 1;

  return (
    <section className="space-y-2" aria-labelledby="health-statements-h" data-testid="health-statements">
      <div className="flex items-center justify-between gap-3">
        <h3 id="health-statements-h" className="text-sm font-semibold">
          Slowest statements
        </h3>
        {rows.length > 1 && (
          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            Sort by
            <Select value={sort} onValueChange={(v) => setSort(v as SortKey)}>
              <SelectTrigger aria-label="Sort by" size="sm" className="h-8 w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SORTS.map((s) => (
                  <SelectItem key={s.key} value={s.key}>
                    {s.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </label>
        )}
      </div>
      {!st.installed || st.note ? (
        <p className="text-sm text-muted-foreground">
          {sentence(st.note ?? "pg_stat_statements is not installed in this database")}{" "}
          <a className="text-primary hover:underline" href={PG_STAT_STATEMENTS_DOCS} target="_blank" rel="noreferrer">
            About pg_stat_statements
          </a>
        </p>
      ) : rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">The application has not run any statements yet.</p>
      ) : (
        <Table className="table-fixed">
          <TableHeader>
            <TableRow>
              <TableHead>Statement</TableHead>
              <TableHead className="w-20 text-right">Calls</TableHead>
              <TableHead className="w-36">Share of time</TableHead>
              <TableHead className="w-24 text-right">Per call</TableHead>
              <TableHead className="w-20 text-right">Rows</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((s) => (
              <StatementRow
                key={s.query_id}
                s={s}
                share={s.total_ms / total}
                open={open === s.query_id}
                onToggle={() => setOpen(open === s.query_id ? null : s.query_id)}
              />
            ))}
          </TableBody>
        </Table>
      )}
      {st.installed && !st.note && <StatementsNote hidden={st.hidden_other_roles ?? 0} />}
    </section>
  );
}

function sentence(s: string): string {
  const t = s.trim();
  return t.endsWith(".") ? t : `${t[0]?.toUpperCase() ?? ""}${t.slice(1)}.`;
}

function StatementsNote({ hidden }: { hidden: number }) {
  return (
    <p className="flex gap-2 text-xs text-muted-foreground" data-testid="health-statements-note">
      <Info aria-hidden="true" className="mt-px size-3.5 shrink-0" />
      <span>
        Showing the application&apos;s statements only; the platform&apos;s own health checks are left out.
        {hidden > 0 &&
          ` ${plural(hidden, "statement")} from other roles ${hidden === 1 ? "is" : "are"} hidden because this connection's role cannot read their text. Grant it pg_read_all_stats to show them.`}
      </span>
    </p>
  );
}

function StatementRow({ s, share, open, onToggle }: { s: AppDBStatement; share: number; open: boolean; onToggle: () => void }) {
  const perRow = s.calls > 0 ? Math.round(s.rows / s.calls) : 0;
  return (
    <Fragment>
      <TableRow className="cursor-pointer" onClick={onToggle} data-testid={`statement-${s.query_id}`}>
        <TableCell className="max-w-0">
          <button
            type="button"
            aria-expanded={open}
            className="flex w-full min-w-0 items-center gap-1.5 text-left"
            onClick={(e) => {
              e.stopPropagation();
              onToggle();
            }}
          >
            <ChevronRight aria-hidden="true" className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")} />
            <span className="truncate font-mono text-xs">{s.query.replace(/\s+/g, " ")}</span>
          </button>
        </TableCell>
        <TableCell className="text-right tabular-nums">{s.calls.toLocaleString()}</TableCell>
        <TableCell>
          <div className="flex items-center gap-2">
            <div className="h-1.5 flex-1 rounded-full bg-muted" aria-hidden="true">
              <div className="h-full rounded-full bg-primary" style={{ width: `${Math.round(share * 100)}%` }} />
            </div>
            <span className="w-9 text-right text-xs tabular-nums">{Math.round(share * 100)}%</span>
          </div>
        </TableCell>
        <TableCell className="text-right tabular-nums">{ms(s.mean_ms)}</TableCell>
        <TableCell className="text-right tabular-nums">{s.rows.toLocaleString()}</TableCell>
      </TableRow>
      {open && (
        <TableRow className="hover:bg-transparent">
          <TableCell colSpan={5} className="whitespace-normal">
            <div className="relative rounded-lg border bg-muted/30 p-3">
              <Button
                size="sm"
                variant="ghost"
                className="absolute top-2 right-2"
                onClick={() => void navigator.clipboard?.writeText(s.query).catch(() => undefined)}
              >
                <Copy aria-hidden="true" />
                Copy
              </Button>
              <pre className="pr-20 font-mono text-xs leading-relaxed whitespace-pre-wrap" data-testid="statement-full">
                {sqlTokens(formatSQL(s.query)).map((t, i) =>
                  t.keyword ? (
                    <span key={i} className="font-semibold text-primary">
                      {t.text}
                    </span>
                  ) : (
                    <Fragment key={i}>{t.text}</Fragment>
                  ),
                )}
              </pre>
              <p className="mt-2 text-xs text-muted-foreground">
                {ms(s.total_ms)} total · Slowest call {ms(s.max_ms ?? 0)} · {plural(perRow, "row")} per call ·{" "}
                {plural(s.calls, "call")}
              </p>
            </div>
          </TableCell>
        </TableRow>
      )}
    </Fragment>
  );
}
