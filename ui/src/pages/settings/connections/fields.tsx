import { useId } from "react";

import { SectionCard } from "@/components/patterns/SectionCard";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

// Shared primitives for the kind-specific connection configuration forms.
// Extracted from ConnectionsPanel.tsx (#766) so each per-kind form stays a
// small, independently reviewable file that composes these controlled inputs.

export interface ConfigFormProps {
  config: Record<string, unknown>;
  onChange: (config: Record<string, unknown>) => void;
}

// ConfigField is one labelled config value: an ui/input tied to its ui/label
// by a generated id, with the help text below. `sensitive` swaps the control
// to a password input and turns autofill off.
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
  const id = useId();
  const helpID = `${id}-help`;
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="gap-0.5 text-xs">
        {label}
        {required && <span className="text-destructive">*</span>}
      </Label>
      <Input
        id={id}
        type={sensitive ? "password" : type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoComplete={sensitive ? "off" : undefined}
        aria-describedby={help ? helpID : undefined}
        className={cn(mono && "font-mono")}
      />
      {help && (
        <p id={helpID} className="text-xs text-muted-foreground">
          {help}
        </p>
      )}
    </div>
  );
}

// ConfigGroup boxes the fields that only exist together — an auth mode's
// credentials, the TLS material, the static headers. It is a SectionCard on a
// muted surface so a group reads as a subsection of the Configuration card
// that contains it rather than a second card of equal weight.
export function ConfigGroup({
  title,
  action,
  children,
}: {
  title: React.ReactNode;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <SectionCard
      title={title}
      action={action}
      className="gap-2 rounded-md bg-muted/30 py-3 shadow-none"
    >
      <div className="space-y-3">{children}</div>
    </SectionCard>
  );
}

export interface ConfigOption {
  value: string;
  label: string;
}

// A Radix listbox item cannot carry an empty value, but several connection
// settings use "" for "unset" (no OIDC prompt parameter, no catalog). The
// empty choice travels under this sentinel and is translated back here, so no
// caller has to know about it.
const UNSET = "__unset__";

// ConfigSelect is one labelled choice among a fixed option set. `action` puts
// a control (a help link, typically) on the label's own row.
export function ConfigSelect({
  label,
  help,
  value,
  onChange,
  options,
  action,
  disabled,
}: {
  label: string;
  help?: React.ReactNode;
  value: string;
  onChange: (v: string) => void;
  options: ConfigOption[];
  action?: React.ReactNode;
  disabled?: boolean;
}) {
  const id = useId();
  const helpID = `${id}-help`;
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={id} className="text-xs">
          {label}
        </Label>
        {action}
      </div>
      <Select
        value={value === "" ? UNSET : value}
        onValueChange={(v) => onChange(v === UNSET ? "" : v)}
        disabled={disabled}
      >
        <SelectTrigger
          id={id}
          className="w-full"
          aria-describedby={help ? helpID : undefined}
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value === "" ? UNSET : o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {help && (
        <p id={helpID} className="text-xs text-muted-foreground">
          {help}
        </p>
      )}
    </div>
  );
}

// ConfigToggle is a boolean config value. It stays a hand-built switch rather
// than a vendored primitive: shadcn's Switch needs @radix-ui/react-switch,
// which this app does not depend on, and one accessible role="switch" button
// is cheaper than the dependency.
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
  const id = useId();
  return (
    <div className="flex items-start gap-3">
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={cn(
          "relative mt-0.5 inline-flex h-5 w-9 shrink-0 rounded-full border-2 border-transparent transition-colors outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
          disabled ? "cursor-not-allowed opacity-60" : "cursor-pointer",
          checked ? "bg-primary" : "bg-muted",
        )}
      >
        <span
          className={cn(
            // The knob takes whichever surface is furthest from the off-state
            // `bg-muted` track in this mode — card in light, page in dark —
            // the same rule the image viewer's checkerboard follows.
            "pointer-events-none block h-4 w-4 rounded-full bg-card shadow-sm transition-transform dark:bg-background",
            checked ? "translate-x-4" : "translate-x-0",
          )}
        />
      </button>
      <div className="space-y-0.5">
        <Label htmlFor={id} className="text-xs">
          {label}
        </Label>
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
