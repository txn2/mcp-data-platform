import { useState } from "react";
import { HelpDialog } from "@/components/HelpDialog";
import { ApiGatewayAuthHelp, ApiGatewayTLSHelp } from "../ApiGatewayHelpContent";
import { ApiGatewayAuthFields } from "./ApiGatewayAuthFields";
import { ConfigField, ConfigGroup, update, type ConfigFormProps } from "./fields";
import { TLSMaterialEditor } from "./TlsMaterialEditor";

// CalDAVConfigForm renders the editor for kind=caldav connections (#2246).
// The credential and TLS fields are the HTTP kinds' own, read through
// internal/upstreamauth; the address, zone and limits below are this kind's
// (pkg/toolkits/caldav/config.go). Most servers take Basic auth with an app
// password.
export function CalDAVConfigForm({
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
        label="Account URL"
        help="The calendar account's address: the server root, the account's principal, or its calendar home. The calendars are found from any of the three."
        value={String(config.url ?? "")}
        onChange={(v) => onChange(update(config, "url", v))}
        placeholder="https://mail.example.com/dav/"
        mono
        required
      />
      <ApiGatewayAuthFields
        config={config}
        onChange={onChange}
        kind="caldav"
        connectionName={connectionName}
        isCreate={isCreate}
        onOpenHelp={() => setAuthHelpOpen(true)}
      />
      <TLSMaterialEditor config={config} onChange={onChange} onOpenHelp={() => setTlsHelpOpen(true)} />
      <ConfigGroup title="Events">
        <ConfigField
          label="Time zone"
          help="IANA zone a time written without a zone is read in, and a YYYY-MM-DD date's midnight. Defaults to UTC."
          value={String(config.time_zone ?? "")}
          onChange={(v) => onChange(update(config, "time_zone", v || undefined))}
          placeholder="Europe/Berlin"
          mono
        />
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <ConfigField
            label="Max events"
            type="number"
            help="Most occurrences one call returns (1-5000). Defaults to 500."
            value={config.max_events == null ? "" : String(config.max_events)}
            onChange={(v) => onChange(update(config, "max_events", v ? Number(v) : undefined))}
            placeholder="500"
          />
          <ConfigField
            label="Max range (days)"
            type="number"
            help="Longest time range one call may ask about. Defaults to 366."
            value={config.max_range_days == null ? "" : String(config.max_range_days)}
            onChange={(v) => onChange(update(config, "max_range_days", v ? Number(v) : undefined))}
            placeholder="366"
          />
        </div>
      </ConfigGroup>
      <HelpDialog open={authHelpOpen} onOpenChange={setAuthHelpOpen} title="Authentication">
        <ApiGatewayAuthHelp />
      </HelpDialog>
      <HelpDialog open={tlsHelpOpen} onOpenChange={setTlsHelpOpen} title="TLS material">
        <ApiGatewayTLSHelp />
      </HelpDialog>
    </>
  );
}
