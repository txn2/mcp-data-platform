import { useEffect, useId, useMemo, useState, type KeyboardEvent, type RefObject } from "react";
import { Search, Users } from "lucide-react";
import { useDirectoryUsers, type SharePersona } from "@/api/portal/hooks";
import { Avatar } from "@/components/ui/avatar";
import { parseEmailAddress } from "@/lib/emailAddress";
import { cn } from "@/lib/utils";
import { GuestPill, fullName } from "./PeopleWithAccess";
import { GUEST_TINT, highlight, initials, tintFor } from "./model";

/** Someone a share can be addressed to: a person, a group, or an address with no account. */
export interface Recipient {
  key: string;
  kind: "person" | "group" | "guest";
  /** What the chip and the toast call them. */
  name: string;
  email?: string;
  persona?: string;
  count?: number;
}

/** One line of the results: a recipient, or a heading. */
type Option = { recipient: Recipient; detail: string; has: boolean };

/** The keys of the recipients that already have access: "p:<address>" and "g:<persona>". */
export type HasAccess = Set<string>;

function useDebounced(value: string, ms = 200): string {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

/** useOptions is what the results list for the text typed, grouped. */
function useOptions(q: string, personas: SharePersona[], has: HasAccess, picked: HasAccess) {
  const settled = useDebounced(q.trim());
  const { data } = useDirectoryUsers(settled, settled.length > 0, { limit: 8 });
  return useMemo(() => {
    const text = q.trim().toLowerCase();
    if (!text) return { groups: [] as Option[], people: [] as Option[], guest: undefined as Option | undefined };
    const groups = personas
      .filter((p) => !picked.has(`g:${p.name}`))
      .filter((p) => (p.display_name || p.name).toLowerCase().includes(text) || p.name.toLowerCase().includes(text))
      .map((p) => ({
        recipient: { key: `g:${p.name}`, kind: "group" as const, name: p.display_name || p.name, persona: p.name, count: p.member_count },
        detail: p.member_count === undefined ? "" : `${p.member_count} ${p.member_count === 1 ? "person" : "people"}`,
        has: has.has(`g:${p.name}`),
      }));
    const users = (data?.users ?? []).filter((u) => !picked.has(`p:${u.email.toLowerCase()}`));
    const people = users.map((u) => ({
      recipient: { key: `p:${u.email.toLowerCase()}`, kind: "person" as const, name: fullName(u) || u.email, email: u.email },
      detail: u.email,
      has: has.has(`p:${u.email.toLowerCase()}`),
    }));
    const addr = parseEmailAddress(q);
    const listed = addr && users.some((u) => u.email.toLowerCase() === addr.toLowerCase());
    const guest =
      addr && !listed && settled === q.trim() && !picked.has(`p:${addr.toLowerCase()}`)
        ? { recipient: { key: `p:${addr.toLowerCase()}`, kind: "guest" as const, name: addr, email: addr }, detail: "Invite as a guest", has: has.has(`p:${addr.toLowerCase()}`) }
        : undefined;
    return { groups, people, guest };
  }, [q, settled, personas, data, has, picked]);
}

/**
 * RecipientSearch is the box people and groups are added from (#2291): a
 * combobox whose results list the matching groups and people, and an address
 * no one in the directory has as a guest. Someone who already has access is
 * listed and cannot be picked.
 */
export function RecipientSearch({
  personas,
  has,
  picked,
  onPick,
  onOpenChange,
  inputRef,
  placeholder = "Add people or groups",
}: {
  personas: SharePersona[];
  has: HasAccess;
  picked: HasAccess;
  onPick: (r: Recipient) => void;
  onOpenChange?: (open: boolean) => void;
  inputRef?: RefObject<HTMLInputElement | null>;
  placeholder?: string;
}) {
  const id = useId();
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const [dismissed, setDismissed] = useState(false);
  const { groups, people, guest } = useOptions(q, personas, has, picked);
  const all = [...(guest ? [guest] : []), ...groups, ...people];
  const open = q.trim() !== "" && !dismissed;

  useEffect(() => {
    onOpenChange?.(open);
  }, [open, onOpenChange]);
  useEffect(() => setActive(0), [q]);

  function pick(o: Option | undefined) {
    if (!o || o.has) return;
    onPick(o.recipient);
    setQ("");
  }

  function onKey(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setDismissed(false);
      setActive((i) => Math.min(i + 1, all.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => Math.max(i - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      pick(all[active]);
    } else if (e.key === "Escape" && open) {
      setDismissed(true);
    }
  }

  const optionId = (i: number) => `${id}-opt-${i}`;
  return (
    <div className="relative">
      <Search className="pointer-events-none absolute top-3 left-3 size-[18px] text-muted-foreground" />
      <input
        ref={inputRef}
        role="combobox"
        aria-expanded={open}
        aria-controls={`${id}-list`}
        aria-autocomplete="list"
        aria-activedescendant={open && all[active] ? optionId(active) : undefined}
        aria-label={placeholder}
        placeholder={placeholder}
        value={q}
        onChange={(e) => {
          setQ(e.target.value);
          setDismissed(false);
        }}
        onKeyDown={onKey}
        className="h-11 w-full rounded-[10px] border border-input bg-background/60 pr-3 pl-10 text-sm outline-none focus:border-2 focus:border-primary"
      />
      {open && (
        <div className="absolute inset-x-0 top-full z-20 mt-2 rounded-xl border bg-popover p-1.5 shadow-lg">
          <div id={`${id}-list`} role="listbox" aria-label="People and groups" className="max-h-72 overflow-auto">
            {guest && <OptionRow o={guest} q={q} id={optionId(0)} active={active === 0} onPick={pick} />}
            {groups.length > 0 && <Heading>Groups</Heading>}
            {groups.map((o, i) => {
              const at = i + (guest ? 1 : 0);
              return <OptionRow key={o.recipient.key} o={o} q={q} id={optionId(at)} active={active === at} onPick={pick} />;
            })}
            {people.length > 0 && <Heading>People</Heading>}
            {people.map((o, i) => {
              const at = i + groups.length + (guest ? 1 : 0);
              return <OptionRow key={o.recipient.key} o={o} q={q} id={optionId(at)} active={active === at} onPick={pick} />;
            })}
          </div>
          <p className="mt-1 border-t px-2.5 pt-2 pb-1 text-xs text-muted-foreground">Not here? Type their full email address to invite them.</p>
        </div>
      )}
    </div>
  );
}

function Heading({ children }: { children: string }) {
  return (
    <div role="presentation" className="px-2.5 pt-2.5 pb-1 text-xs font-semibold text-muted-foreground">
      {children}
    </div>
  );
}

function OptionRow({ o, q, id, active, onPick }: { o: Option; q: string; id: string; active: boolean; onPick: (o: Option) => void }) {
  const r = o.recipient;
  const [before, match, after] = highlight(r.name, q.trim());
  return (
    <div
      id={id}
      role="option"
      aria-selected={active}
      aria-disabled={o.has}
      onMouseDown={(e) => {
        e.preventDefault();
        onPick(o);
      }}
      className={cn("flex cursor-default items-center gap-3 rounded-lg px-2.5 py-2", active && "bg-accent", o.has && "opacity-60")}
    >
      <OptionMark r={r} />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm">
          {before}
          {match && <span className="font-semibold text-primary">{match}</span>}
          {after}
          {r.kind === "guest" && <GuestPill />}
        </div>
        {o.detail && <div className="truncate text-xs text-muted-foreground">{o.detail}</div>}
      </div>
      {o.has && <span className="text-xs text-muted-foreground">Has access</span>}
    </div>
  );
}

/** OptionMark is a recipient's avatar. */
export function OptionMark({ r, size = 36 }: { r: Recipient; size?: 22 | 36 }) {
  if (r.kind === "group") {
    return (
      <Avatar tint={tintFor(r.name)} size={size}>
        <Users />
      </Avatar>
    );
  }
  return (
    <Avatar tint={r.kind === "guest" ? GUEST_TINT : tintFor(r.email ?? r.name)} size={size}>
      {initials(r.kind === "person" ? r.name : "", r.email ?? "")}
    </Avatar>
  );
}
