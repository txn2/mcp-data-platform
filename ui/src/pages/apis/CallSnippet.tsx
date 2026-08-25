import { useMemo, useState } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { APIOperationDetail, APIParameterDetail } from "@/api/apis/types";
import { asSchema, typeLabel } from "./SchemaView";

// The call an operation produces, as a person would run it.
//
// The gateway route is the one non-MCP callers already have
// (internal/httpserver/gatewayhttp), so what this writes is the request an
// InvokeHTTP processor, a cron job or a shell needs, with the operation's own
// method, path and required parameters filled in. Path parameters stay as the
// braces the spec wrote them in: they are the one part the caller must replace,
// and a fabricated id would read as a working value.
//
// Nothing here calls anything. The snippet is text.

/** SAMPLE_DEPTH bounds how far a generated body follows nested objects. */
const SAMPLE_DEPTH = 3;

/** SCALAR_PLACEHOLDER is the stand-in for a type whose shape a literal can
 * carry. Everything else gets its type name in angle brackets, which tells the
 * reader what to put there instead of handing them a value that looks real. */
const SCALAR_PLACEHOLDER: Record<string, unknown> = {
  integer: 0,
  number: 0,
  boolean: false,
};

/** schemaType is the primary type name, or "" when the schema declares none. */
function schemaType(schema: Record<string, unknown> | undefined): string {
  const type = schema?.type;
  if (Array.isArray(type)) return String(type[0] ?? "");
  return typeof type === "string" ? type : "";
}

/** placeholderFor is the stand-in for one scalar. An enumeration answers with
 * its first member, because that is a value the endpoint actually accepts. */
function placeholderFor(schema: Record<string, unknown> | undefined): unknown {
  const values = schema?.enum;
  if (Array.isArray(values) && values.length > 0) return values[0];
  const type = schemaType(schema);
  if (type in SCALAR_PLACEHOLDER) return SCALAR_PLACEHOLDER[type];
  return `<${typeLabel(schema)}>`;
}

/** sampleValue is one property: a nested shape is followed, a scalar is not. */
function sampleValue(prop: Record<string, unknown>, depth: number): unknown {
  if (prop.properties || prop.type === "array") return sampleBody(prop, depth + 1);
  return placeholderFor(prop);
}

/**
 * sampleObject writes the smallest object the schema declares: every required
 * property, and nothing else. A schema with no required list yields all of its
 * properties, because a body with no required fields still has to show what it
 * accepts or the snippet would carry an empty object.
 */
function sampleObject(
  required: string[] | undefined,
  properties: Record<string, Record<string, unknown>>,
  depth: number,
): Record<string, unknown> {
  const names = required && required.length > 0 ? required : Object.keys(properties);
  const out: Record<string, unknown> = {};
  for (const name of names) {
    const prop = properties[name];
    if (prop) out[name] = sampleValue(prop, depth);
  }
  return out;
}

/** sampleBody builds the body an operation's request-body schema describes. */
export function sampleBody(value: unknown, depth = 1): unknown {
  const schema = asSchema(value) as Record<string, unknown> | undefined;
  if (!schema) return undefined;
  if (schema.type === "array") {
    const item = sampleBody(schema.items, depth);
    return item === undefined ? [] : [item];
  }
  const properties = schema.properties as Record<string, Record<string, unknown>> | undefined;
  if (!properties || depth > SAMPLE_DEPTH) return placeholderFor(schema);
  return sampleObject(schema.required as string[] | undefined, properties, depth);
}

/** paramsIn collects the required parameters of one location. */
function paramsIn(
  parameters: APIParameterDetail[] | undefined,
  location: string,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const p of parameters ?? []) {
    if (p.in !== location || !p.required) continue;
    out[p.name] = placeholderFor(asSchema(p.schema) as Record<string, unknown> | undefined);
  }
  return out;
}

/** invokeBody is the JSON the gateway route takes: the operation reduced to a
 * method, a path, and whatever the caller has to supply with it. */
export function invokeBody(detail: APIOperationDetail): Record<string, unknown> {
  const body: Record<string, unknown> = { method: detail.method, path: detail.path };
  const query = paramsIn(detail.parameters, "query");
  if (Object.keys(query).length > 0) body.query_params = query;
  const headers = paramsIn(detail.parameters, "header");
  if (Object.keys(headers).length > 0) body.headers = headers;
  const sample = detail.request_body ? sampleBody(detail.request_body.schema) : undefined;
  if (sample !== undefined) body.body = sample;
  return body;
}

/**
 * shellQuote wraps a value in single quotes for a POSIX shell, closing and
 * reopening around any single quote inside it. A spec author's apostrophe -- in
 * an enum value, a path, a parameter name -- would otherwise end the quoted
 * string and leave the reader with a command that does not run.
 */
function shellQuote(value: string): string {
  return `'${value.split("'").join(`'\\''`)}'`;
}

/** curlSnippet writes the request as a shell command. */
export function curlSnippet(origin: string, connection: string, detail: APIOperationDetail): string {
  const payload = JSON.stringify(invokeBody(detail), null, 2)
    .split("\n")
    .map((line, i) => (i === 0 ? line : `  ${line}`))
    .join("\n");
  const url = `${origin}/api/v1/gateway/${encodeURIComponent(connection)}/invoke`;
  return [
    `curl -sS -X POST ${shellQuote(url)} \\`,
    `  -H 'Content-Type: application/json' \\`,
    // Double-quoted so the shell substitutes the reader's own key. The platform
    // never puts a credential in this snippet.
    `  -H "X-API-Key: $MCP_API_KEY" \\`,
    `  -d ${shellQuote(payload)}`,
  ].join("\n");
}

interface CallSnippetProps {
  connection: string;
  baseURL?: string;
  detail: APIOperationDetail;
  /** The page origin the gateway is reached at. Injected so a test can state it. */
  origin?: string;
}

/**
 * CallSnippet shows the upstream call an operation produces and the gateway
 * request that produces it, copyable.
 */
export function CallSnippet({ connection, baseURL, detail, origin }: CallSnippetProps) {
  const [copied, setCopied] = useState(false);
  const pageOrigin = origin ?? (typeof window === "undefined" ? "" : window.location.origin);
  const snippet = useMemo(
    () => curlSnippet(pageOrigin, connection, detail),
    [pageOrigin, connection, detail],
  );
  const hasPathParams = detail.path.includes("{");

  function copy() {
    void navigator.clipboard.writeText(snippet).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }

  return (
    <section className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">Call it over HTTP</h3>
        <Button size="sm" variant="outline" onClick={copy} aria-label="Copy the curl command">
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      <p className="text-[11px] text-muted-foreground">
        The gateway performs{" "}
        <code className="font-mono">
          {detail.method} {baseURL ?? ""}
          {detail.path}
        </code>{" "}
        with the connection's own credential, which never leaves the platform.
        {hasPathParams && " Replace each braced path parameter before sending."}
      </p>
      <pre className="overflow-x-auto rounded-md bg-muted p-3 font-mono text-[11px] leading-relaxed">
        {snippet}
      </pre>
    </section>
  );
}
