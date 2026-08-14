import { useState } from "react";
import { Minus, Plus, X } from "lucide-react";
import type { ScriptGrants } from "@/api/admin/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { grantDelta, toggle } from "./scriptGrants";

// The grant editor: what approving this version would let it do, read against
// what it can do today.
//
// The three axes are edited separately because they answer different
// questions — which host functions, which data, and where output may land —
// and because a grant is deny-by-default per axis on the server.

// GrantAxis renders one closed vocabulary as toggles, marking each entry
// against the approved grant: new authority, authority being taken away, or no
// change. The marker is the point of the control — a reviewer is deciding on
// the delta, not on the absolute list.
export function GrantAxis({
  label,
  help,
  options,
  granted,
  previous,
  hasBaseline,
  onChange,
}: {
  label: string;
  help: string;
  options: readonly string[];
  granted: string[];
  previous: string[];
  // hasBaseline says whether the script executes anything today. Without one
  // every grant is an addition, and listing them again under the controls that
  // set them says nothing.
  hasBaseline: boolean;
  onChange: (next: string[]) => void;
}) {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs">{label}</Label>
      <div className="flex flex-wrap gap-2">
        {options.map((option) => {
          const on = granted.includes(option);
          return (
            <Button
              key={option}
              type="button"
              size="sm"
              variant={on ? "default" : "outline"}
              aria-pressed={on}
              onClick={() => onChange(toggle(granted, option))}
              className="font-mono text-xs"
            >
              {on ? <Plus /> : <Minus />}
              {option}
            </Button>
          );
        })}
      </div>
      <DeltaLine delta={grantDelta(previous, granted)} hasBaseline={hasBaseline} />
      <p className="text-xs text-muted-foreground">{help}</p>
    </div>
  );
}

// DeltaLine states the change in one sentence, so a widening is read rather
// than inferred from which chips happen to be lit.
function DeltaLine({
  delta,
  hasBaseline,
}: {
  delta: ReturnType<typeof grantDelta>;
  hasBaseline: boolean;
}) {
  if (!hasBaseline) {
    return (
      <p className="text-xs text-muted-foreground">
        This script executes nothing today, so everything granted here is new.
      </p>
    );
  }
  if (delta.added.length === 0 && delta.removed.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">
        No change from what this script holds today.
      </p>
    );
  }
  return (
    <p className="flex flex-wrap items-center gap-1.5 text-xs">
      {delta.added.map((v) => (
        <Badge key={`add-${v}`} variant="warning" className="font-mono">
          + {v}
        </Badge>
      ))}
      {delta.removed.map((v) => (
        <Badge key={`rm-${v}`} variant="muted" className="font-mono line-through">
          {v}
        </Badge>
      ))}
    </p>
  );
}

// ConnectionsEditor edits the connection allowlist. Connections are open text
// rather than a closed set: they are named per deployment, and a script that
// computes its connection name has none a static read could offer.
export function ConnectionsEditor({
  granted,
  previous,
  referenced,
  hasBaseline,
  onChange,
}: {
  granted: string[];
  previous: string[];
  referenced: string[];
  hasBaseline: boolean;
  onChange: (next: string[]) => void;
}) {
  const [draft, setDraft] = useState("");
  const add = () => {
    const value = draft.trim();
    if (value && !granted.includes(value)) {
      onChange([...granted, value].sort());
    }
    setDraft("");
  };
  const suggestions = referenced.filter((c) => !granted.includes(c));

  return (
    <div className="space-y-1.5">
      <Label className="text-xs">Connections</Label>
      <div className="flex gap-2">
        <Input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
          // Commit on blur too, so a name typed but not added is not silently
          // dropped by the Approve the reviewer reaches for next.
          onBlur={add}
          placeholder="warehouse"
          aria-label="Add connection"
          className="font-mono"
        />
        <Button type="button" variant="outline" onClick={add} disabled={!draft.trim()}>
          <Plus />
          Add
        </Button>
      </div>
      {granted.length > 0 && (
        <ul className="flex flex-wrap gap-1.5 pt-1">
          {granted.map((name) => (
            <li key={name}>
              <Badge variant="outline" className="gap-1 bg-muted/40 py-1 pr-1 pl-2.5 font-mono">
                {name}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => onChange(granted.filter((c) => c !== name))}
                  aria-label={`Remove ${name}`}
                  className="size-4 rounded-full"
                >
                  <X />
                </Button>
              </Badge>
            </li>
          ))}
        </ul>
      )}
      {suggestions.length > 0 && (
        <p className="text-xs text-muted-foreground">
          This version queries{" "}
          {suggestions.map((name) => (
            <button
              key={name}
              type="button"
              onClick={() => onChange([...granted, name].sort())}
              className="mr-1 font-mono underline underline-offset-2"
            >
              {name}
            </button>
          ))}
          and would be refused on its first query without it.
        </p>
      )}
      <DeltaLine delta={grantDelta(previous, granted)} hasBaseline={hasBaseline} />
    </div>
  );
}

// AuthorityNote states the roles approving binds and that they are not a
// choice. The approve request carries no roles: the server copies them from
// the version's author, so an approver can narrow what a script reaches and
// can never hand it authority the author did not hold. Rendering them as an
// editable control would imply a decision that does not exist.
export function AuthorityNote({ grants, author }: { grants: ScriptGrants; author: string }) {
  const roles = grants.roles ?? [];
  return (
    <div className="rounded-md border border-blue-500/30 bg-blue-500/5 p-3 text-xs">
      <div className="font-medium">Authority this version would run with</div>
      <div className="mt-1 flex flex-wrap items-center gap-1.5">
        {roles.length === 0 ? (
          <span className="text-muted-foreground">
            none, so an approved run would resolve to the deny-all persona and could call
            nothing
          </span>
        ) : (
          roles.map((role) => (
            <Badge key={role} variant="info" className="font-mono">
              {role}
            </Badge>
          ))
        )}
      </div>
      <p className="mt-1.5 text-muted-foreground">
        Copied from {author || "the author"}, who wrote this version. Approving cannot
        change it: a script can never do what the person who wrote it could not.
      </p>
    </div>
  );
}
