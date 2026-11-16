import type { CallHandlerKind } from "@/api/portal/hooks/scriptHandlers";
import type { CallCondition } from "@/api/portal/hooks/callHandlers";

// What the Add handler form checks before it saves, the sentence saying which
// calls by whom a handler will meet (#2650), and the words and state the form
// shares with them.

export const KIND_WORDS: Record<CallHandlerKind, { label: string; hint: string; entry: string }> = {
  policy: { label: "Policy", hint: "Decide whether a call runs", entry: "decide(call)" },
  transform: { label: "Transform", hint: "Change what a call returns", entry: "transform(call, result)" },
  watch: { label: "Watch", hint: "Run a script after a call", entry: "main(), reading run.call" },
};

// Who is who a handler applies to; "" is not chosen yet, which a new handler
// starts at: everyone is a choice like the others (#2650).
export type Who = "" | "everyone" | "personas" | "users";

/** HandlerApproval is a script's version state for the approve offer:
 * whether the latest and the approved versions define the kind's entry, read
 * from their source; absent where it is not known. */
export type HandlerApproval = { latest: number; running: number; latestDefines?: boolean; runningDefines?: boolean };

// FormState is everything the form edits.
export interface FormState {
  conn: string;
  who: Who;
  personas: string[];
  users: string[];
  operations: string[];
  conditions: CallCondition[];
  itemsPath: string;
  timeoutMS: string;
  onFailure: string;
  protectedCols: string[];
  args: Record<string, string>;
  approve: boolean;
}

// definesSentence says whether the version shown defines the kind's entry,
// read from its source; nothing when that is not known.
export function definesSentence(kind: CallHandlerKind, approval: HandlerApproval): string {
  const shown = approval.latest > approval.running || approval.running === 0 ? approval.latestDefines : approval.runningDefines;
  if (shown === undefined) return "";
  return shown ? ` It defines ${KIND_WORDS[kind].entry}.` : ` It does not define ${KIND_WORDS[kind].entry}.`;
}

// versionRefusal is why the version a new handler would run cannot serve its
// kind: the approved one, when the latest is not being approved and the
// approved one defines no entry (#2650).
export function versionRefusal(kind: CallHandlerKind, approve: boolean, approval?: HandlerApproval): string | null {
  if (!approval) return null;
  const entry = KIND_WORDS[kind].entry;
  if (approval.latestDefines === false && (approve || approval.running === 0)) {
    return `Version ${approval.latest} does not define ${entry}.`;
  }
  if (!approve && approval.running > 0 && approval.running !== approval.latest && approval.runningDefines === false) {
    return `Version ${approval.latest} defines ${entry}, but version ${approval.running}, the one approved for handlers, does not. Approve version ${approval.latest} to add this ${KIND_WORDS[kind].label.toLowerCase()}.`;
  }
  return null;
}

// whoRefusal is why who a handler applies to cannot be saved: not chosen, or
// a list chosen and left empty (#2650).
export function whoRefusal(st: FormState): string | null {
  switch (st.who) {
    case "":
      return "Choose who it applies to.";
    case "personas":
      return st.personas.length === 0 ? "Add at least one persona, or choose Everyone." : null;
    case "users":
      return st.users.length === 0 ? "Add at least one person, or choose Everyone." : null;
  }
  return null;
}

const KIND_VERBS: Record<CallHandlerKind, string> = { policy: "decide", transform: "change what returns from", watch: "run after" };

// ScopeSummary says in one sentence which calls by whom the handler will
// meet, so a broad one is chosen knowingly (#2650).
export function ScopeSummary({ kind, st }: { kind: CallHandlerKind; st: FormState }) {
  if (st.who === "") return null;
  const conn = st.conn.trim() || "the connection";
  const ops = st.operations.length > 0 ? st.operations.join(", ") : "every call";
  if (st.who === "everyone") {
    return (
      <p className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-100" data-testid="scope-summary">
        It will {KIND_VERBS[kind]} {ops} on {conn}, by every caller.
      </p>
    );
  }
  const list = st.who === "personas" ? st.personas : st.users;
  if (list.length === 0) return null;
  const whom = st.who === "personas" ? `the personas ${list.join(", ")}` : list.join(", ");
  return (
    <p className="rounded-md border bg-muted/40 px-3 py-2 text-sm" data-testid="scope-summary">
      It will {KIND_VERBS[kind]} {ops} on {conn} for {whom}.
    </p>
  );
}

