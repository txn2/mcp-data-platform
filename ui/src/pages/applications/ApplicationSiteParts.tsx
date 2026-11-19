import { useState } from "react";
import {
  useSetSitePartMode,
  useUpdateApplication,
  type ApplicationDetail,
  type ApplicationReleaseMode,
  type ApplicationSitePart,
} from "@/api/portal/hooks/applications";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { errorText } from "./applicationFormat";
import { AddEnvironmentDialog } from "./EnvironmentDialogs";
import { envSchemaOf, siteEnvOf, siteLabel } from "./environmentModel";

// ApplicationSiteParts is the application's site parts (#2679): each site
// environment's release mode, the release it serves, the contract range that
// release declares and whether it works with the application's contract; the
// owner and administrators change a part's mode and the contract version.
// Each part calls the application environment of its own name (#2709), shown
// as Calls; a part whose name matches no environment calls nothing and its
// /endpoints answer 404, and an administrator adds that environment from it.

export function ApplicationSiteParts({
  detail,
  canEdit,
  isAdmin = false,
}: {
  detail: ApplicationDetail;
  canEdit: boolean;
  isAdmin?: boolean;
}) {
  const app = detail.application;
  const parts = detail.site_parts ?? [];
  const setMode = useSetSitePartMode(app.id);
  const [failure, setFailure] = useState<string | null>(null);
  const [contractOpen, setContractOpen] = useState(false);
  const contract = app.contract_version ?? 0;
  if (parts.length === 0 && contract === 0) return null;

  const change = (id: string, mode: ApplicationReleaseMode) => {
    setFailure(null);
    setMode.mutate({ id, release_mode: mode }, { onError: (e) => setFailure(errorText(e, "The mode could not be changed")) });
  };

  return (
    <SectionCard
      title={<CardTitle contract={contract} />}
      action={
        canEdit ? (
          <Button size="sm" variant="outline" onClick={() => setContractOpen(true)}>
            Contract version…
          </Button>
        ) : undefined
      }
    >
      {failure && (
        <Alert variant="destructive" className="mb-3">
          <AlertDescription>{failure}</AlertDescription>
        </Alert>
      )}
      {parts.length > 0 && (
        <SitePartsTable
          detail={detail}
          contract={contract}
          isAdmin={isAdmin}
          onMode={change}
          canEdit={canEdit}
          busy={setMode.isPending}
        />
      )}
      <ModeNotes detail={detail} />
      {contractOpen && <ContractDialog app={app.id} description={app.description} current={contract} onClose={() => setContractOpen(false)} />}
    </SectionCard>
  );
}

function CardTitle({ contract }: { contract: number }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span>Site parts</span>
      <span className="text-sm font-normal text-muted-foreground" data-testid="contract-version">
        {contract > 0 ? `Contract ${contract}` : "No contract declared"}
      </span>
    </div>
  );
}

/** SitePartsTable is a row per site part: what it calls, its mode, release and contract. */
function SitePartsTable({
  detail,
  contract,
  isAdmin,
  onMode,
  canEdit,
  busy,
}: {
  detail: ApplicationDetail;
  contract: number;
  isAdmin: boolean;
  onMode: (id: string, mode: ApplicationReleaseMode) => void;
  canEdit: boolean;
  busy: boolean;
}) {
  // A no-match row adds the environment it names (#2709).
  const [adding, setAdding] = useState<string | null>(null);
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm" data-testid="site-parts">
        <thead className="text-left text-xs text-muted-foreground">
          <tr>
            <th className="py-2 pr-3 font-medium">Site environment</th>
            <th className="py-2 pr-3 font-medium">Calls</th>
            <th className="py-2 pr-3 font-medium">Mode</th>
            <th className="py-2 pr-3 font-medium">Serves</th>
            <th className="py-2 pr-3 font-medium">Contract it needs</th>
            <th className="py-2 pr-3 font-medium">With contract {contract}</th>
            <th className="py-2" />
          </tr>
        </thead>
        <tbody>
          {(detail.site_parts ?? []).map((p) => (
            <SitePartRow
              key={p.part_id}
              detail={detail}
              part={p}
              isAdmin={isAdmin}
              onAdd={setAdding}
              onMode={canEdit ? onMode : undefined}
              busy={busy}
            />
          ))}
        </tbody>
      </table>
      {adding !== null && <AddEnvironmentDialog detail={detail} name={adding} onClose={() => setAdding(null)} />}
    </div>
  );
}

function SitePartRow({
  detail,
  part: p,
  isAdmin,
  onAdd,
  onMode,
  busy,
}: {
  detail: ApplicationDetail;
  part: ApplicationSitePart;
  isAdmin: boolean;
  onAdd: (env: string) => void;
  onMode?: (id: string, mode: ApplicationReleaseMode) => void;
  busy: boolean;
}) {
  const linked = p.release_mode === "linked";
  return (
    <tr className="border-t" data-testid={`site-part-${p.part_id}`}>
      <td className="py-2 pr-3">{siteLabel(p.part_id)}</td>
      <td className="py-2 pr-3" data-testid={`site-part-calls-${p.part_id}`}>
        <Calls detail={detail} partId={p.part_id} isAdmin={isAdmin} onAdd={onAdd} />
      </td>
      <td className="py-2 pr-3">
        <Badge variant={linked ? "info" : "warning"}>{linked ? "Linked" : "Coupled"}</Badge>
      </td>
      <td className="py-2 pr-3">{p.serves > 0 ? `release ${p.serves}` : "no release"}</td>
      <td className="py-2 pr-3">
        {p.contract ? <code className="font-mono text-xs">{p.contract}</code> : <span className="text-muted-foreground">Any (declares none)</span>}
      </td>
      <td className="py-2 pr-3">
        {p.compatible ? (
          <span className="text-emerald-700 dark:text-emerald-300">Compatible</span>
        ) : (
          <span className="text-destructive">Needs contract {p.contract}</span>
        )}
      </td>
      <td className="py-2 text-right">
        {onMode && (
          <Button size="sm" variant="outline" disabled={busy} onClick={() => onMode(p.part_id, linked ? "coupled" : "linked")}>
            Make {linked ? "coupled" : "linked"}
          </Button>
        )}
      </td>
    </tr>
  );
}

/** ModeNotes says what each release mode does, in the application's environment names. */
function ModeNotes({ detail }: { detail: ApplicationDetail }) {
  const envs = detail.application.environments ?? [];
  const first = envs[0]?.name ?? "dev";
  const last = envs[envs.length - 1]?.name ?? "prod";
  return (
    <div className="mt-3 grid gap-3 text-xs text-muted-foreground sm:grid-cols-2">
      <p>
        <b className="text-foreground">Linked</b> calls the application environment of its name, on its own address with the application's cookies and
        viewer, and moves only by Promote and Roll back on the site, unless a release names it.
      </p>
      <p>
        <b className="text-foreground">Coupled</b> does everything linked does, and moves one step when a release reaches the environment it calls: a release
        into {first} moves the part calling {first}, a promote into {last} moves the part calling {last}.
      </p>
    </div>
  );
}

function ContractDialog({ app, description, current, onClose }: { app: string; description: string; current: number; onClose: () => void }) {
  const update = useUpdateApplication(app);
  const [value, setValue] = useState(String(current));
  const [failure, setFailure] = useState<string | null>(null);
  const n = Number(value);
  const valid = value.trim() !== "" && Number.isInteger(n) && n >= 0;
  const save = () => {
    setFailure(null);
    update.mutate(
      { description, contract_version: n },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The contract could not be saved")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Contract version</DialogTitle>
          <DialogDescription>A whole number you raise when a release breaks what front ends call. 0 declares no contract and every front end is compatible.</DialogDescription>
        </DialogHeader>
        <div className="space-y-1">
          <Label htmlFor="contract-version">Contract</Label>
          <Input id="contract-version" inputMode="numeric" className="w-32" value={value} onChange={(e) => setValue(e.target.value)} />
        </div>
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={save} disabled={!valid || update.isPending}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Calls is the application environment a site part calls, or why it calls none. */
function Calls({
  detail,
  partId,
  isAdmin,
  onAdd,
}: {
  detail: ApplicationDetail;
  partId: string;
  isAdmin: boolean;
  onAdd: (env: string) => void;
}) {
  const app = detail.application;
  const { env } = siteEnvOf(partId);
  const match = (app.environments ?? []).find((e) => e.name === env);
  if (!match) {
    return (
      <span className="text-destructive">
        <b>Nothing.</b> {app.name} has no environment named {env}, so its /endpoints answer 404.{" "}
        {isAdmin && (
          <button type="button" className="text-primary underline-offset-2 hover:underline" onClick={() => onAdd(env)}>
            Add environment {env}
          </button>
        )}
      </span>
    );
  }
  const schema = envSchemaOf(detail, env)?.schema;
  return (
    <span>
      <b>
        {app.name} / {env}
      </b>
      <span className="text-muted-foreground">
        {" "}
        · {match.release > 0 ? `release ${match.release}` : "no release"}
        {schema ? ` · ${schema}` : ""}
      </span>
    </span>
  );
}
