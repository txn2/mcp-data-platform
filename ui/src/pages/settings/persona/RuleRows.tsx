import { useState } from "react";
import { ChevronDown, ChevronRight, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { matchPattern } from "./resolve";

// RuleRows is the list a Tools or Connections section edits (#2257): one row
// per tool or connection with the server's decision, and Allow / No rule /
// Deny, which write the row's own name to the persona's allow or deny list.
// Patterns are edited apart from the rows, in a collapsed panel, and a deny
// pattern still wins over an exact allow, which is why Allow is disabled with
// the pattern named on a row one matches.

export type RowRule = "allow" | "none" | "deny";

export interface RuleRow {
  name: string;
  /** What the row is: the connections a tool reaches, or a connection's kind. */
  detail: string;
  /** The server's decision, as a short phrase. */
  decision: string;
  /** Whether the persona's people can use it. */
  usable: boolean;
  /** Badges the section adds: scratch, channels, warnings. */
  badges?: { text: string; tone?: "warn" | "info" }[];
}

export interface PatternLists {
  allow: string[];
  deny: string[];
}

/** exactRule is the row's own entry on the persona's lists. */
export function exactRule(name: string, lists: PatternLists): RowRule {
  if (lists.deny.includes(name)) return "deny";
  if (lists.allow.includes(name)) return "allow";
  return "none";
}

/** blockingDeny is the deny pattern, other than the row's own name, that matches it. */
export function blockingDeny(name: string, lists: PatternLists): string | undefined {
  return lists.deny.find((p) => p !== name && matchPattern(p, name));
}

/** withRule is the lists after setting the row's own entry to rule. */
export function withRule(name: string, rule: RowRule, lists: PatternLists): PatternLists {
  const allow = lists.allow.filter((p) => p !== name);
  const deny = lists.deny.filter((p) => p !== name);
  if (rule === "allow") allow.push(name);
  if (rule === "deny") deny.push(name);
  return { allow, deny };
}

export function RuleRows({
  rows,
  lists,
  onChange,
  locked,
  lockedNote,
  isReadOnly,
  noun,
}: {
  rows: RuleRow[];
  lists: PatternLists;
  onChange: (lists: PatternLists) => void;
  /** Rows are shown but cannot be changed: an external persona keeps its rules. */
  locked: boolean;
  lockedNote?: string;
  isReadOnly: boolean;
  noun: string;
}) {
  const [filter, setFilter] = useState("");
  const q = filter.trim().toLowerCase();
  const shown = q ? rows.filter((r) => r.name.toLowerCase().includes(q) || r.detail.toLowerCase().includes(q)) : rows;
  return (
    <div className="space-y-3">
      <PatternPanel lists={lists} onChange={onChange} disabled={isReadOnly || locked} />
      {locked && lockedNote && <p className="text-xs text-muted-foreground">{lockedNote}</p>}
      <Input
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder={`Filter ${noun}`}
        aria-label={`Filter ${noun}`}
        className="h-8 max-w-xs text-xs"
      />
      {rows.length === 0 ? (
        <p className="text-xs text-muted-foreground">No {noun} are registered.</p>
      ) : (
        <ul className="divide-y rounded border">
          {shown.map((row) => (
            <Row
              key={row.name}
              row={row}
              rule={exactRule(row.name, lists)}
              blockedBy={blockingDeny(row.name, lists)}
              disabled={isReadOnly || locked}
              onRule={(rule) => onChange(withRule(row.name, rule, lists))}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

const RULE_LABEL: Record<RowRule, string> = { allow: "Allow", none: "No rule", deny: "Deny" };

function Row({
  row,
  rule,
  blockedBy,
  disabled,
  onRule,
}: {
  row: RuleRow;
  rule: RowRule;
  blockedBy: string | undefined;
  disabled: boolean;
  onRule: (rule: RowRule) => void;
}) {
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="font-mono text-xs">{row.name}</span>
          {row.badges?.map((b) => (
            <Badge
              key={b.text}
              variant="outline"
              className={cn("text-[10px]", b.tone === "warn" && "border-amber-500/40 text-amber-700 dark:text-amber-400")}
            >
              {b.text}
            </Badge>
          ))}
        </div>
        <p className="truncate text-[11px] text-muted-foreground">{row.detail}</p>
      </div>
      <span className={cn("text-[11px]", row.usable ? "text-emerald-700 dark:text-emerald-400" : "text-muted-foreground")}>
        {row.decision}
      </span>
      <div role="group" aria-label={`Rule for ${row.name}`} className="flex overflow-hidden rounded border">
        {(["allow", "none", "deny"] as const).map((r) => {
          const blocked = r === "allow" && !!blockedBy;
          return (
            <button
              key={r}
              type="button"
              disabled={disabled || blocked}
              aria-pressed={rule === r}
              title={blocked ? `Blocked by deny pattern ${blockedBy}` : undefined}
              onClick={() => onRule(r)}
              className={cn(
                "px-2 py-0.5 text-[11px] disabled:cursor-not-allowed disabled:opacity-50",
                rule === r ? "bg-muted font-medium" : "hover:bg-muted/50",
              )}
            >
              {RULE_LABEL[r]}
            </button>
          );
        })}
      </div>
      {blockedBy && (
        <span className="w-full text-[10px] text-muted-foreground sm:w-auto">Blocked by deny pattern {blockedBy}</span>
      )}
    </li>
  );
}

/** PatternPanel edits the glob patterns, collapsed by default. */
function PatternPanel({
  lists,
  onChange,
  disabled,
}: {
  lists: PatternLists;
  onChange: (lists: PatternLists) => void;
  disabled: boolean;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="rounded border">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-1.5 px-3 py-2 text-left text-xs font-medium"
      >
        {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        Pattern rules ({lists.allow.length} allow, {lists.deny.length} deny)
      </button>
      {open && (
        <div className="grid gap-4 border-t px-3 py-3 sm:grid-cols-2">
          <PatternList
            label="Allow"
            patterns={lists.allow}
            disabled={disabled}
            onChange={(allow) => onChange({ ...lists, allow })}
          />
          <PatternList
            label="Deny"
            help="A deny pattern wins over every allow."
            patterns={lists.deny}
            disabled={disabled}
            onChange={(deny) => onChange({ ...lists, deny })}
          />
        </div>
      )}
    </div>
  );
}

function PatternList({
  label,
  help,
  patterns,
  disabled,
  onChange,
}: {
  label: string;
  help?: string;
  patterns: string[];
  disabled: boolean;
  onChange: (patterns: string[]) => void;
}) {
  const [draft, setDraft] = useState("");
  const add = () => {
    const p = draft.trim();
    setDraft("");
    if (p && !patterns.includes(p)) onChange([...patterns, p]);
  };
  return (
    <div>
      <p className="text-xs font-medium">{label}</p>
      {help && <p className="text-[10px] text-muted-foreground">{help}</p>}
      <ul className="mt-1 flex flex-wrap gap-1">
        {patterns.length === 0 && <li className="text-[11px] italic text-muted-foreground">None.</li>}
        {patterns.map((p) => (
          <li key={p} className="flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 font-mono text-[11px]">
            {p}
            {!disabled && (
              <button type="button" aria-label={`Remove ${p}`} onClick={() => onChange(patterns.filter((x) => x !== p))}>
                <X className="size-3" />
              </button>
            )}
          </li>
        ))}
      </ul>
      {!disabled && (
        <div className="mt-2 flex gap-1">
          <Input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                add();
              }
            }}
            placeholder="trino_*"
            aria-label={`Add ${label.toLowerCase()} pattern`}
            className="h-7 font-mono text-xs"
          />
          <Button type="button" size="sm" variant="outline" className="h-7" onClick={add}>
            Add
          </Button>
        </div>
      )}
    </div>
  );
}
