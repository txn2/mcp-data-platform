import { useState } from "react";
import type { ApplicationDiagram, ApplicationDiagramNode } from "@/api/portal/hooks/applications";
import { Button } from "@/components/ui/button";
import {
  accessLabel,
  cellText,
  count,
  reads,
  scriptsOf,
  tablesOf,
  writes,
  type Access,
  type Grouping,
  type Reach,
} from "./diagramScale";

// DiagramTableView is a large application as a table (#2700): its automations
// as rows and its tables as columns, each cell saying whether the automation
// reads (R) or writes (W) the table, and under each column how many write it,
// a table nothing writes flagged. Selecting a row's or a column's heading
// opens what it is in the side column. On a phone the same graph is a list,
// grouped by automation or by table.

interface Props {
  diagram: ApplicationDiagram;
  reach: Reach;
  unwritten: Set<string>;
  selected: string | null;
  phone: boolean;
  onSelect: (id: string | null) => void;
}

export function DiagramTableView(props: Props) {
  return props.phone ? <TableList {...props} /> : <Matrix {...props} />;
}

const CELL_CLASS: Record<Access, string> = {
  reads: "bg-blue-50 font-semibold text-blue-700 dark:bg-blue-500/10 dark:text-blue-300",
  writes: "bg-orange-100 font-bold text-orange-800 dark:bg-orange-500/15 dark:text-orange-300",
  both: "bg-orange-100 font-bold text-orange-800 dark:bg-orange-500/15 dark:text-orange-300",
};

function Matrix({ diagram, reach, unwritten, selected, onSelect }: Props) {
  const scripts = scriptsOf(diagram);
  const tables = tablesOf(diagram);
  return (
    <div className="flex flex-col gap-2" data-testid="diagram-table-view">
      <div className="max-h-[640px] overflow-auto rounded-md border bg-card">
        <table className="border-collapse text-xs" aria-label="Automations by tables">
          <thead className="sticky top-0 z-10 bg-card">
            <tr>
              <th scope="col" className="sticky left-0 z-20 bg-card px-3 py-2 text-left text-[10.5px] font-semibold tracking-wide text-muted-foreground uppercase">
                Automation ↓ · Table →
              </th>
              {tables.map((t) => (
                <th key={t.id} scope="col" className={`h-28 w-[46px] border-l align-bottom ${unwritten.has(t.id) ? "text-amber-800 dark:text-amber-300" : ""}`}>
                  <HeadingButton node={t} selected={selected === t.id} vertical onSelect={onSelect} />
                </th>
              ))}
              <th scope="col" className="border-l px-2 text-left text-[10.5px] font-medium text-muted-foreground">
                Reads · writes
              </th>
            </tr>
          </thead>
          <tbody>
            {scripts.map((s) => {
              const m = reach.byScript.get(s.id);
              return (
                <tr key={s.id} className={`border-t ${selected === s.id ? "bg-amber-50 dark:bg-amber-500/10" : ""}`} data-row={s.id}>
                  <th scope="row" className="sticky left-0 bg-card px-1 text-left font-normal">
                    <HeadingButton node={s} selected={selected === s.id} onSelect={onSelect} />
                  </th>
                  {tables.map((t) => (
                    <Cell key={t.id} access={m?.get(t.id)} lit={selected === t.id} label={`${s.label}, ${t.label}`} />
                  ))}
                  <td className="border-l px-2 whitespace-nowrap text-muted-foreground">
                    {count(m, reads)} · {count(m, writes)}
                  </td>
                </tr>
              );
            })}
          </tbody>
          <tfoot>
            <tr className="border-t-2">
              <th scope="row" className="sticky left-0 bg-card px-3 py-1.5 text-left text-[11px] font-medium text-muted-foreground">
                Written by
              </th>
              {tables.map((t) => {
                const n = count(reach.byTable.get(t.id), writes);
                return (
                  <td
                    key={t.id}
                    className={`border-l text-center text-[11px] ${n === 0 ? "bg-amber-50 font-bold text-amber-800 dark:bg-amber-500/10 dark:text-amber-300" : "text-muted-foreground"}`}
                    data-written-by={t.id}
                  >
                    {n === 0 ? "none" : n}
                  </td>
                );
              })}
              <td className="border-l" />
            </tr>
          </tfoot>
        </table>
      </div>
      <p className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span>R reads</span>
        <span>W writes</span>
        <span className="text-amber-800 dark:text-amber-300">none: a table no part writes</span>
        <span>Select an automation or a table to open what it is.</span>
      </p>
    </div>
  );
}

function Cell({ access, lit, label }: { access?: Access; lit: boolean; label: string }) {
  const text = cellText(access);
  return (
    <td
      className={`h-[26px] border-l text-center text-[10.5px] ${access ? CELL_CLASS[access] : ""} ${lit ? "ring-1 ring-amber-400 ring-inset" : ""}`}
      aria-label={access ? `${label}: ${accessLabel(access)}` : undefined}
    >
      {text}
    </td>
  );
}

function HeadingButton({
  node,
  selected,
  vertical = false,
  onSelect,
}: {
  node: ApplicationDiagramNode;
  selected: boolean;
  vertical?: boolean;
  onSelect: (id: string | null) => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={selected}
      className={`max-w-[176px] truncate rounded px-2 py-1 text-left hover:bg-accent ${selected ? "font-semibold" : ""} ${
        vertical ? "max-h-28 [writing-mode:vertical-rl] rotate-180" : "w-[176px]"
      }`}
      onClick={() => onSelect(selected ? null : node.id)}
    >
      {node.label}
    </button>
  );
}

// TableList is the phone's Table view: one entry per automation, or per
// table, opened to say what it reaches and how.
function TableList({ diagram, reach, unwritten, selected, onSelect }: Props) {
  const [grouping, setGrouping] = useState<Grouping>("script");
  const nodes = grouping === "script" ? scriptsOf(diagram) : tablesOf(diagram);
  const byId = new Map(diagram.nodes.map((n) => [n.id, n]));
  return (
    <div className="flex flex-col gap-2" data-testid="diagram-table-view">
      <div role="group" aria-label="Group by" className="flex gap-1 self-start rounded-md border p-0.5">
        {(["script", "table"] as const).map((g) => (
          <Button key={g} type="button" size="sm" variant={grouping === g ? "secondary" : "ghost"} aria-pressed={grouping === g} onClick={() => setGrouping(g)}>
            {g === "script" ? "By automation" : "By table"}
          </Button>
        ))}
      </div>
      <ul className="divide-y rounded-md border bg-card">
        {nodes.map((n) => {
          const m = (grouping === "script" ? reach.byScript : reach.byTable).get(n.id);
          const open = selected === n.id;
          return (
            <li key={n.id}>
              <button
                type="button"
                aria-expanded={open}
                className="flex w-full items-center gap-2 px-3 py-2.5 text-left text-sm"
                onClick={() => onSelect(open ? null : n.id)}
              >
                <span className="min-w-0 flex-1 truncate font-medium">{n.label}</span>
                <span className={`text-xs ${unwritten.has(n.id) ? "font-semibold text-amber-800 dark:text-amber-300" : "text-muted-foreground"}`}>
                  {listNote(grouping, m, unwritten.has(n.id))}
                </span>
              </button>
              {open && <ListDetail grouping={grouping} reach={m} byId={byId} />}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function listNote(grouping: Grouping, m: Map<string, Access> | undefined, unwritten: boolean): string {
  if (grouping === "script") return `reads ${count(m, reads)} · writes ${count(m, writes)}`;
  if (unwritten) return `no writer · read by ${count(m, reads)}`;
  return `written by ${count(m, writes)} · read by ${count(m, reads)}`;
}

function ListDetail({
  grouping,
  reach,
  byId,
}: {
  grouping: Grouping;
  reach: Map<string, Access> | undefined;
  byId: Map<string, ApplicationDiagramNode>;
}) {
  const names = (test: (a: Access) => boolean) =>
    [...(reach ?? new Map<string, Access>())].filter(([, a]) => test(a)).map(([id]) => byId.get(id)?.label ?? id);
  const sections =
    grouping === "script"
      ? [{ title: "Writes", items: names(writes) }, { title: "Reads", items: names(reads) }]
      : [{ title: "Written by", items: names(writes) }, { title: "Read by", items: names(reads) }];
  return (
    <div className="space-y-2 px-3 pb-3 text-sm">
      {sections.map((s) => (
        <section key={s.title} aria-label={s.title}>
          <h4 className="text-xs font-medium text-muted-foreground">{s.title}</h4>
          <p>{s.items.length ? s.items.join(", ") : "Nothing"}</p>
        </section>
      ))}
    </div>
  );
}
