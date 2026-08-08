import { cn } from "@/lib/utils";

// MetaGrid is how a capture drawer states a record's identity: paired
// label-over-value fields, two to a row, so the whole header is scannable
// without reading any of it.
export function MetaGrid({
  className,
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={cn("grid grid-cols-2 gap-3 text-sm", className)}>{children}</div>
  );
}

// MetaField is one such pair. `mono` is for machine identity (ids, URNs) and
// `wide` spans both columns for a value too long to sit in half a row.
export function MetaField({
  label,
  mono = false,
  wide = false,
  title,
  children,
}: {
  label: string;
  mono?: boolean;
  wide?: boolean;
  title?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={cn(wide && "col-span-2")}>
      <p className="text-xs text-muted-foreground">{label}</p>
      <div className={cn(mono && "break-all font-mono text-xs")} title={title}>
        {children}
      </div>
    </div>
  );
}

// LabeledBlock is a named block of content under the field grid: the insight
// text, a JSON payload, a list of ids.
export function LabeledBlock({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <p className="mb-1 text-xs text-muted-foreground">{label}</p>
      {children}
    </div>
  );
}

// JsonBlock renders a stored payload as it is: pretty-printed, scrolling in its
// own box rather than stretching the drawer.
export function JsonBlock({ value }: { value: unknown }) {
  return (
    <pre className="overflow-auto rounded bg-muted p-3 text-xs">
      {JSON.stringify(value, null, 2)}
    </pre>
  );
}
