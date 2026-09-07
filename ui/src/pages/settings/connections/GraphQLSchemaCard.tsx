import { useState } from "react";
import { RefreshCw, Upload, AlertCircle, CheckCircle2 } from "lucide-react";
import {
  useGraphQLSchema,
  useRefreshGraphQLSchema,
  type GraphQLSchemaInfo,
} from "@/api/admin/hooks";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

// formatWhen renders a timestamp in the viewer's own locale, falling back to
// the raw value when the platform sent something unparseable.
function formatWhen(value?: string): string {
  if (!value) return "never";
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? value : at.toLocaleString();
}

// SchemaState is what the platform holds for the connection, or the named
// cause when it holds nothing.
function SchemaState({ info }: { info: GraphQLSchemaInfo }) {
  if (info.error) {
    return (
      <Alert variant="destructive">
        <AlertCircle />
        <AlertDescription>
          The platform holds no schema for this connection: {info.error}
        </AlertDescription>
      </Alert>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs">
      <CheckCircle2 className="size-4 text-emerald-600" />
      <span>
        <span className="font-medium">{info.operation_count}</span> operations
      </span>
      <Badge variant="outline">
        {info.source === "upload" ? "uploaded" : "introspected"}
      </Badge>
      <span className="text-muted-foreground">read {formatWhen(info.fetched_at)}</span>
      {info.schema_hash && (
        <span
          className="font-mono text-[10px] text-muted-foreground"
          title={info.schema_hash}
        >
          {info.schema_hash.slice(0, 12)}
        </span>
      )}
    </div>
  );
}

// SchemaUpload is the paste box for an endpoint that disables introspection.
function SchemaUpload({
  pending,
  onApply,
}: {
  pending: boolean;
  onApply: (schema: string, done: () => void) => void;
}) {
  const [text, setText] = useState("");
  return (
    <div className="space-y-2">
      <Label className="text-xs">Schema</Label>
      <p className="text-xs text-muted-foreground">
        Paste SDL, or a saved introspection result in either the full GraphQL
        response shape or the <code>__schema</code> object alone. This is the
        path for an endpoint that disables introspection.
      </p>
      <Textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={10}
        spellCheck={false}
        placeholder={"type Query {\n  search(input: SearchInput!): SearchResults\n}"}
        className="font-mono text-xs"
      />
      <Button
        type="button"
        size="sm"
        disabled={pending || text.trim() === ""}
        onClick={() => onApply(text, () => setText(""))}
      >
        {pending ? "Applying..." : "Apply schema"}
      </Button>
    </div>
  );
}

// SchemaActions is the pair of ways an administrator makes the platform's
// schema current: re-read the endpoint, or open the paste box.
function SchemaActions({
  pending,
  uploadOpen,
  onRefresh,
  onToggleUpload,
}: {
  pending: boolean;
  uploadOpen: boolean;
  onRefresh: () => void;
  onToggleUpload: () => void;
}) {
  return (
    <div className="flex flex-wrap gap-2">
      <Button type="button" size="sm" variant="outline" disabled={pending} onClick={onRefresh}>
        <RefreshCw />
        {pending ? "Reading..." : "Re-read from endpoint"}
      </Button>
      <Button type="button" size="sm" variant="ghost" onClick={onToggleUpload}>
        <Upload />
        {uploadOpen ? "Cancel upload" : "Upload a schema"}
      </Button>
    </div>
  );
}

// GraphQLSchemaCard shows what the platform holds for a graphql connection and
// lets an administrator make it current. Both ways a schema arrives are here
// because they are one question — is the platform's schema the endpoint's? —
// reached from one place: re-read it, or paste it in for an endpoint that
// disables introspection.
export function GraphQLSchemaCard({
  connectionName,
  isReadOnly,
}: {
  connectionName: string;
  isReadOnly: boolean;
}) {
  const { data, isLoading, error } = useGraphQLSchema(connectionName, true);
  const refresh = useRefreshGraphQLSchema(connectionName);
  const [uploadOpen, setUploadOpen] = useState(false);

  const failure = refresh.error instanceof Error ? refresh.error.message : null;

  return (
    <SectionCard title="Schema">
      <div className="space-y-4">
        {isLoading && (
          <p className="text-xs text-muted-foreground">Reading schema state...</p>
        )}

        {error && (
          <Alert variant="destructive">
            <AlertCircle />
            <AlertDescription>
              Could not read this connection&apos;s schema state.
            </AlertDescription>
          </Alert>
        )}

        {data && (
          <>
            <SchemaState info={data} />
            <p className="text-xs text-muted-foreground">
              The schema decides what graphql_discover lists and what
              graphql_query will send. Re-read it after the endpoint changes; a
              connection in strict validation refuses a document its stored
              schema does not admit.
            </p>
          </>
        )}

        {failure && (
          <Alert variant="destructive">
            <AlertCircle />
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}

        {!isReadOnly && (
          <SchemaActions
            pending={refresh.isPending}
            uploadOpen={uploadOpen}
            onRefresh={() => refresh.mutate(undefined)}
            onToggleUpload={() => setUploadOpen((open) => !open)}
          />
        )}

        {uploadOpen && !isReadOnly && (
          <SchemaUpload
            pending={refresh.isPending}
            onApply={(schema, done) =>
              refresh.mutate(schema, {
                onSuccess: () => {
                  done();
                  setUploadOpen(false);
                },
              })
            }
          />
        )}
      </div>
    </SectionCard>
  );
}
