import { useState } from "react";
import { Minus, Plus, X } from "lucide-react";
import type { ScriptDestination, ScriptGrants } from "@/api/admin/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  DESTINATION_KIND_S3,
  destinationKey,
  destinationKeys,
  grantDelta,
  isPortal,
  portalDestination,
  PORTAL_DESTINATION,
  toggle,
} from "./scriptGrants";

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

// DestinationsEditor edits where an approved run may write.
//
// The portal is a toggle, because the platform provides it and it has no
// address to decide. Everywhere else the reviewer supplies the address — the
// connection, the bucket, and the prefix everything written there sits under —
// because that address is what they are actually agreeing to. A script names
// only the destination; a grant that recorded only the name would leave what it
// means in configuration nobody re-approves.
export function DestinationsEditor({
  granted,
  previous,
  hasBaseline,
  onChange,
}: {
  granted: ScriptDestination[];
  previous: ScriptDestination[];
  hasBaseline: boolean;
  onChange: (next: ScriptDestination[]) => void;
}) {
  const [draft, setDraft] = useState("");
  const portalGranted = granted.some(isPortal);
  const external = granted.filter((d) => !isPortal(d));

  const addExternal = () => {
    const name = draft.trim();
    if (name && name !== PORTAL_DESTINATION && !granted.some((d) => d.name === name)) {
      onChange([...granted, { name, kind: DESTINATION_KIND_S3 }]);
    }
    setDraft("");
  };
  const update = (name: string, patch: Partial<ScriptDestination>) =>
    onChange(granted.map((d) => (d.name === name ? { ...d, ...patch } : d)));

  return (
    <div className="space-y-1.5">
      <Label className="text-xs">Output destinations</Label>
      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          size="sm"
          variant={portalGranted ? "default" : "outline"}
          aria-pressed={portalGranted}
          onClick={() =>
            onChange(
              portalGranted
                ? granted.filter((d) => !isPortal(d))
                : [portalDestination(), ...granted],
            )
          }
          className="font-mono text-xs"
        >
          {portalGranted ? <Plus /> : <Minus />}
          {PORTAL_DESTINATION}
        </Button>
      </div>
      {external.map((destination) => (
        <ExternalDestination
          key={destination.name}
          destination={destination}
          onChange={(patch) => update(destination.name, patch)}
          onRemove={() => onChange(granted.filter((d) => d.name !== destination.name))}
        />
      ))}
      <div className="flex gap-2">
        <Input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              addExternal();
            }
          }}
          onBlur={addExternal}
          placeholder="acme-drop"
          aria-label="Add bucket destination"
          className="font-mono"
        />
        <Button type="button" variant="outline" onClick={addExternal} disabled={!draft.trim()}>
          <Plus />
          Add bucket
        </Button>
      </div>
      <DeltaLine
        delta={grantDelta(destinationKeys(previous), destinationKeys(granted))}
        hasBaseline={hasBaseline}
      />
      <p className="text-xs text-muted-foreground">
        Where output may be written. An empty list means the script may compute but not
        persist. A bucket destination sends data out of the platform, over a connection
        the platform holds the credentials for; the script supplies no address of its own
        and can write nothing outside the prefix granted here.
      </p>
    </div>
  );
}

// ExternalDestination edits the address one named destination resolves to.
function ExternalDestination({
  destination,
  onChange,
  onRemove,
}: {
  destination: ScriptDestination;
  onChange: (patch: Partial<ScriptDestination>) => void;
  onRemove: () => void;
}) {
  const incomplete = !destination.connection || !destination.bucket;
  return (
    <div className="space-y-1.5 rounded-md border p-2.5">
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-xs">{destination.name}</span>
        <div className="flex items-center gap-1.5">
          {incomplete && <Badge variant="warning">Needs an address</Badge>}
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            onClick={onRemove}
            aria-label={`Remove ${destination.name}`}
            className="size-5 rounded-full"
          >
            <X />
          </Button>
        </div>
      </div>
      <div className="grid gap-2 sm:grid-cols-3">
        <Input
          value={destination.connection ?? ""}
          onChange={(e) => onChange({ connection: e.target.value })}
          placeholder="connection"
          aria-label={`${destination.name} connection`}
          className="font-mono text-xs"
        />
        <Input
          value={destination.bucket ?? ""}
          onChange={(e) => onChange({ bucket: e.target.value })}
          placeholder="bucket"
          aria-label={`${destination.name} bucket`}
          className="font-mono text-xs"
        />
        <Input
          value={destination.prefix ?? ""}
          onChange={(e) => onChange({ prefix: e.target.value })}
          placeholder="prefix (optional)"
          aria-label={`${destination.name} prefix`}
          className="font-mono text-xs"
        />
      </div>
      <p className="font-mono text-xs text-muted-foreground">{destinationKey(destination)}</p>
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
