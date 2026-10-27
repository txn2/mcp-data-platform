import { Lock } from "lucide-react";
import { useScriptEndpoint, type FormSource } from "@/api/portal/hooks/scriptEndpoint";
import {
  submissionsExportURL,
  useScriptSubmissions,
  type FormSubmissionsPage,
} from "@/api/portal/hooks/scriptSubmissions";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { formatBytes, formatWhen } from "./runFormat";

// ScriptSubmissions is a form's stored submissions (#2180): the newest rows of
// the webhook source its accepted submissions are stored in (#2178), newest
// first, with its sensitive fields decrypted for the owner, which the platform
// audits (#2179), each file a submission carried linked to its resource
// (#2333), and the same rows as CSV or an Excel workbook. It shows only for a
// form whose source exists.

export function ScriptSubmissions({ scriptId }: { scriptId: string }) {
  const { data: endpoint } = useScriptEndpoint(scriptId, true);
  if (!endpoint?.form) return null;
  return <Submissions scriptId={scriptId} form={endpoint.form} />;
}

function Submissions({ scriptId, form }: { scriptId: string; form: FormSource }) {
  const { data, isLoading, isFetching, error, refetch } = useScriptSubmissions(scriptId, true);
  return (
    <SectionCard title="Submissions" collapsible defaultOpen summary={data ? `${data.submissions.length} shown` : undefined}>
      <div className="space-y-3">
        <Toolbar scriptId={scriptId} refreshing={isFetching} onRefresh={() => void refetch()} />
        <p className="text-xs text-muted-foreground">
          Stored in {form.table} on {form.connection}.
        </p>
        {isLoading && <p className="text-sm text-muted-foreground">Loading...</p>}
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error.message}</AlertDescription>
          </Alert>
        )}
        {data && <SubmissionsTable page={data} />}
      </div>
    </SectionCard>
  );
}

function Toolbar({ scriptId, refreshing, onRefresh }: { scriptId: string; refreshing: boolean; onRefresh: () => void }) {
  return (
    <div className="flex flex-wrap gap-2">
      <Button size="sm" variant="outline" disabled={refreshing} onClick={onRefresh}>
        {refreshing ? "Refreshing..." : "Refresh"}
      </Button>
      <Button size="sm" variant="outline" asChild>
        <a href={submissionsExportURL(scriptId, "csv")} download>
          Export CSV
        </a>
      </Button>
      <Button size="sm" variant="outline" asChild>
        <a href={submissionsExportURL(scriptId, "xlsx")} download>
          Export Excel
        </a>
      </Button>
    </div>
  );
}

function SubmissionsTable({ page }: { page: FormSubmissionsPage }) {
  if (page.submissions.length === 0) {
    return <p className="text-sm text-muted-foreground">No submissions yet.</p>;
  }
  const showSubmitter = page.submissions.some((s) => s.submitter);
  return (
    <div className="overflow-x-auto rounded-md border">
      <table className="w-full text-sm">
        <thead className="bg-muted/50 text-left text-muted-foreground">
          <tr>
            <th className="whitespace-nowrap px-3 py-2 font-medium">Received</th>
            {showSubmitter && <th className="whitespace-nowrap px-3 py-2 font-medium">Submitter</th>}
            {page.fields.map((f) => (
              <th key={f} className="whitespace-nowrap px-3 py-2 font-medium">
                <span className="inline-flex items-center gap-1">
                  {f}
                  {page.sensitive.includes(f) && (
                    <Lock className="size-3.5" aria-label="Encrypted when stored">
                      <title>Encrypted when stored</title>
                    </Lock>
                  )}
                </span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y">
          {page.submissions.map((s) => (
            <tr key={s.id} className="align-top">
              <td className="whitespace-nowrap px-3 py-2">{formatWhen(s.received_at)}</td>
              {showSubmitter && <td className="whitespace-nowrap px-3 py-2">{s.submitter?.email ?? "—"}</td>}
              {page.fields.map((f) => (
                <td key={f} className="max-w-xs break-words px-3 py-2">
                  <Cell value={s.values[f]} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** SubmittedFile is a file a submission carried, as it was saved (#2333). */
export interface SubmittedFile {
  name: string;
  content_type: string;
  size: number;
  uri?: string;
  resource_id?: string;
}

/** submittedFiles is v's files when v is a file field's value, else null. */
export function submittedFiles(v: unknown): SubmittedFile[] | null {
  if (!Array.isArray(v) || v.length === 0) return null;
  const files = v.filter((item): item is SubmittedFile => typeof item === "object" && item !== null && "resource_id" in item);
  return files.length === v.length ? files : null;
}

/** Cell is a submitted value; a file field's files open their resources. */
function Cell({ value }: { value: unknown }) {
  const files = submittedFiles(value);
  if (!files) return <>{cellText(value)}</>;
  return (
    <ul className="space-y-1">
      {files.map((f) => (
        <li key={f.resource_id}>
          <a href={`/portal/resources/${encodeURIComponent(f.resource_id ?? "")}`} className="underline" title={f.uri}>
            {f.name}
          </a>{" "}
          <span className="text-xs text-muted-foreground">{formatBytes(f.size)}</span>
        </li>
      ))}
    </ul>
  );
}

/** cellText is a submitted value as the table shows it. */
export function cellText(v: unknown): string {
  if (v === undefined || v === null || v === "") return "—";
  if (Array.isArray(v)) return v.length === 0 ? "—" : v.map(String).join(", ");
  if (typeof v === "boolean") return v ? "Yes" : "No";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}
