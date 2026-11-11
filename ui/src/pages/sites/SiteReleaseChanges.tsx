import { useState } from "react";
import { useSiteCompare } from "@/api/portal/hooks/sites";
import { useSiteFileDiff } from "@/api/portal/hooks/siteFiles";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { UnifiedDiff } from "./SiteDiff";

// ChangesFrom is what a release changed from the one it was based on (#2590):
// each file added, changed or deleted, and, for the people who manage the site,
// one file's change as a diff. An earlier release can hold what a later one took
// out, so the diff is theirs alone.

export function ChangesFrom({ site, from, to, manage }: { site: string; from: number; to: number; manage: boolean }) {
  const { data, isLoading, error } = useSiteCompare(site, from, to);
  const [viewing, setViewing] = useState<string | null>(null);
  const rows = data
    ? [
        ...data.changed.map((p) => ({ p, label: "Changed", variant: "info" as const })),
        ...data.added.map((p) => ({ p, label: "Added", variant: "success" as const })),
        ...data.removed.map((p) => ({ p, label: "Deleted", variant: "danger" as const })),
      ]
    : [];
  return (
    <SectionCard title={`Changes from release ${from}`} action={data && <span className="text-xs text-muted-foreground">{countLine(rows.length)}</span>}>
      {error ? (
        <Alert variant="destructive">
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      ) : isLoading ? (
        <p className="text-sm text-muted-foreground">Comparing…</p>
      ) : rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">No file changed; only the redirect map or the note did.</p>
      ) : (
        <div className="overflow-x-auto" data-testid="release-changes">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Path</TableHead>
                <TableHead>Change</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow key={r.label + r.p}>
                  <TableCell className="break-all font-mono text-sm">{r.p}</TableCell>
                  <TableCell>
                    <Badge variant={r.variant}>{r.label}</Badge>
                  </TableCell>
                  <TableCell className="text-right">
                    {manage && r.label === "Changed" && (
                      <Button size="sm" variant="outline" onClick={() => setViewing(r.p)}>
                        View change
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {viewing && <FileChangeDialog site={site} path={viewing} from={from} to={to} onClose={() => setViewing(null)} />}
    </SectionCard>
  );
}

function countLine(n: number): string {
  return `${n} ${n === 1 ? "file" : "files"} changed`;
}

function FileChangeDialog({ site, path, from, to, onClose }: { site: string; path: string; from: number; to: number; onClose: () => void }) {
  const { data, isLoading, error } = useSiteFileDiff(site, path, from, to);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="break-all">
            {path}, release {from} to {to}
          </DialogTitle>
        </DialogHeader>
        {error ? (
          <Alert variant="destructive">
            <AlertDescription>{error.message}</AlertDescription>
          </Alert>
        ) : isLoading || !data ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : (
          <UnifiedDiff text={data.diff} />
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
