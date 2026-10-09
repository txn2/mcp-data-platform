import { useCallback, useEffect, useState } from "react";
import { Map as MapIcon, RefreshCw, Trash2 } from "lucide-react";
import {
  useAddMapRegion,
  useDeleteMapRegion,
  useEstimateMapRegion,
  useMaps,
  useRefreshMapRegion,
  useSetMapSettings,
} from "@/api/admin/hooks";
import type { MapBounds, MapRegion, MapSettings } from "@/api/admin/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { formatBytes } from "@/lib/format";
import { ConfigField, ConfigSelect, ConfigToggle } from "./connections/fields";
import { SettingsCard } from "./panels";
import {
  ErrorBanner,
  SaveButton,
  SaveFeedbackBanners,
  UpdatedByMeta,
  WarningBanner,
} from "./settingsChrome";

// The street maps settings section (#2068).
//
// What it configures is the basemap a map asset draws on: whether this
// deployment serves one, where its archives are written, how deep they go and
// which build they are cut from, and the regions kept. A region's fetch runs
// in the background; its state is read here until it settles.

interface FormState {
  enabled: boolean;
  s3_connection: string;
  bucket: string;
  max_zoom: string;
  source_url: string;
}

function formFrom(s: MapSettings): FormState {
  return {
    enabled: s.enabled,
    s3_connection: s.s3_connection,
    bucket: s.bucket,
    max_zoom: String(s.max_zoom),
    source_url: s.source_url,
  };
}

const ZOOM_OPTIONS = Array.from({ length: 15 }, (_, i) => ({
  value: String(i + 1),
  label: String(i + 1),
}));

export function MapsCard() {
  const { data, isLoading, error: loadError, refetch } = useMaps();
  const save = useSetMapSettings();

  const [form, setForm] = useState<FormState | null>(null);
  const [dirty, setDirty] = useState(false);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  useEffect(() => {
    if (!data || dirty) return;
    setForm(formFrom(data.settings));
  }, [data, dirty]);

  const handleChange = useCallback((patch: Partial<FormState>) => {
    setForm((prev) => (prev ? { ...prev, ...patch } : prev));
    setDirty(true);
    setSaveSuccess(false);
    setSaveError(null);
  }, []);

  const handleSave = useCallback(() => {
    if (!form) return;
    setSaveError(null);
    save.mutate(
      {
        enabled: form.enabled,
        s3_connection: form.s3_connection.trim(),
        bucket: form.bucket.trim(),
        max_zoom: parseInt(form.max_zoom, 10),
        source_url: form.source_url.trim(),
      },
      {
        onSuccess: () => {
          setDirty(false);
          setSaveSuccess(true);
          setTimeout(() => setSaveSuccess(false), 2500);
        },
        onError: (err) => setSaveError(err instanceof Error ? err.message : "Failed to save"),
      },
    );
  }, [form, save]);

  return (
    <SettingsCard
      icon={MapIcon}
      title="Maps"
      description="The basemap a map asset draws streets on. Each region is cut from an OpenStreetMap build into your bucket and served by this deployment, so no map view and no thumbnail calls a third party."
      notices={
        loadError ? (
          <ErrorBanner message="The maps settings could not be read." onRetry={() => void refetch()} />
        ) : data && !data.settings.enabled && data.regions.length > 0 ? (
          <WarningBanner>
            Maps are off. The regions below are kept in the bucket and are not served, and the maps knowledge
            page tells agents this deployment has no basemap.
          </WarningBanner>
        ) : undefined
      }
      feedback={<SaveFeedbackBanners saveError={saveError} dirty={dirty} />}
      action={
        <>
          <UpdatedByMeta updatedBy={data?.settings.updated_by} updatedAt={data?.settings.updated_at} />
          <SaveButton dirty={dirty} saving={save.isPending} saveSuccess={saveSuccess} onSave={handleSave} />
        </>
      }
    >
      {isLoading || !form || !data ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <div className="space-y-6">
          <div className="space-y-4">
            <ConfigToggle
              label="Enabled"
              help="Serve the regions below to map assets and keep fetching them. Off, the archives stay in the bucket and are not served; on again, they serve at once."
              checked={form.enabled}
              onChange={(v) => handleChange({ enabled: v })}
            />
            <div className="grid gap-4 sm:grid-cols-2">
              <ConfigField
                label="S3 connection"
                help="The S3 connection archives are written through. Empty is the managed-resources connection."
                value={form.s3_connection}
                onChange={(v) => handleChange({ s3_connection: v })}
                placeholder="managed resources"
                mono
              />
              <ConfigField
                label="Bucket"
                help={`Empty is the managed-resources bucket (${data.settings.resolved_bucket}).`}
                value={form.bucket}
                onChange={(v) => handleChange({ bucket: v })}
                placeholder={data.settings.resolved_bucket}
                mono
              />
              <ConfigSelect
                label="Maximum zoom"
                help="The deepest zoom a fetch extracts. Streets read clearly from 14, which is drawn sharp beyond it; each step deeper is about twice the size."
                value={form.max_zoom}
                onChange={(v) => handleChange({ max_zoom: v })}
                options={ZOOM_OPTIONS}
              />
              <ConfigField
                label="Source"
                help={`The .pmtiles archive a fetch reads. Empty is ${data.default_source}; a URL is a mirror you host.`}
                value={form.source_url}
                onChange={(v) => handleChange({ source_url: v })}
                placeholder="https://"
                mono
              />
            </div>
          </div>
          <RegionList regions={data.regions} />
          <AddRegion presets={data.presets} />
          <p className="text-xs text-muted-foreground">
            On a closed network, skip the fetch: put a .pmtiles file in{" "}
            <code className="font-mono">
              {data.settings.resolved_bucket}/{data.upload_prefix}
            </code>{" "}
            and it is listed here as a region named by its file name.
          </p>
        </div>
      )}
    </SettingsCard>
  );
}

const STATE_LABEL: Record<MapRegion["state"], string> = {
  queued: "Queued",
  fetching: "Fetching",
  ready: "Ready",
  failed: "Failed",
};

const STATE_VARIANT: Record<MapRegion["state"], "muted" | "info" | "success" | "destructive"> = {
  queued: "muted",
  fetching: "info",
  ready: "success",
  failed: "destructive",
};

function RegionList({ regions }: { regions: MapRegion[] }) {
  const refresh = useRefreshMapRegion();
  const remove = useDeleteMapRegion();
  const [deleting, setDeleting] = useState<MapRegion | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  if (regions.length === 0) {
    return <p className="text-sm text-muted-foreground">No regions yet. Add one below.</p>;
  }
  return (
    <div className="space-y-2">
      {actionError && <ErrorBanner message={actionError} />}
      <div className="overflow-x-auto rounded-md border">
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-xs text-muted-foreground">
            <tr>
              <th className="px-3 py-2 text-left font-medium">Region</th>
              <th className="px-3 py-2 text-left font-medium">State</th>
              <th className="px-3 py-2 text-left font-medium">Archive</th>
              <th className="px-3 py-2 text-left font-medium">Built from</th>
              <th className="px-3 py-2 text-left font-medium">Size</th>
              <th className="px-3 py-2" />
            </tr>
          </thead>
          <tbody>
            {regions.map((r) => (
              <tr key={r.id} className="border-t align-top">
                <td className="px-3 py-2">
                  <div className="font-medium">{r.name}</div>
                  <div className="font-mono text-xs text-muted-foreground">{r.id}</div>
                </td>
                <td className="px-3 py-2">
                  <RegionState region={r} />
                </td>
                <td className="px-3 py-2 font-mono text-xs">
                  {r.archive ? `/portal/maps/${r.id}.pmtiles` : "none yet"}
                  {r.archive && (
                    <div className="text-muted-foreground">
                      zoom {r.archive.min_zoom} to {r.archive.max_zoom}
                    </div>
                  )}
                </td>
                <td className="px-3 py-2 text-xs">{r.archive ? `OpenStreetMap, ${r.archive.build}` : ""}</td>
                <td className="px-3 py-2 text-xs">{r.archive ? formatBytes(r.archive.size_bytes) : ""}</td>
                <td className="px-3 py-2">
                  <div className="flex justify-end gap-1">
                    {r.origin === "fetch" && (
                      <Button
                        variant="ghost"
                        size="xs"
                        disabled={r.state === "fetching" || r.state === "queued" || refresh.isPending}
                        onClick={() => {
                          setActionError(null);
                          refresh.mutate(r.id, {
                            onError: (e) => setActionError(e instanceof Error ? e.message : "Refresh failed"),
                          });
                        }}
                      >
                        <RefreshCw />
                        Refresh
                      </Button>
                    )}
                    <Button variant="ghost" size="xs" onClick={() => setDeleting(r)}>
                      <Trash2 />
                      Delete
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={`Delete ${deleting?.name ?? "region"}?`}
        description="Its archive is removed from the bucket, and a map that names this region stops drawing its streets."
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          if (!deleting) return;
          await remove.mutateAsync(deleting.id);
          setDeleting(null);
        }}
      />
    </div>
  );
}

function RegionState({ region: r }: { region: MapRegion }) {
  const pct = r.total_bytes > 0 ? Math.min(100, Math.round((r.progress_bytes / r.total_bytes) * 100)) : 0;
  return (
    <div className="space-y-1">
      <Badge variant={STATE_VARIANT[r.state]}>{STATE_LABEL[r.state]}</Badge>
      {r.state === "fetching" && (
        <div className="w-40 space-y-1">
          <div
            className="h-1.5 overflow-hidden rounded-full bg-muted"
            role="progressbar"
            aria-valuenow={pct}
            aria-valuemin={0}
            aria-valuemax={100}
          >
            <div className="h-full bg-primary transition-all" style={{ width: `${pct}%` }} />
          </div>
          <div className="text-xs text-muted-foreground">
            {r.total_bytes > 0
              ? `${formatBytes(r.progress_bytes)} of ${formatBytes(r.total_bytes)}`
              : "reading the source"}
          </div>
        </div>
      )}
      {r.state === "failed" && r.error && <div className="max-w-xs text-xs text-destructive">{r.error}</div>}
      {r.state === "failed" && r.archive && (
        <div className="text-xs text-muted-foreground">Still serving the {r.archive.build} build.</div>
      )}
    </div>
  );
}

const CUSTOM = "custom";

function AddRegion({ presets }: { presets: { id: string; name: string; bounds: MapBounds }[] }) {
  const add = useAddMapRegion();
  const estimate = useEstimateMapRegion();
  const [choice, setChoice] = useState(presets[0]?.id ?? CUSTOM);
  const [box, setBox] = useState({ id: "", name: "", west: "", south: "", east: "", north: "" });
  const [error, setError] = useState<string | null>(null);

  const custom = choice === CUSTOM;
  const bounds = (): MapBounds => ({
    min_lon: Number(box.west),
    min_lat: Number(box.south),
    max_lon: Number(box.east),
    max_lat: Number(box.north),
  });
  const target = () => (custom ? { bounds: bounds() } : { preset: choice });

  const options = [
    ...presets.map((p) => ({ value: p.id, label: p.name })),
    { value: CUSTOM, label: "Custom box" },
  ];
  const setField = (k: keyof typeof box) => (v: string) => {
    setBox((b) => ({ ...b, [k]: v }));
    estimate.reset();
  };

  return (
    <div className="space-y-3 rounded-md border p-4">
      <div className="text-sm font-medium">Add region</div>
      <div className="grid gap-4 sm:grid-cols-2">
        <ConfigSelect
          label="Preset"
          value={choice}
          onChange={(v) => {
            setChoice(v || CUSTOM);
            estimate.reset();
          }}
          options={options}
        />
      </div>
      {custom && (
        <div className="grid gap-4 sm:grid-cols-2">
          <ConfigField label="ID" value={box.id} onChange={setField("id")} placeholder="bay-area" mono />
          <ConfigField label="Name" value={box.name} onChange={setField("name")} placeholder="Bay Area" />
          <ConfigField label="West" type="number" value={box.west} onChange={setField("west")} placeholder="-122.6" mono />
          <ConfigField label="South" type="number" value={box.south} onChange={setField("south")} placeholder="37.2" mono />
          <ConfigField label="East" type="number" value={box.east} onChange={setField("east")} placeholder="-121.7" mono />
          <ConfigField label="North" type="number" value={box.north} onChange={setField("north")} placeholder="38.0" mono />
        </div>
      )}
      {error && <ErrorBanner message={error} />}
      {estimate.data && (
        <p className="text-xs text-muted-foreground">
          {formatBytes(estimate.data.size_bytes)}, {estimate.data.tiles.toLocaleString()} tiles at zoom{" "}
          {estimate.data.min_zoom} to {estimate.data.max_zoom}, from the OpenStreetMap build of {estimate.data.build}.
        </p>
      )}
      <div className="flex gap-2">
        <Button
          variant="outline"
          size="sm"
          disabled={estimate.isPending}
          onClick={() => {
            setError(null);
            estimate.mutate(target(), {
              onError: (e) => setError(e instanceof Error ? e.message : "The estimate failed"),
            });
          }}
        >
          {estimate.isPending ? "Estimating..." : "Estimate size"}
        </Button>
        <Button
          size="sm"
          disabled={add.isPending}
          onClick={() => {
            setError(null);
            const input = custom ? { id: box.id.trim(), name: box.name.trim(), bounds: bounds() } : { preset: choice };
            add.mutate(input, {
              onSuccess: () => {
                estimate.reset();
                setBox({ id: "", name: "", west: "", south: "", east: "", north: "" });
              },
              onError: (e) => setError(e instanceof Error ? e.message : "The region could not be added"),
            });
          }}
        >
          Add region
        </Button>
      </div>
    </div>
  );
}
