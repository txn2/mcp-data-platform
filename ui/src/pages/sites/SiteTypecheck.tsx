import { RefreshCw } from "lucide-react";
import {
  isPending,
  useReleaseTypecheck,
  useRetypecheck,
  type TypecheckCheck,
  type TypecheckStatus,
  type TypecheckSummary,
} from "@/api/portal/hooks/siteTypecheck";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { when } from "./siteFormat";

type Variant = "muted" | "info" | "success" | "warning" | "danger";

/** typecheckBadge is the label and tint a check's status reads as. */
export function typecheckBadge(status: TypecheckStatus, errors: number): { label: string; variant: Variant } {
  switch (status) {
    case "queued":
      return { label: "Waiting", variant: "muted" };
    case "running":
      return { label: "Checking", variant: "info" };
    case "clean":
      return { label: "No errors", variant: "success" };
    case "errors":
      return { label: `${errors} error${errors === 1 ? "" : "s"}`, variant: "danger" };
    case "unsupported":
      return { label: "Cannot check", variant: "danger" };
    default:
      return { label: "Not finished", variant: "warning" };
  }
}

/** TypecheckBadge is a release's check in the Releases list. */
export function TypecheckBadge({ summary }: { summary?: TypecheckSummary }) {
  if (!summary) return <span className="text-xs text-muted-foreground">Not checked</span>;
  const b = typecheckBadge(summary.status, summary.errors);
  return <Badge variant={b.variant}>{b.label}</Badge>;
}

function seconds(ms: number): string {
  return ms < 1000 ? `${ms} ms` : `${Math.round(ms / 1000)} s`;
}

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

/** borderFor tints the card by how the check ended. */
function borderFor(status: TypecheckStatus): string {
  if (status === "clean") return "border-emerald-300";
  if (status === "errors" || status === "unsupported") return "border-red-300";
  return isPending(status) ? "" : "border-amber-300";
}

// SiteTypecheckCard is a release's type check (#2793). A site that asks for
// none shows no card, and the release page looks as it did.
export function SiteTypecheckCard({
  site,
  release,
  canCheck,
  onOpenFile,
}: {
  site: string;
  release: number;
  canCheck: boolean;
  onOpenFile?: (path: string) => void;
}) {
  const { data } = useReleaseTypecheck(site, release);
  const again = useRetypecheck(site, release);
  const check = data?.check;
  if (!check) return null;
  const b = typecheckBadge(check.status, check.result?.diagnostics.length ?? 0);
  const showAgain = canCheck && !isPending(check.status) && check.status !== "unsupported";
  return (
    <SectionCard
      className={borderFor(check.status)}
      title={
        <span className="flex items-center gap-2">
          Type check <Badge variant={b.variant}>{b.label}</Badge>
        </span>
      }
      action={
        showAgain ? (
          <Button size="sm" variant="outline" disabled={again.isPending} onClick={() => again.mutate()}>
            <RefreshCw /> Check again
          </Button>
        ) : undefined
      }
    >
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">
          <TypecheckSentence check={check} />
        </p>
        {check.status === "errors" && <Diagnostics check={check} onOpenFile={onOpenFile} />}
        <PackageNotes check={check} />
        {again.error && (
          <Alert variant="destructive">
            <AlertDescription>{again.error.message}</AlertDescription>
          </Alert>
        )}
      </div>
    </SectionCard>
  );
}

/** PackageNotes names the packages whose types a check could not read. */
function PackageNotes({ check }: { check: TypecheckCheck }) {
  const missing = check.result?.missing ?? [];
  const unverifiable = check.result?.unverifiable ?? [];
  return (
    <>
      {missing.length > 0 && (
        <Alert>
          <AlertDescription>
            {plural(missing.length, "package")} imported by the code {missing.length === 1 ? "is" : "are"} not in the
            lockfile, so their types were not read: <span className="font-mono text-xs">{missing.join(", ")}</span>.
          </AlertDescription>
        </Alert>
      )}
      {unverifiable.length > 0 && (
        <Alert>
          <AlertDescription>
            The lockfile gives no integrity hash for <span className="font-mono text-xs">{unverifiable.join(", ")}</span>,
            so their types were not read.
          </AlertDescription>
        </Alert>
      )}
    </>
  );
}

function TypecheckSentence({ check }: { check: TypecheckCheck }) {
  if (check.status === "queued") return <>Checks run one at a time. {aheadOf(check.ahead ?? 0)}</>;
  if (check.status === "running") {
    return <>Reading the types of the packages in the lockfile, then checking. Started {when(check.started_at ?? check.created_at)}.</>;
  }
  if (check.status === "clean" || check.status === "errors") return <FinishedSentence check={check} />;
  return <>{check.result?.message ?? "The check did not finish."}</>;
}

function aheadOf(ahead: number): string {
  if (ahead === 0) return "This one is next.";
  return `${plural(ahead, "release")} ${ahead === 1 ? "is" : "are"} ahead of this one.`;
}

function FinishedSentence({ check }: { check: TypecheckCheck }) {
  const res = check.result;
  const lockfile = res && res.packages > 0 && res.lockfile ? res.lockfile : "";
  return (
    <>
      {check.status === "errors" && "The release published and is being served. TypeScript found these errors in it. "}
      Checked {when(check.finished_at ?? check.created_at)} against <span className="font-mono text-xs">{check.tsconfig}</span>
      {res ? ` in ${seconds(res.duration_ms)}` : ""}
      {lockfile && (
        <>
          , using the types of {plural(res!.packages, "package")} at the versions in <span className="font-mono text-xs">{lockfile}</span>
        </>
      )}
      .
    </>
  );
}

function Diagnostics({ check, onOpenFile }: { check: TypecheckCheck; onOpenFile?: (path: string) => void }) {
  const diags = check.result?.diagnostics ?? [];
  return (
    <div className="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>File</TableHead>
            <TableHead>Line</TableHead>
            <TableHead>Column</TableHead>
            <TableHead>Code</TableHead>
            <TableHead>Message</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {diags.map((d, i) => (
            <TableRow key={`${d.file}:${d.line}:${d.column}:${i}`}>
              <TableCell className="break-all font-mono text-xs">
                {onOpenFile ? (
                  <button type="button" className="text-left text-primary hover:underline" onClick={() => onOpenFile(d.file)}>
                    {d.file}
                  </button>
                ) : (
                  d.file
                )}
              </TableCell>
              <TableCell>{d.line}</TableCell>
              <TableCell>{d.column}</TableCell>
              <TableCell className="font-mono text-xs">{d.code}</TableCell>
              <TableCell>{d.message}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
