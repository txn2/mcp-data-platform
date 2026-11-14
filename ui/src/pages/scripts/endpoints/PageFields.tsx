import type { ReactNode } from "react";
import { useEndpointIds } from "./endpointIds";
import type { ScriptEndpoint } from "@/api/portal/hooks/scriptEndpoint";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

/** PageLists are a page's origin lists as the form edits them, one origin a line. */
export interface PageLists {
  cdns: string;
  frame: string;
  connect: string;
  form: string;
}

/** PAGE_LISTS name each list on the form and on the card (#2554), and say what it opens. */
export const PAGE_LISTS: { key: keyof PageLists; field: keyof ScriptEndpoint; label: string; fact: string; hint: string }[] = [
  {
    key: "cdns",
    field: "page_cdns",
    label: "Other sites this page may load from",
    fact: "Loads from",
    hint: "Scripts, styles, images and fonts. One https origin per line, at most 20. A leading *. needs two labels after it.",
  },
  {
    key: "frame",
    field: "page_frame_origins",
    label: "Framed sites",
    fact: "Framed sites",
    hint: "Sites this page may show in a frame, such as hosted card fields. One https origin per line, at most 20.",
  },
  {
    key: "connect",
    field: "page_connect_origins",
    label: "Called sites",
    fact: "Called sites",
    hint: "Sites this page's scripts may call, besides its own address. One https origin per line, at most 20.",
  },
  {
    key: "form",
    field: "page_form_origins",
    label: "Form destinations",
    fact: "Form destinations",
    hint: "Sites this page's forms may post to or be redirected to after a post, besides its own address. One https origin per line, at most 20.",
  },
];

/** listOf is one of a page's lists from the binding, empty when it has none. */
export function listOf(e: Partial<ScriptEndpoint>, field: keyof ScriptEndpoint): string[] {
  const v = e[field];
  return Array.isArray(v) ? (v as string[]) : [];
}

/** pageFacts are the lines a page endpoint adds to the endpoint card. */
export function pageFacts(e: Partial<ScriptEndpoint> & { response_mode: "api" | "page" }): [string, string][] {
  const rows: [string, string][] = [["Answers with", e.response_mode === "page" ? "A browser page" : "JSON or text"]];
  if (e.response_mode !== "page") return rows;
  if (e.page_address) rows.push(["Page address", e.page_address]);
  for (const l of PAGE_LISTS) rows.push([l.fact, listOf(e, l.field).join(", ") || "None"]);
  return rows;
}

/** pageListsOf is a binding's lists as the form edits them. */
export function pageListsOf(e: Partial<ScriptEndpoint>): PageLists {
  const lines = (field: keyof ScriptEndpoint) => listOf(e, field).join("\n");
  return { cdns: lines("page_cdns"), frame: lines("page_frame_origins"), connect: lines("page_connect_origins"), form: lines("page_form_origins") };
}

/** pageListInput is the lists a save sends: each one on a page, none on an api endpoint. */
export function pageListInput(mode: "api" | "page", lists: PageLists) {
  const read = (v: string) => (mode === "page" ? v.split("\n").map((o) => o.trim()).filter(Boolean) : []);
  return {
    page_cdns: read(lists.cdns),
    page_frame_origins: read(lists.frame),
    page_connect_origins: read(lists.connect),
    page_form_origins: read(lists.form),
  };
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
// browser page (#2517, #2559), which needs its script to belong to an
// application, and on a page the sites its policy opens (#2554).
export function PageFields({
  endpoint,
  responseMode,
  lists,
  onMode,
  onList,
}: {
  endpoint: ScriptEndpoint;
  responseMode: "api" | "page";
  lists: PageLists;
  onMode: (v: "api" | "page") => void;
  onList: (key: keyof PageLists, v: string) => void;
}) {
  const eid = useEndpointIds();
  if (endpoint.page_problem && responseMode !== "page") {
    return (
      <Alert data-testid="page-needs-application">
        <AlertDescription>A browser page needs its script to belong to an application, so it has an address of its own. {endpoint.page_problem}</AlertDescription>
      </Alert>
    );
  }
  return (
    <>
      <Field id={eid("endpoint-response-mode")} label="Answers with">
        <select id={eid("endpoint-response-mode")} className="w-full rounded-md border bg-background px-3 py-2 text-sm" value={responseMode} onChange={(e) => onMode(e.target.value as "api" | "page")}>
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
          {PAGE_LISTS.map((l) => (
            <Field key={l.key} id={eid(`endpoint-page-${l.key}`)} label={l.label}>
              <Textarea
                id={eid(`endpoint-page-${l.key}`)}
                rows={2}
                value={lists[l.key]}
                placeholder={l.key === "cdns" ? "https://cdn.jsdelivr.net" : "https://js.stripe.com"}
                onChange={(e) => onList(l.key, e.target.value)}
              />
              <p className="mt-1 text-xs text-muted-foreground">{l.hint}</p>
            </Field>
          ))}
        </>
      )}
    </>
  );
}
