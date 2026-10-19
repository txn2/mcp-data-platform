export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  // Below one byte the unit index went to -1, so a rate of 0.149 B/day read
  // as "152.2 undefined" (#2201).
  if (Math.abs(bytes) < 1) return "< 1 B";
  if (bytes < 0) return `-${formatBytes(-bytes)}`;
  const k = 1024;
  // Through GB and TB, and clamped to the last unit. The list stopped at MB,
  // so anything a gigabyte or over read as "2 undefined" -- reachable from a
  // deployment that raises resources.managed.max_upload_bytes past a gigabyte
  // and from any stored file that size (#1628).
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1);
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
}

export function formatOwner(asset: { owner_email: string; owner_id: string }): string {
  return asset.owner_email || asset.owner_id;
}
