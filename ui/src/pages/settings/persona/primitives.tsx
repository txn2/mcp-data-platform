import { ChevronDown, ChevronRight } from "lucide-react";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

// Small presentational building blocks for the persona editor. Extracted from
// PersonaEditor.tsx (#766); none hold cross-cutting state — they render label
// chrome and forward events to the editor's handlers.

// labelClass is the persona editor's field label: the rail is 300px wide and
// stacks a dozen labelled controls, so labels are small caps rather than the
// body-sized default.
const labelClass =
  "mb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground";

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
  htmlFor,
  required,
  children,
}: {
  label: string;
  // The id of the control this labels. Omitted for composite controls (the
  // role chip editor) that own no single focusable element.
  htmlFor?: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="mb-2.5 last:mb-0">
      <Label htmlFor={htmlFor} className={cn(labelClass, "gap-0.5")}>
        {label}
        {required && <span className="text-destructive">*</span>}
      </Label>
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
      <Label className={labelClass}>{label}</Label>
      <MarkdownEditor
        value={value}
        onChange={onChange}
        minHeight={minHeight}
        readOnly={readOnly}
      />
    </div>
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
    <Button
      type="button"
      variant="outline"
      onClick={onApply}
      className="h-auto w-full justify-between px-2.5 py-1.5 text-left font-normal hover:border-primary/40 hover:bg-muted/40"
    >
      <span className="block">
        <span className="block text-[11px] font-semibold">{name}</span>
        <span className="mt-0.5 block text-[10px] text-muted-foreground">{hint}</span>
      </span>
      <ChevronRight className="text-muted-foreground" />
    </Button>
  );
}
