import { useState, useEffect, useCallback, useRef } from "react";
import { Plus, X } from "lucide-react";
import { cn } from "@/lib/utils";

// Shared primitives for the kind-specific connection configuration forms.
// Extracted from ConnectionsPanel.tsx (#766) so each per-kind form stays a
// small, independently reviewable file that composes these controlled inputs.

export interface ConfigFormProps {
  config: Record<string, unknown>;
  onChange: (config: Record<string, unknown>) => void;
}

export function ConfigField({
  label,
  help,
  value,
  onChange,
  type = "text",
  placeholder,
  mono,
  sensitive,
  required,
}: {
  label: string;
  help?: string;
  value: string;
  onChange: (v: string) => void;
  type?: "text" | "number";
  placeholder?: string;
  mono?: boolean;
  sensitive?: boolean;
  required?: boolean;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs font-medium">
        {label}
        {required && <span className="text-destructive ml-0.5">*</span>}
      </label>
      <input
        type={sensitive ? "password" : type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoComplete={sensitive ? "off" : undefined}
        className={cn(
          "w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2",
          mono && "font-mono",
        )}
      />
      {help && <p className="mt-1 text-xs text-muted-foreground">{help}</p>}
    </div>
  );
}

export function ConfigToggle({
  label,
  help,
  checked,
  onChange,
  disabled = false,
}: {
  label: string;
  help?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  // disabled renders the switch inert while keeping its stored value visible,
  // for states where the setting exists but cannot currently take effect.
  disabled?: boolean;
}) {
  return (
    <div className="flex items-start gap-3">
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={cn(
          "mt-0.5 relative inline-flex h-5 w-9 shrink-0 rounded-full border-2 border-transparent transition-colors",
          disabled ? "cursor-not-allowed opacity-60" : "cursor-pointer",
          checked ? "bg-primary" : "bg-muted",
        )}
      >
        <span
          className={cn(
            "pointer-events-none block h-4 w-4 rounded-full bg-background shadow-sm transition-transform",
            checked ? "translate-x-4" : "translate-x-0",
          )}
        />
      </button>
      <div>
        <label className="text-xs font-medium">{label}</label>
        {help && <p className="text-xs text-muted-foreground">{help}</p>}
      </div>
    </div>
  );
}

export function update(
  config: Record<string, unknown>,
  key: string,
  value: unknown,
): Record<string, unknown> {
  if (value === "" || value === undefined) {
    const next = { ...config };
    delete next[key];
    return next;
  }
  return { ...config, [key]: value };
}

export function KeyValueEditor({
  entries,
  onChange,
  keyPlaceholder,
  valuePlaceholder,
}: {
  entries: Record<string, string>;
  onChange: (entries: Record<string, string>) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
}) {
  const [newKey, setNewKey] = useState("");
  const [newValue, setNewValue] = useState("");
  const items = Object.entries(entries);

  const add = () => {
    const k = newKey.trim();
    const v = newValue.trim();
    if (k && v) {
      onChange({ ...entries, [k]: v });
      setNewKey("");
      setNewValue("");
    }
  };

  return (
    <div>
      {items.length > 0 && (
        <div className="rounded-md border overflow-hidden mb-2">
          <table className="w-full text-xs">
            <tbody>
              {items.map(([k, v]) => (
                <tr key={k} className="border-b last:border-0">
                  <td className="px-3 py-1.5 font-mono">{k}</td>
                  <td className="px-2 text-muted-foreground">→</td>
                  <td className="px-3 py-1.5 font-mono">{v}</td>
                  <td className="px-2">
                    <button
                      type="button"
                      onClick={() => {
                        const next = { ...entries };
                        delete next[k];
                        onChange(next);
                      }}
                      className="text-muted-foreground hover:text-destructive"
                    >
                      <X className="h-3 w-3" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="flex gap-2">
        <input
          type="text"
          value={newKey}
          onChange={(e) => setNewKey(e.target.value)}
          placeholder={keyPlaceholder ?? "key"}
          className="w-36 rounded-md border bg-background px-3 py-1.5 text-xs font-mono outline-none ring-ring focus:ring-2"
        />
        <input
          type="text"
          value={newValue}
          onChange={(e) => setNewValue(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(); } }}
          placeholder={valuePlaceholder ?? "value"}
          className="flex-1 rounded-md border bg-background px-3 py-1.5 text-xs font-mono outline-none ring-ring focus:ring-2"
        />
        <button
          type="button"
          onClick={add}
          disabled={!newKey.trim() || !newValue.trim()}
          className="rounded-md border px-2.5 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-30"
        >
          <Plus className="h-3 w-3" />
        </button>
      </div>
    </div>
  );
}

// asStringMap normalizes a possibly-undefined/array/scalar value into
// Record<string, string>. The platform's redaction layer returns
// `static_headers` with values of "[REDACTED]" (a string), so the
// editor just sees strings here either way.
export function asStringMap(raw: unknown): Record<string, string> {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return {};
  }
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
    if (typeof v === "string") {
      out[k] = v;
    }
  }
  return out;
}

// SensitiveKeyValueEditor renders one inline-editable row per entry
// plus an "Add header" button that appends a fresh empty row. Every
// keystroke commits to the parent's entries map (a row contributes
// only when BOTH name and value are non-empty), so there is no
// "pending uncommitted" state to lose at save time. Stable row IDs
// keep React's reconciliation correct as the user renames keys or
// removes rows out of order. Without IDs, deleting row 1 of 3 would
// look like row 1 had its values changed and row 3 disappeared.
//
// Existing rows come back from the server with value === "[REDACTED]"
// (the redaction mask). The password input renders that as dots; the
// operator selects-all and types to replace the value, and the
// backend's redaction-merge layer reinstates the stored value if the
// row is saved without a change.
export function SensitiveKeyValueEditor({
  entries,
  onChange,
  keyPlaceholder,
  valuePlaceholder,
}: {
  entries: Record<string, string>;
  onChange: (entries: Record<string, string>) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
}) {
  type Row = { id: number; name: string; value: string };
  const idSeq = useRef(0);
  const nextID = useCallback(() => ++idSeq.current, []);

  // Local rows are the source of truth for editing. Initial value is
  // derived from the entries prop on mount; later sync happens only
  // when the prop's KEY SET changes (a real refresh from the server),
  // not on every value change (which would clobber the user's
  // mid-edit local state after every save round-trip turning real
  // values into "[REDACTED]" masks).
  const [rows, setRows] = useState<Row[]>(() =>
    Object.entries(entries).map(([k, v]) => ({ id: nextID(), name: k, value: v })),
  );
  const lastKeySet = useRef(
    Object.keys(entries).slice().sort().join(""),
  );
  useEffect(() => {
    const k = Object.keys(entries).slice().sort().join("");
    if (k !== lastKeySet.current) {
      lastKeySet.current = k;
      setRows(
        Object.entries(entries).map(([key, val]) => ({
          id: nextID(),
          name: key,
          value: val,
        })),
      );
    }
  }, [entries, nextID]);

  const commit = useCallback(
    (updated: Row[]) => {
      setRows(updated);
      const out: Record<string, string> = {};
      for (const r of updated) {
        const n = r.name.trim();
        if (n && r.value.length > 0) {
          out[n] = r.value;
        }
      }
      // Update lastKeySet to match what we're about to emit so the
      // useEffect above doesn't fire a redundant re-sync after our
      // own commit propagates back through the entries prop.
      lastKeySet.current = Object.keys(out).slice().sort().join("");
      onChange(out);
    },
    [onChange],
  );

  const updateRow = useCallback(
    (id: number, patch: Partial<Row>) => {
      commit(rows.map((r) => (r.id === id ? { ...r, ...patch } : r)));
    },
    [rows, commit],
  );

  const deleteRow = useCallback(
    (id: number) => {
      commit(rows.filter((r) => r.id !== id));
    },
    [rows, commit],
  );

  const addRow = useCallback(() => {
    setRows((prev) => [...prev, { id: nextID(), name: "", value: "" }]);
  }, [nextID]);

  return (
    <div className="space-y-2">
      {rows.map((row) => (
        <div key={row.id} className="flex gap-2">
          <input
            type="text"
            value={row.name}
            onChange={(e) => updateRow(row.id, { name: e.target.value })}
            placeholder={keyPlaceholder ?? "header"}
            className="w-56 rounded-md border bg-background px-3 py-1.5 text-xs font-mono outline-none ring-ring focus:ring-2"
          />
          <input
            type="password"
            value={row.value}
            onChange={(e) => updateRow(row.id, { value: e.target.value })}
            placeholder={valuePlaceholder ?? "value"}
            className="flex-1 rounded-md border bg-background px-3 py-1.5 text-xs font-mono outline-none ring-ring focus:ring-2"
          />
          <button
            type="button"
            onClick={() => deleteRow(row.id)}
            className="rounded-md border px-2.5 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-destructive"
            aria-label={`Remove ${row.name || "header"}`}
          >
            <X className="h-3 w-3" />
          </button>
        </div>
      ))}
      <button
        type="button"
        onClick={addRow}
        className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
      >
        <Plus className="h-3 w-3" />
        Add header
      </button>
    </div>
  );
}
