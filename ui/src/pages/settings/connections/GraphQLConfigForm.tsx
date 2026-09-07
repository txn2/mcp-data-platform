import { useState } from "react";
import { HelpDialog } from "@/components/HelpDialog";
import { ApiGatewayAuthHelp, ApiGatewayTLSHelp } from "../ApiGatewayHelpContent";
import { ApiGatewayAuthFields } from "./ApiGatewayAuthFields";
import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  ConfigToggle,
  asStringMap,
  update,
  type ConfigFormProps,
} from "./fields";
import { SensitiveKeyValueEditor } from "./keyvalue";
import { TLSMaterialEditor } from "./TlsMaterialEditor";

const SCHEMA_VALIDATION = [
  { value: "strict", label: "Strict (default) — refuse a document the schema does not admit" },
  { value: "warn", label: "Warn — send it anyway and report the violations" },
];

// GraphQLConfigForm renders the editor for kind=graphql connections.
// The credential, TLS, static-header and timeout fields are the same
// ones the HTTP API gateway uses, because both kinds read them through
// internal/upstreamauth; the fields below the divider are this kind's
// own (see pkg/toolkits/graphql/config.go).
export function GraphQLConfigForm({
  config,
  onChange,
  connectionName,
  isCreate,
}: ConfigFormProps & { connectionName: string; isCreate: boolean }) {
  const [authHelpOpen, setAuthHelpOpen] = useState(false);
  const [tlsHelpOpen, setTlsHelpOpen] = useState(false);

  return (
    <>
      <ConfigField
        label="Endpoint URL"
        help="Full URL documents are POSTed to. Unlike an HTTP API's base URL this is the whole address: a GraphQL endpoint has exactly one."
        value={String(config.endpoint_url ?? "")}
        onChange={(v) => onChange(update(config, "endpoint_url", v))}
        placeholder="https://datahub.example.com/api/graphql"
        mono
        required
      />

      <ApiGatewayAuthFields
        config={config}
        onChange={onChange}
        connectionName={connectionName}
        isCreate={isCreate}
        onOpenHelp={() => setAuthHelpOpen(true)}
      />

      <TLSMaterialEditor
        config={config}
        onChange={onChange}
        onOpenHelp={() => setTlsHelpOpen(true)}
      />

      <ConfigGroup title="Static headers">
        <p className="text-xs text-muted-foreground">
          Headers added to every outbound request, in addition to whatever
          Auth mode contributes. This is where an upstream&apos;s tenant or
          folder routing goes — a vendor subscription key, or an ERP&apos;s
          folder header. The model never sets or overrides these. Values are
          encrypted at rest; existing values are masked.
        </p>
        <SensitiveKeyValueEditor
          entries={asStringMap(config.static_headers)}
          onChange={(next) =>
            onChange(
              update(
                config,
                "static_headers",
                Object.keys(next).length === 0 ? undefined : next,
              ),
            )
          }
          keyPlaceholder="x-tenant-id"
          valuePlaceholder="header value"
        />
      </ConfigGroup>

      <ConfigGroup title="Schema and documents">
        <ConfigSelect
          label="Schema validation"
          value={String(config.schema_validation ?? "strict")}
          onChange={(v) => onChange(update(config, "schema_validation", v))}
          options={SCHEMA_VALIDATION}
          help="What happens when a document does not validate against the schema the platform read from this endpoint. Strict refuses it and names the connection and when its schema was read, so schema drift diagnoses itself. Warn sends it and reports the violations alongside the answer — for a deployment whose endpoint has moved ahead of the stored schema."
        />

        <div className="grid grid-cols-2 gap-3">
          <ConfigField
            label="Max query depth"
            help="Deepest selection a document may have. A deeply nested document is how one small request makes an endpoint do unbounded work. Default 15, which is deeper than any document Discover renders."
            type="number"
            value={String(config.max_query_depth ?? "")}
            onChange={(v) =>
              onChange(update(config, "max_query_depth", v ? Number(v) : undefined))
            }
            placeholder="15"
          />
          <ConfigField
            label="Namespace depth"
            help="How many segments an operation id may have when the schema is walked into operations. A flat schema indexes its root fields whatever this is; a namespaced one (package, entity, verb) needs 3. Default 3."
            type="number"
            value={String(config.namespace_depth ?? "")}
            onChange={(v) =>
              onChange(update(config, "namespace_depth", v ? Number(v) : undefined))
            }
            placeholder="3"
          />
        </div>

        <ConfigToggle
          label="Read only"
          help="Refuse every mutation document on this connection, for every persona. Set it here once when an endpoint is mounted for reporting, rather than writing the same deny rule into every persona."
          checked={Boolean(config.read_only)}
          onChange={(v) => onChange(update(config, "read_only", v || undefined))}
        />
      </ConfigGroup>

      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Connect timeout"
          help="Initial dial timeout (e.g. 10s, 1m)."
          value={String(config.connect_timeout ?? "")}
          onChange={(v) => onChange(update(config, "connect_timeout", v))}
          placeholder="10s"
          mono
        />
        <ConfigField
          label="Call timeout"
          help="Per-call upstream timeout (e.g. 60s)."
          value={String(config.call_timeout ?? "")}
          onChange={(v) => onChange(update(config, "call_timeout", v))}
          placeholder="60s"
          mono
        />
      </div>

      <ConfigField
        label="Max response bytes"
        help="Upstream read cap: the most the platform reads of any one response. A transfer limit, not what reaches the model. Default 10485760 (10 MiB)."
        type="number"
        value={String(config.max_response_bytes ?? "")}
        onChange={(v) =>
          onChange(update(config, "max_response_bytes", v ? Number(v) : undefined))
        }
        placeholder="10485760"
      />

      <ConfigField
        label="Max inline bytes"
        help="Model-context budget: the most a rendered graphql_query result may hold. A result past it has its data withheld — a JSON document cut in half cannot be parsed — is flagged data_truncated, and carries the graphql_export call that writes the whole result to an asset. Default 32768 (32 KiB)."
        type="number"
        value={String(config.max_inline_bytes ?? "")}
        onChange={(v) =>
          onChange(update(config, "max_inline_bytes", v ? Number(v) : undefined))
        }
        placeholder="32768"
      />

      <HelpDialog
        open={authHelpOpen}
        onOpenChange={setAuthHelpOpen}
        title="Authentication modes"
      >
        <ApiGatewayAuthHelp />
      </HelpDialog>

      <HelpDialog
        open={tlsHelpOpen}
        onOpenChange={setTlsHelpOpen}
        title="TLS and mTLS"
      >
        <ApiGatewayTLSHelp />
      </HelpDialog>
    </>
  );
}
