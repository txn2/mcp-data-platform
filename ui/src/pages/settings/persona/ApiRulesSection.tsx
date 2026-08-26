import { useId, useState } from "react";
import { Plus, X } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import type { APIRouteRule } from "@/api/admin/types";
import { Section } from "./primitives";
import { renderPattern } from "./renderPattern";
import { BUCKET_TINT, type Bucket } from "./tints";

// The API-endpoint scope's rule editor. It lists the persona's rules as they
// were written and lets one be written by hand, which is what a pattern with no
// indexed operation needs: a path prefix on a connection whose spec has not
// been loaded yet has no row to select in the list beside it (#1479).
//
// A rule is shown exactly as stored. Nothing here rewrites a hand-written glob
// into the selection it resembles, so a rule an operator typed round-trips to
// them as the rule they typed.

/** methodsOf splits a free-text method list into the globs it names. */
function methodsOf(raw: string): string[] | undefined {
  const parts = raw
    .split(/[\s,]+/)
    .map((m) => m.trim().toUpperCase())
    .filter(Boolean);
  return parts.length > 0 ? parts : undefined;
}

/** pathsOf does the same for paths, which may contain no whitespace. */
function pathsOf(raw: string): string[] | undefined {
  const parts = raw
    .split(/[\s,]+/)
    .map((p) => p.trim())
    .filter(Boolean);
  return parts.length > 0 ? parts : undefined;
}

export function ApiRulesSection({
  rules,
  connectionNames,
  onAdd,
  onRemove,
  highlightIndex,
  onHighlight,
}: {
  rules: APIRouteRule[];
  connectionNames: string[];
  onAdd: (rule: APIRouteRule) => void;
  onRemove: (index: number) => void;
  highlightIndex: number | null;
  onHighlight: (index: number | null) => void;
}) {
  return (
    <Section
      title="API Endpoint Rules"
      meta={
        <span className="font-mono text-[10px] text-muted-foreground">
          {rules.length}
        </span>
      }
      description="Each rule narrows one API connection by method and path. A connection no rule names keeps full access, so adding a rule for one connection does not close the others."
    >
      {rules.length === 0 ? (
        <p className="text-[11px] italic text-muted-foreground">No rules.</p>
      ) : (
        <div className="space-y-1">
          {rules.map((rule, i) => (
            <RuleChip
              key={`${rule.connection}-${i}`}
              rule={rule}
              highlighted={highlightIndex === i}
              onHover={(h) => onHighlight(h ? i : null)}
              onRemove={() => onRemove(i)}
            />
          ))}
        </div>
      )}
      <AddRuleForm connectionNames={connectionNames} onAdd={onAdd} />
      {rules.some((r) => r.action !== "deny") && (
        <Alert variant="warning" className="mt-2 px-2 py-1.5">
          <AlertDescription className="text-[10px]">
            An allow rule closes the rest of the connection it names: once any
            rule names a connection, an operation must match an allow rule to be
            reachable.
          </AlertDescription>
        </Alert>
      )}
    </Section>
  );
}

function RuleChip({
  rule,
  highlighted,
  onHover,
  onRemove,
}: {
  rule: APIRouteRule;
  highlighted: boolean;
  onHover: (hovered: boolean) => void;
  onRemove: () => void;
}) {
  const bucket: Bucket = rule.action === "deny" ? "deny" : "allow";
  return (
    <Badge
      variant={bucket === "allow" ? "success" : "danger"}
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      className={cn(
        "group flex w-full items-start gap-2 rounded px-2 py-1",
        highlighted && "ring-1 ring-offset-1 ring-offset-background",
      )}
    >
      <span className="min-w-0 flex-1 space-y-0.5 text-left">
        <span className="block truncate font-mono text-[11px]">
          {renderPattern(rule.connection)}
        </span>
        <span className="block truncate font-mono text-[10px] opacity-80">
          {rule.methods?.length ? rule.methods.join(" ") : "any method"}
        </span>
        <span className="block truncate font-mono text-[10px] opacity-80">
          {rule.paths?.length
            ? rule.paths.map((p, i) => (
                <span key={p}>
                  {i > 0 && " "}
                  {renderPattern(p)}
                </span>
              ))
            : "any path"}
        </span>
      </span>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        onClick={onRemove}
        aria-label={`remove rule ${rule.connection}`}
        className="size-4 shrink-0 opacity-0 transition-opacity hover:bg-background/60 group-hover:opacity-100"
      >
        <X />
      </Button>
    </Badge>
  );
}

function AddRuleForm({
  connectionNames,
  onAdd,
}: {
  connectionNames: string[];
  onAdd: (rule: APIRouteRule) => void;
}) {
  // A generated id rather than a literal: a datalist is addressed by id, and a
  // literal one collides the moment this form is rendered twice.
  const listID = useId();
  const [open, setOpen] = useState(false);
  const [connection, setConnection] = useState("");
  const [methods, setMethods] = useState("");
  const [paths, setPaths] = useState("");
  const [action, setAction] = useState<Bucket>("deny");

  const close = () => {
    setOpen(false);
    setConnection("");
    setMethods("");
    setPaths("");
    setAction("deny");
  };

  const commit = () => {
    const conn = connection.trim();
    if (!conn) return;
    onAdd({
      connection: conn,
      methods: methodsOf(methods),
      paths: pathsOf(paths),
      action,
    });
    close();
  };

  if (!open) {
    return (
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => setOpen(true)}
        className="mt-2 w-full text-[11px]"
      >
        <Plus />
        Add rule
      </Button>
    );
  }

  return (
    <div className={cn("mt-2 space-y-2 rounded-md border p-2", BUCKET_TINT[action].border)}>
      <Input
        type="text"
        autoFocus
        value={connection}
        onChange={(e) => setConnection(e.target.value)}
        list={listID}
        placeholder="connection, e.g. crm-*"
        aria-label="Rule connection"
        className="h-7 px-2 font-mono text-[11px]"
      />
      <datalist id={listID}>
        {connectionNames.map((n) => (
          <option key={n} value={n} />
        ))}
      </datalist>
      <Input
        type="text"
        value={methods}
        onChange={(e) => setMethods(e.target.value)}
        placeholder="methods, blank for any"
        aria-label="Rule methods"
        className="h-7 px-2 font-mono text-[11px]"
      />
      <Input
        type="text"
        value={paths}
        onChange={(e) => setPaths(e.target.value)}
        placeholder="paths, blank for any"
        aria-label="Rule paths"
        className="h-7 px-2 font-mono text-[11px]"
      />
      <p className="text-[10px] text-muted-foreground">
        A path is matched against the path a call reaches and the path its
        operation declares, so <code className="font-mono">/v1/orders/{"{id}"}</code>{" "}
        governs that one operation. <code className="font-mono">*</code> does not
        cross a <code className="font-mono">/</code>.
      </p>
      <Select value={action} onValueChange={(v) => setAction(v as Bucket)}>
        <SelectTrigger className="h-7 text-[11px]" aria-label="Rule action">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="deny">Deny</SelectItem>
          <SelectItem value="allow">Allow</SelectItem>
        </SelectContent>
      </Select>
      <div className="flex justify-end gap-1.5">
        <Button type="button" variant="outline" size="xs" onClick={close}>
          Cancel
        </Button>
        <Button
          type="button"
          size="xs"
          onClick={commit}
          disabled={!connection.trim()}
          className={BUCKET_TINT[action].solid}
        >
          Add
        </Button>
      </div>
    </div>
  );
}
