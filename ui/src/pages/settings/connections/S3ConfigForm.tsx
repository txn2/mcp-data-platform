import { Separator } from "@/components/ui/separator";
import {
  ConfigField,
  ConfigToggle,
  update,
  type ConfigFormProps,
} from "./fields";

// Editor form for kind=s3 connections. Field shape matches the mcp-s3 toolkit
// config plus the platform's datahub_source_name integration key.
export function S3ConfigForm({ config, onChange }: ConfigFormProps) {
  return (
    <>
      <ConfigField
        label="Endpoint"
        value={String(config.endpoint ?? "")}
        onChange={(v) => onChange(update(config, "endpoint", v))}
        placeholder="https://s3.amazonaws.com"
        mono
        help="S3-compatible endpoint URL. Leave blank for AWS S3. Set for MinIO, SeaweedFS, etc."
      />
      <div className="grid grid-cols-2 gap-4">
        <ConfigField
          label="Region"
          value={String(config.region ?? "")}
          onChange={(v) => onChange(update(config, "region", v))}
          placeholder="us-east-1"
          mono
          help="AWS region for the S3 service"
        />
        <ConfigField
          label="Bucket Prefix"
          value={String(config.bucket_prefix ?? "")}
          onChange={(v) => onChange(update(config, "bucket_prefix", v))}
          placeholder="data-lake-"
          mono
          help="Only show buckets matching this prefix"
        />
      </div>
      <div className="grid grid-cols-2 gap-4">
        <ConfigField
          label="Access Key ID"
          value={String(config.access_key_id ?? "")}
          onChange={(v) => onChange(update(config, "access_key_id", v))}
          placeholder="AKIA..."
          mono
          help="AWS access key ID or S3-compatible equivalent"
        />
        <ConfigField
          label="Secret Access Key"
          value={String(config.secret_access_key ?? "")}
          onChange={(v) => onChange(update(config, "secret_access_key", v))}
          placeholder="••••••••"
          sensitive
          help="Leave blank to keep existing secret"
        />
      </div>
      <ConfigToggle
        label="Force Path Style"
        checked={!!config.use_path_style}
        onChange={(v) => onChange(update(config, "use_path_style", v))}
        help="Use path-style URLs (bucket in path, not subdomain). Required for MinIO and most S3-compatible stores."
      />
      <Separator className="mt-2" />
      <div className="space-y-4">
        <p className="text-xs font-medium">DataHub Integration</p>
        <ConfigField
          label="DataHub Source Name"
          value={String(config.datahub_source_name ?? "")}
          onChange={(v) => onChange(update(config, "datahub_source_name", v))}
          placeholder="s3"
          mono
          help="The platform identifier in DataHub URNs for datasets accessible through this connection. Defaults to s3 if not set."
        />
      </div>
    </>
  );
}
