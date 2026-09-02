import { useState } from "react";
import { HelpDialog } from "@/components/HelpDialog";
import { ApiGatewayAuthHelp, ApiGatewayTLSHelp } from "../ApiGatewayHelpContent";
import { ApiGatewayAuthFields } from "./ApiGatewayAuthFields";
import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  asStringMap,
  update,
  type ConfigFormProps,
} from "./fields";
import { SensitiveKeyValueEditor } from "./keyvalue";
import { TLSMaterialEditor } from "./TlsMaterialEditor";
import { APICatalogPicker, LegacyOpenAPISpecBanner } from "./ApiCatalogPicker";

const TRUST_LEVELS = [
  { value: "untrusted", label: "Untrusted (default)" },
  { value: "trusted", label: "Trusted" },
];

// ApiGatewayConfigForm renders the editor for kind=api connections —
// the HTTP API gateway. Field shape matches the apigateway toolkit
// config (see pkg/toolkits/apigateway/config.go): base_url, the auth
// block (ApiGatewayAuthFields), TLS material, static headers, the
// catalog reference, timeouts, max_response_bytes and max_inline_bytes.
export function ApiGatewayConfigForm({
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
        label="Base URL"
        help="HTTPS URL of the upstream API (no trailing slash)."
        value={String(config.base_url ?? "")}
        onChange={(v) => onChange(update(config, "base_url", v))}
        placeholder="https://api.vendor.example.com"
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
          Auth mode contributes. Required by APIs that demand both an
          OAuth bearer AND a separate key, e.g. Google&apos;s
          <code className="mx-1">x-goog-user-project</code> for quota
          billing or a vendor subscription header. Values are encrypted
          at rest; existing values are masked.
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
          keyPlaceholder="X-Goog-User-Project"
          valuePlaceholder="header value"
        />
      </ConfigGroup>

      <APICatalogPicker config={config} onChange={onChange} />
      <LegacyOpenAPISpecBanner config={config} onChange={onChange} />

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
        help="Upstream read cap: the most the gateway reads of any one response (a page of a walk, an inline call). A transfer limit, not what reaches the model; see Max inline bytes. Default 10485760 (10 MiB)."
        type="number"
        value={String(config.max_response_bytes ?? "")}
        onChange={(v) => onChange(update(config, "max_response_bytes", v ? Number(v) : undefined))}
        placeholder="10485760"
      />

      <ConfigField
        label="Max inline bytes"
        help="Model-context budget: the most of a response api_invoke_endpoint returns in a tool result. Past it the body is cut, body_truncated is set, and export_arguments names the api_export call that streams the whole response into an asset. Default 131072 (128 KiB); the read cap bounds it."
        type="number"
        value={String(config.max_inline_bytes ?? "")}
        onChange={(v) => onChange(update(config, "max_inline_bytes", v ? Number(v) : undefined))}
        placeholder="131072"
      />

      <ConfigSelect
        label="Trust level"
        value={String(config.trust_level ?? "untrusted")}
        onChange={(v) => onChange(update(config, "trust_level", v))}
        options={TRUST_LEVELS}
        help="Reserved for future content-fencing of upstream responses."
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
