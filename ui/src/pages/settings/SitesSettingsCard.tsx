import { useEffect, useState } from "react";
import { Globe } from "lucide-react";
import { useSaveSiteSettings, useSiteSettings, type SiteSettings } from "@/api/admin/hooks";
import { ConfigField } from "./connections/fields";
import { keepValid, ReleasesFields } from "./SitesRetention";
import { SettingsCard } from "./panels";
import { ErrorBanner, SaveButton, SaveFeedbackBanners, UpdatedByMeta } from "./settingsChrome";

const MIB = 1 << 20;
const GIB = 1 << 30;

interface Draft {
  domain: string;
  files: string;
  fileMiB: string;
  releaseGiB: string;
  pages: string;
  /** How many releases an environment keeps by default (#2602); 0 keeps every release. */
  keep: string;
  mode: "delete" | "report";
}

function draftOf(s: SiteSettings): Draft {
  return {
    domain: s.pages_domain,
    files: String(s.max_release_files),
    fileMiB: String(s.max_file_bytes / MIB),
    releaseGiB: String(s.max_release_bytes / GIB),
    pages: String(s.crawl_max_pages),
    keep: String(s.releases_keep ?? 50),
    mode: s.releases_mode ?? "delete",
  };
}

// SitesSettingsCard is where a site's own address and application pages are
// served, and how large a release can be (#2559). A save applies on every
// replica without a restart.
export function SitesSettingsCard() {
  const { data, isLoading, error } = useSiteSettings();
  const save = useSaveSiteSettings();
  const [draft, setDraft] = useState<Draft | null>(null);
  const [saved, setSaved] = useState(false);
  useEffect(() => {
    if (data) setDraft(draftOf(data));
  }, [data]);
  const set = <K extends keyof Draft>(k: K) => (v: Draft[K]) => {
    setSaved(false);
    save.reset();
    setDraft((d) => (d ? { ...d, [k]: v } : d));
  };
  const dirty = isDirty(draft, data);
  // An emptied or non-numeric "Releases kept" is not saved as 0 (every release) or dropped.
  const savable = dirty && !!draft && keepValid(draft.keep);
  const submit = () => {
    if (draft && data && keepValid(draft.keep)) save.mutate(toSettings(draft, data), { onSuccess: () => setSaved(true) });
  };
  return (
    <SettingsCard
      icon={Globe}
      title="Sites"
      description="Where a site's own address and application pages are served, how large a release can be, and how many releases are kept."
      action={
        <div className="flex items-center gap-3">
          <UpdatedByMeta updatedBy={data?.updated_by} updatedAt={data?.updated_at} />
          <SaveButton dirty={savable} saving={save.isPending} saveSuccess={saved} onSave={submit} />
        </div>
      }
      feedback={<SaveFeedbackBanners saveError={save.error?.message ?? null} dirty={dirty} />}
    >
      {error && <ErrorBanner message={error.message} />}
      {isLoading || !draft ? <p className="text-sm text-muted-foreground">Loading…</p> : <Fields draft={draft} set={set} />}
    </SettingsCard>
  );
}

function isDirty(draft: Draft | null, data?: SiteSettings): boolean {
  return !!draft && !!data && JSON.stringify(draft) !== JSON.stringify(draftOf(data));
}

function toSettings(d: Draft, data: SiteSettings): SiteSettings {
  return {
    ...data,
    pages_domain: d.domain.trim(),
    max_release_files: Number(d.files),
    max_file_bytes: Number(d.fileMiB) * MIB,
    max_release_bytes: Number(d.releaseGiB) * GIB,
    crawl_max_pages: Number(d.pages),
    releases_keep: Number(d.keep.trim()),
    releases_mode: d.mode,
  };
}

function Fields({ draft, set }: { draft: Draft; set: <K extends keyof Draft>(k: K) => (v: Draft[K]) => void }) {
  const domain = draft.domain.trim() || "pages.example.net";
  return (
    <div className="space-y-4">
      <ConfigField
        label="Pages domain"
        value={draft.domain}
        onChange={set("domain")}
        mono
        help={`A site's own address is <site>.${domain} and application pages are at <application>.${domain}. A system administrator adds the DNS and ingress for them. Use a registrable domain of its own, not a sibling of the portal's hostname.`}
      />
      <div className="grid gap-4 sm:grid-cols-2">
        <ConfigField label="Most files in a release" type="number" value={draft.files} onChange={set("files")} />
        <ConfigField label="Largest file (MiB)" type="number" value={draft.fileMiB} onChange={set("fileMiB")} />
        <ConfigField label="Largest release (GiB)" type="number" value={draft.releaseGiB} onChange={set("releaseGiB")} />
        <ConfigField label="Most pages when copying a site" type="number" value={draft.pages} onChange={set("pages")} />
      </div>
      <ReleasesFields keep={draft.keep} mode={draft.mode} onKeep={set("keep")} onMode={set("mode")} />
    </div>
  );
}
