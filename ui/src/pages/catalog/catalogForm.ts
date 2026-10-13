import type { CatalogExclusion, CatalogScan, ConnectionInstance } from "@/api/admin/types";

// The Settings tab's editable form, and the translation to and from the
// settings the API stores (#1918).

/** One exclusion row: a connection, and what it leaves out as the text the
 * field shows. */
export interface ExclusionRow {
  connection: string;
  leaveOut: string;
}

export interface CatalogForm {
  scanEvery: string;
  rows: ExclusionRow[];
}

/** connectionKey names a connection by kind and name together, since a name
 * alone does not identify one. */
export function connectionKey(kind: string, name: string): string {
  return `${kind}/${name}`;
}

function splitKey(key: string): { kind: string; name: string } {
  const i = key.indexOf("/");
  return i < 0 ? { kind: "", name: key } : { kind: key.slice(0, i), name: key.slice(i + 1) };
}

/** parseEntries reads the comma- or line-separated prefixes a person typed. */
export function parseEntries(text: string): string[] {
  return text
    .split(/[,\n]/)
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

export function formFromSettings(scanInterval: string, exclusions: CatalogExclusion[]): CatalogForm {
  return {
    scanEvery: scanInterval,
    rows: exclusions.map((x) => ({
      connection: connectionKey(x.connection_kind, x.connection),
      leaveOut: x.entries.join(", "),
    })),
  };
}

/** settingsFromForm drops a row that names no connection or leaves nothing
 * out, so an added-then-abandoned row is not saved. */
export function settingsFromForm(form: CatalogForm): { scan_interval: string; exclusions: CatalogExclusion[] } {
  const exclusions: CatalogExclusion[] = [];
  for (const row of form.rows) {
    const entries = parseEntries(row.leaveOut);
    if (row.connection === "" || entries.length === 0) continue;
    const { kind, name } = splitKey(row.connection);
    exclusions.push({ connection_kind: kind, connection: name, entries });
  }
  return { scan_interval: form.scanEvery.trim(), exclusions };
}

/** connectionOptions lists every connection a scan has reported or the
 * database holds, each once, by name. */
export function connectionOptions(
  scans: CatalogScan[] | undefined,
  instances: ConnectionInstance[] | undefined,
): { value: string; label: string }[] {
  const seen = new Map<string, string>();
  for (const s of scans ?? []) seen.set(connectionKey(s.connection_kind, s.connection), `${s.connection} (${s.connection_kind})`);
  for (const c of instances ?? []) seen.set(connectionKey(c.kind, c.name), `${c.name} (${c.kind})`);
  return [...seen.entries()]
    .map(([value, label]) => ({ value, label }))
    .sort((a, b) => a.label.localeCompare(b.label));
}
