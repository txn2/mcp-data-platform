import { Alert, AlertDescription } from "@/components/ui/alert";
import { EmptyState } from "@/components/patterns/EmptyState";
import type {
  APIConnection,
  APIOperationDetail,
  APIParameterDetail,
  APIResponseDetail,
  APISavedExample,
} from "@/api/apis/types";
import { CallSnippet } from "./CallSnippet";
import { MethodBadge } from "@/components/patterns/MethodBadge";
import { SchemaView } from "./SchemaView";

// The right half of the browser: one operation, in the order a person reads it.
// What it is, what it takes, what it gives back, and -- where there is a
// connection to call it on -- the request that calls it.
//
// Everything rendered here comes from the platform's own operation resolution,
// so this pane and api_get_endpoint_schema describe an operation identically.

/** SECTION_HEADING is the one styling of a section label in this pane. */
const SECTION_HEADING =
  "text-[11px] font-semibold uppercase tracking-wide text-muted-foreground";

/** KNOWN_LOCATIONS are the parameter locations this pane gives a section to.
 * Anything else (a cookie parameter, a vendor extension) lands in "Other", so
 * it is shown rather than dropped. */
const KNOWN_LOCATIONS = ["path", "query", "header"];

/** ParameterRow is one parameter: where it goes, whether it is required, and
 * the shape it takes. */
function ParameterRow({ parameter }: { parameter: APIParameterDetail }) {
  return (
    <li className="px-3 py-2">
      <div className="flex flex-wrap items-baseline gap-x-2">
        <code className="font-mono text-[12px]">{parameter.name}</code>
        <RequirementTag required={parameter.required} />
      </div>
      {parameter.description && (
        <p className="mt-0.5 text-[11px] text-muted-foreground">{parameter.description}</p>
      )}
      <SchemaView value={parameter.schema} className="mt-1" />
    </li>
  );
}

/** RequirementTag states whether a caller must supply the value. Both readings
 * are labeled rather than only the required one, so an unlabeled row never
 * means "we did not say". */
function RequirementTag({ required }: { required?: boolean }) {
  if (required) {
    return (
      <span className="text-[10px] font-semibold uppercase tracking-wide text-destructive">
        required
      </span>
    );
  }
  return (
    <span className="text-[10px] uppercase tracking-wide text-muted-foreground">optional</span>
  );
}

/** ParameterList renders the parameters of one location, or nothing when the
 * operation declares none there. */
function ParameterList({
  title,
  parameters,
}: {
  title: string;
  parameters: APIParameterDetail[];
}) {
  if (parameters.length === 0) return null;
  return (
    <div className="space-y-1">
      <h4 className={SECTION_HEADING}>{title}</h4>
      <ul className="divide-y rounded-md border">
        {parameters.map((p) => (
          <ParameterRow key={`${p.in}-${p.name}`} parameter={p} />
        ))}
      </ul>
    </div>
  );
}

/** ParameterSections splits the parameters into the places they are sent. */
function ParameterSections({ parameters }: { parameters: APIParameterDetail[] }) {
  const at = (location: string) => parameters.filter((p) => p.in === location);
  return (
    <>
      <ParameterList title="Path parameters" parameters={at("path")} />
      <ParameterList title="Query parameters" parameters={at("query")} />
      <ParameterList title="Header parameters" parameters={at("header")} />
      <ParameterList
        title="Other parameters"
        parameters={parameters.filter((p) => !KNOWN_LOCATIONS.includes(p.in))}
      />
    </>
  );
}

/** RequestBodySection renders the body an operation takes. */
function RequestBodySection({ detail }: { detail: APIOperationDetail }) {
  const body = detail.request_body;
  if (!body) return null;
  return (
    <div className="space-y-1">
      <h4 className={SECTION_HEADING}>
        Request body
        {body.required && <span className="ml-2 text-destructive">required</span>}
      </h4>
      <div className="rounded-md border px-3 py-2">
        {body.description && (
          <p className="text-[11px] text-muted-foreground">{body.description}</p>
        )}
        {body.content_types && (
          <p className="font-mono text-[10px] text-muted-foreground/80">
            {body.content_types.join(", ")}
          </p>
        )}
        <SchemaView value={body.schema} className="mt-1" />
      </div>
    </div>
  );
}

/** ResponseRow is one declared status. */
function ResponseRow({ response }: { response: APIResponseDetail }) {
  const headers = Object.keys(response.headers ?? {});
  return (
    <li className="px-3 py-2">
      <div className="flex flex-wrap items-baseline gap-x-2">
        <code className="font-mono text-[12px] font-semibold">{response.status}</code>
        {response.description && (
          <span className="text-[11px] text-muted-foreground">{response.description}</span>
        )}
        {response.content_types && (
          <span className="font-mono text-[10px] text-muted-foreground/80">
            {response.content_types.join(", ")}
          </span>
        )}
      </div>
      {headers.length > 0 && (
        <p className="mt-0.5 text-[11px] text-muted-foreground">
          Headers: {headers.join(", ")}
        </p>
      )}
      <SchemaView value={response.schema} className="mt-1" />
    </li>
  );
}

/** ResponseSection renders one entry per declared status. */
function ResponseSection({ responses }: { responses: APIResponseDetail[] | undefined }) {
  if (!responses || responses.length === 0) return null;
  return (
    <div className="space-y-1">
      <h4 className={SECTION_HEADING}>Responses</h4>
      <ul className="divide-y rounded-md border">
        {responses.map((r) => (
          <ResponseRow key={r.status} response={r} />
        ))}
      </ul>
    </div>
  );
}

/** SavedExamplesSection lists the requests promoted from calls that worked on
 * this connection, which is a different claim from what the spec declares. */
function SavedExamplesSection({ examples }: { examples: APISavedExample[] | undefined }) {
  if (!examples || examples.length === 0) return null;
  return (
    <div className="space-y-1">
      <h4 className={SECTION_HEADING}>Calls that worked here</h4>
      <ul className="divide-y rounded-md border">
        {examples.map((ex, i) => (
          <li key={ex.id ?? `${ex.name}-${i}`} className="px-3 py-2">
            <p className="text-[12px] font-medium">{ex.name}</p>
            {ex.description && (
              <p className="text-[11px] text-muted-foreground">{ex.description}</p>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

/** OperationHeader names the operation and the call it produces. */
function OperationHeader({
  detail,
  authMode,
}: {
  detail: APIOperationDetail;
  authMode?: string;
}) {
  return (
    <header className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <MethodBadge method={detail.method} className="px-2 py-1 text-[11px]" />
        <code className="break-all font-mono text-sm">{detail.path}</code>
      </div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
        <span>
          Operation <code className="font-mono">{detail.operation_id}</code>
        </span>
        {detail.spec && (
          <span>
            Spec <code className="font-mono">{detail.spec}</code>
          </span>
        )}
        {authMode && (
          <span>
            Auth <code className="font-mono">{authMode}</code>
          </span>
        )}
      </div>
      {detail.summary && <p className="text-sm font-medium">{detail.summary}</p>}
      {detail.description && (
        <p className="whitespace-pre-wrap text-sm text-muted-foreground">{detail.description}</p>
      )}
    </header>
  );
}

interface OperationDetailProps {
  detail: APIOperationDetail | undefined;
  loading?: boolean;
  error?: string;
  /** The connection an invoke snippet is written against. Absent on the
   * operator's catalog view, where a spec need not be wired to anything yet. */
  connection?: APIConnection;
  /** Injected page origin for the snippet, so a test can state it. */
  origin?: string;
}

/** OperationDetail renders one operation in full, or says why it cannot. */
export function OperationDetail({
  detail,
  loading,
  error,
  connection,
  origin,
}: OperationDetailProps) {
  if (loading) {
    return <p className="p-6 text-center text-sm text-muted-foreground">Loading operation...</p>;
  }
  if (error) {
    return (
      <Alert variant="destructive" className="m-4">
        <AlertDescription>{error}</AlertDescription>
      </Alert>
    );
  }
  if (!detail) {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <EmptyState>Select an operation to see what it takes and what it returns.</EmptyState>
      </div>
    );
  }

  return (
    <div className="h-full overflow-auto p-4">
      <div className="space-y-5">
        <OperationHeader detail={detail} authMode={connection?.auth_mode} />
        {detail.note && (
          <Alert>
            <AlertDescription>{detail.note}</AlertDescription>
          </Alert>
        )}
        <ParameterSections parameters={detail.parameters ?? []} />
        <RequestBodySection detail={detail} />
        <ResponseSection responses={detail.responses} />
        <SavedExamplesSection examples={detail.saved_examples} />
        {connection && (
          <CallSnippet
            connection={connection.name}
            baseURL={connection.base_url}
            detail={detail}
            origin={origin}
          />
        )}
      </div>
    </div>
  );
}
