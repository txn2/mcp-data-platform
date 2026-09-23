import { useState } from "react";
import {
  useAddScriptGrant,
  useRemoveScriptGrant,
  useScriptGrants,
} from "@/api/portal/hooks/scripts";
import type { ScriptGrant, ScriptGrantKind } from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { formatWhen } from "./runFormat";

// ScriptGrantsCard is who other than the owner may run this script (#1846):
// a persona, a role, or an API key by name. A grantee runs the script over
// HTTP and reads the runs it started; it never reads the source or changes the
// script, and the run executes as the script with its author's roles. Every
// grant and withdrawal is audited.

const KIND_LABELS: Record<ScriptGrantKind, string> = {
  api_key: "API key",
  persona: "Persona",
  role: "Role",
};

export function ScriptGrantsCard({ scriptId }: { scriptId: string }) {
  const { data, isLoading, error } = useScriptGrants(scriptId, true);
  // A deployment that keeps no grants answers null: there is nothing to show.
  if (data === null) return null;
  const grants = data ?? [];
  return (
    <SectionCard
      title="Access"
      collapsible
      defaultOpen={false}
      summary={grants.length === 0 ? "Owner only" : `Granted to ${grants.length}`}
    >
      <div className="space-y-4">
        <p className="text-sm text-muted-foreground">
          A grantee may run this script and read the runs it started. It cannot read the
          source or change the script, and each run executes as this script with its
          author's roles.
        </p>
        {isLoading && <p className="text-sm text-muted-foreground">Loading...</p>}
        {error && (
          <Alert variant="destructive">
            <AlertDescription>The grants could not be read.</AlertDescription>
          </Alert>
        )}
        {grants.length > 0 && <GrantList scriptId={scriptId} grants={grants} />}
        <GrantForm scriptId={scriptId} />
      </div>
    </SectionCard>
  );
}

function GrantList({ scriptId, grants }: { scriptId: string; grants: ScriptGrant[] }) {
  const remove = useRemoveScriptGrant(scriptId);
  return (
    <ul className="divide-y rounded-md border text-sm">
      {grants.map((g) => (
        <li key={`${g.principal_kind}:${g.principal}`} className="flex items-center gap-3 px-3 py-2">
          <span className="w-20 shrink-0 text-muted-foreground">{KIND_LABELS[g.principal_kind]}</span>
          <span className="min-w-0 flex-1 truncate font-medium">{g.principal}</span>
          <span className="hidden text-xs text-muted-foreground sm:inline">
            {g.granted_by ? `by ${g.granted_by}, ` : ""}
            {formatWhen(g.created_at)}
          </span>
          <Button
            size="sm"
            variant="ghost"
            disabled={remove.isPending}
            onClick={() => remove.mutate({ principal_kind: g.principal_kind, principal: g.principal })}
          >
            Withdraw
          </Button>
        </li>
      ))}
    </ul>
  );
}

function GrantForm({ scriptId }: { scriptId: string }) {
  const add = useAddScriptGrant(scriptId);
  const [kind, setKind] = useState<ScriptGrantKind>("api_key");
  const [principal, setPrincipal] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const submit = () => {
    setFailure(null);
    add.mutate(
      { principal_kind: kind, principal: principal.trim() },
      {
        onSuccess: () => setPrincipal(""),
        onError: (e: unknown) => setFailure(e instanceof Error ? e.message : "The grant could not be added"),
      },
    );
  };
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-end gap-2">
        <div className="space-y-1">
          <Label htmlFor="grant-kind">Grant to</Label>
          <select
            id="grant-kind"
            className="h-9 rounded-md border bg-background px-2 text-sm"
            value={kind}
            onChange={(e) => setKind(e.target.value as ScriptGrantKind)}
          >
            {(Object.keys(KIND_LABELS) as ScriptGrantKind[]).map((k) => (
              <option key={k} value={k}>
                {KIND_LABELS[k]}
              </option>
            ))}
          </select>
        </div>
        <div className="min-w-48 flex-1 space-y-1">
          <Label htmlFor="grant-principal">Name</Label>
          <Input
            id="grant-principal"
            value={principal}
            placeholder={kind === "api_key" ? "reporting-app" : kind === "role" ? "dp_reports" : "analyst"}
            onChange={(e) => setPrincipal(e.target.value)}
          />
        </div>
        <Button size="sm" disabled={!principal.trim() || add.isPending} onClick={submit}>
          {add.isPending ? "Granting..." : "Grant"}
        </Button>
      </div>
      {failure && (
        <Alert variant="destructive">
          <AlertDescription>{failure}</AlertDescription>
        </Alert>
      )}
    </div>
  );
}
