import type { ReactNode } from "react";
import type { ScriptEndpoint } from "@/api/portal/hooks/scriptEndpoint";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

/** pageFacts are the lines a page endpoint adds to the endpoint card. */
export function pageFacts(e: { response_mode: "api" | "page"; page_address?: string }): [string, string][] {
  const rows: [string, string][] = [["Answers with", e.response_mode === "page" ? "A browser page" : "JSON or text"]];
  if (e.response_mode === "page" && e.page_address) rows.push(["Page address", e.page_address]);
  return rows;
}

function Field({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  return (
    <div className="space-y-1">
      <Label htmlFor={id}>{label}</Label>
      {children}
    </div>
  );
}

// PageFields say whether the endpoint answers with JSON or text or serves a
// browser page (#2517, #2559), which needs its script to belong to an application.
export function PageFields({
  endpoint,
  responseMode,
  pageCdns,
  onMode,
  onCdns,
}: {
  endpoint: ScriptEndpoint;
  responseMode: "api" | "page";
  pageCdns: string;
  onMode: (v: "api" | "page") => void;
  onCdns: (v: string) => void;
}) {
  if (endpoint.page_problem && responseMode !== "page") {
    return (
      <Alert data-testid="page-needs-application">
        <AlertDescription>A browser page needs its script to belong to an application, so it has an address of its own. {endpoint.page_problem}</AlertDescription>
      </Alert>
    );
  }
  return (
    <>
      <Field id="endpoint-response-mode" label="Answers with">
        <select id="endpoint-response-mode" className="w-full rounded-md border bg-background px-3 py-2 text-sm" value={responseMode} onChange={(e) => onMode(e.target.value as "api" | "page")}>
          <option value="page">A browser page</option>
          <option value="api">JSON or text</option>
        </select>
        <p className="mt-1 text-xs text-muted-foreground">A page runs its own scripts on the application's own address. It never sees the portal's session.</p>
      </Field>
      {responseMode === "page" && (
        <>
          {endpoint.page_address && (
            <div>
              <Label>Page address</Label>
              <p className="break-all rounded border p-2 font-mono text-sm">{endpoint.page_address}</p>
            </div>
          )}
          <Field id="endpoint-page-cdns" label="Other sites this page may load from">
            <Textarea id="endpoint-page-cdns" rows={3} value={pageCdns} placeholder="https://cdn.jsdelivr.net" onChange={(e) => onCdns(e.target.value)} />
            <p className="mt-1 text-xs text-muted-foreground">One https origin per line, at most 20. Everything else is blocked.</p>
          </Field>
        </>
      )}
    </>
  );
}

