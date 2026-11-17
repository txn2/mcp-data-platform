import { useState } from "react";
import {
  useSetSitePartMode,
  useUpdateApplication,
  type ApplicationDetail,
  type ApplicationReleaseMode,
} from "@/api/portal/hooks/applications";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { errorText } from "./applicationFormat";

// ApplicationSiteParts is the application's site parts (#2679): each site
// environment's release mode, the release it serves, the contract range that
// release declares and whether it works with the application's contract; the
// owner and administrators change a part's mode and the contract version.

export function ApplicationSiteParts({ detail, canEdit }: { detail: ApplicationDetail; canEdit: boolean }) {
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
      title={
        <div className="flex flex-col gap-0.5">
          <span>Site parts</span>
          <span className="text-sm font-normal text-muted-foreground" data-testid="contract-version">
            {contract > 0 ? `Contract ${contract}` : "No contract declared"}
          </span>
        </div>
      }
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
        <div className="overflow-x-auto">
          <table className="w-full text-sm" data-testid="site-parts">
            <thead className="text-left text-xs text-muted-foreground">
              <tr>
                <th className="py-2 pr-3 font-medium">Site environment</th>
                <th className="py-2 pr-3 font-medium">Mode</th>
                <th className="py-2 pr-3 font-medium">Serves</th>
                <th className="py-2 pr-3 font-medium">Contract it needs</th>
                <th className="py-2 pr-3 font-medium">With contract {contract}</th>
                <th className="py-2" />
              </tr>
            </thead>
            <tbody>
              {parts.map((p) => (
                <tr key={p.part_id} className="border-t" data-testid={`site-part-${p.part_id}`}>
                  <td className="py-2 pr-3">{p.part_id.replace("/", " / ")}</td>
                  <td className="py-2 pr-3">
                    <Badge variant={p.release_mode === "linked" ? "info" : "warning"}>{p.release_mode === "linked" ? "Linked" : "Coupled"}</Badge>
                  </td>
                  <td className="py-2 pr-3">{p.serves > 0 ? `release ${p.serves}` : "no release"}</td>
                  <td className="py-2 pr-3">{p.contract ? <code className="font-mono text-xs">{p.contract}</code> : <span className="text-muted-foreground">Any (declares none)</span>}</td>
                  <td className="py-2 pr-3">
                    {p.compatible ? (
                      <span className="text-emerald-700 dark:text-emerald-300">Compatible</span>
                    ) : (
                      <span className="text-destructive">Needs contract {p.contract}</span>
                    )}
                  </td>
                  <td className="py-2 text-right">
                    {canEdit && (
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={setMode.isPending}
                        onClick={() => change(p.part_id, p.release_mode === "linked" ? "coupled" : "linked")}
                      >
                        Make {p.release_mode === "linked" ? "coupled" : "linked"}
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="mt-3 grid gap-3 text-xs text-muted-foreground sm:grid-cols-2">
        <p>
          <b className="text-foreground">Linked</b> calls the application on its own address with its cookies and viewer, and moves only by Promote and Roll back on
          the site, unless a release names it.
        </p>
        <p>
          <b className="text-foreground">Coupled</b> does everything linked does, and every application release moves it one step and every rollback returns it.
        </p>
      </div>
      {contractOpen && <ContractDialog app={app.id} description={app.description} current={contract} onClose={() => setContractOpen(false)} />}
    </SectionCard>
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
