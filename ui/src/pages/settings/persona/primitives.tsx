import { X, ChevronDown, ChevronRight } from "lucide-react";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { cn } from "@/lib/utils";

// Small presentational building blocks for the persona editor. Extracted from
// PersonaEditor.tsx (#766); none hold cross-cutting state — they render label
// chrome and forward events to the editor's handlers.

export function Section({
  title,
  meta,
  description,
  children,
  collapsible,
  open,
  onToggle,
}: {
  title: string;
  meta?: React.ReactNode;
  description?: string;
  children: React.ReactNode;
  collapsible?: boolean;
  open?: boolean;
  onToggle?: () => void;
}) {
  return (
    <div className="border-b px-4 py-3 last:border-b-0">
      <div
        className={cn(
          "mb-2 flex items-center justify-between",
          collapsible && "cursor-pointer select-none",
        )}
        onClick={collapsible ? onToggle : undefined}
      >
        <div className="flex items-center gap-1.5">
          {collapsible &&
            (open ? (
              <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
            ) : (
              <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
            ))}
          <h3 className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
            {title}
          </h3>
        </div>
        {meta}
      </div>
      {description && (!collapsible || open) && (
        <p className="mb-2 text-[11px] leading-snug text-muted-foreground">
          {description}
        </p>
      )}
      {(!collapsible || open) && children}
    </div>
  );
}

export function Field({
  label,
  required,
  children,
}: {
  label: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="mb-2.5 last:mb-0">
      <label className="mb-1 block text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
        {required && <span className="ml-0.5 text-rose-600">*</span>}
      </label>
      {children}
    </div>
  );
}

export function CtxField({
  label,
  value,
  onChange,
  minHeight = "80px",
  readOnly = false,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  minHeight?: string;
  readOnly?: boolean;
}) {
  return (
    <div>
      <label className="mb-1 block text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
      </label>
      <MarkdownEditor
        value={value}
        onChange={onChange}
        minHeight={minHeight}
        readOnly={readOnly}
      />
    </div>
  );
}

export function MainTab({
  active,
  label,
  onClick,
}: {
  active: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "border-b-2 px-4 py-2.5 text-xs font-medium transition-colors",
        active
          ? "border-primary text-foreground"
          : "border-transparent text-muted-foreground hover:text-foreground",
      )}
    >
      {label}
    </button>
  );
}

export function ChipInput({
  values,
  onAdd,
  onRemove,
  draft,
  onDraftChange,
  placeholder,
}: {
  values: string[];
  onAdd: (v: string) => void;
  onRemove: (v: string) => void;
  draft: string;
  onDraftChange: (s: string) => void;
  placeholder?: string;
}) {
  return (
    <div className="flex flex-wrap gap-1 rounded-md border bg-background p-1.5 focus-within:ring-2 focus-within:ring-ring">
      {values.map((v) => (
        <span
          key={v}
          className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 font-mono text-[10px]"
        >
          {v}
          <button
            type="button"
            onClick={() => onRemove(v)}
            className="text-muted-foreground hover:text-foreground"
          >
            <X className="h-2.5 w-2.5" />
          </button>
        </span>
      ))}
      <input
        type="text"
        value={draft}
        onChange={(e) => onDraftChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            if (draft.trim()) onAdd(draft);
          } else if (
            e.key === "Backspace" &&
            !draft &&
            values.length > 0
          ) {
            onRemove(values[values.length - 1]!);
          }
        }}
        onBlur={() => {
          if (draft.trim()) onAdd(draft);
        }}
        placeholder={values.length === 0 ? placeholder : ""}
        className="flex-1 min-w-[80px] bg-transparent text-[11px] outline-none placeholder:text-muted-foreground"
      />
    </div>
  );
}

export function ScopeTab({
  active,
  count,
  label,
  onClick,
}: {
  active: boolean;
  count: number;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "flex items-center gap-2 px-3 py-1.5 text-xs font-medium transition-colors",
        active
          ? "border-b-2 border-primary text-foreground"
          : "text-muted-foreground hover:text-foreground",
      )}
    >
      {label}
      <span
        className={cn(
          "rounded px-1.5 py-0.5 font-mono text-[10px]",
          active
            ? "bg-muted text-foreground"
            : "bg-muted/50 text-muted-foreground",
        )}
      >
        {count}
      </span>
    </button>
  );
}

export function TemplateRow({
  name,
  hint,
  onApply,
}: {
  name: string;
  hint: string;
  onApply: () => void;
}) {
  return (
    <button
      onClick={onApply}
      className="flex w-full items-center justify-between rounded-md border bg-background px-2.5 py-1.5 text-left transition-colors hover:border-primary/40 hover:bg-muted/40"
    >
      <div>
        <div className="text-[11px] font-semibold">{name}</div>
        <div className="mt-0.5 text-[10px] text-muted-foreground">{hint}</div>
      </div>
      <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
    </button>
  );
}
