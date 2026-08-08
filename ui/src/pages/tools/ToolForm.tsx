import { useState } from "react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type {
  EffectiveConnection,
  ToolSchema,
  ToolParameterSchema,
} from "@/api/admin/types";

interface ToolFormProps {
  schema: ToolSchema;
  selectedConnection: string;
  initialValues?: Record<string, unknown>;
  isSubmitting: boolean;
  onSubmit: (params: Record<string, unknown>) => void;
  /**
   * Connections available to fill an unbound `connection` parameter
   * dropdown. Only consulted when selectedConnection is empty (i.e.
   * the tool is platform-level and the operator must pick a target
   * at call time, e.g. api_list_endpoints). Already filtered by the
   * caller to the tool's kind so the dropdown lists only valid
   * targets.
   */
  availableConnections?: EffectiveConnection[];
  /**
   * Bumping this remounts the form, useful when replaying audit events with
   * different initial values for the same tool.
   */
  formVersion?: number;
}

export function ToolForm({
  schema,
  selectedConnection,
  initialValues,
  isSubmitting,
  onSubmit,
  availableConnections,
  formVersion = 0,
}: ToolFormProps) {
  const properties = schema.parameters.properties ?? {};
  const required = schema.parameters.required ?? [];
  // The listbox fields carry their value in a hidden input, which the browser
  // excludes from constraint validation — so a required one left empty would
  // stop the submit with nothing said. The form reports it itself instead.
  const [missing, setMissing] = useState<string[]>([]);

  function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const formData = new FormData(e.currentTarget);
    const params: Record<string, unknown> = {};
    for (const [key, propSchema] of Object.entries(properties)) {
      const val = formData.get(key);
      if (val === null || val === "") continue;
      if (propSchema.type === "integer") {
        params[key] = parseInt(String(val), 10);
      } else if (propSchema.type === "boolean") {
        params[key] = val === "on";
      } else {
        params[key] = String(val);
      }
    }
    const unfilled = required.filter((r) => params[r] === undefined || params[r] === "");
    setMissing(unfilled);
    if (unfilled.length > 0) return;
    onSubmit(params);
  }

  return (
    <form
      key={`${schema.name}-${selectedConnection}-${formVersion}`}
      onSubmit={handleSubmit}
      className="space-y-3 rounded-lg border bg-card p-4"
    >
      {Object.entries(properties).map(([key, prop]) => (
        <FormField
          key={key}
          name={key}
          prop={prop}
          required={required.includes(key)}
          invalid={missing.includes(key)}
          schema={schema}
          selectedConnection={selectedConnection}
          availableConnections={availableConnections}
          initialValue={initialValues?.[key]}
        />
      ))}
      {missing.length > 0 && (
        <Alert variant="destructive">
          <AlertDescription>
            Fill in {missing.join(", ")} before executing.
          </AlertDescription>
        </Alert>
      )}
      <Button type="submit" disabled={isSubmitting}>
        {isSubmitting ? "Executing…" : "Execute"}
      </Button>
    </form>
  );
}

// FormField renders one parameter: its name, the schema's own description, and
// the control the parameter's type calls for. `connection` is the exception —
// it is a routing choice rather than a payload field, so it has its own field.
function FormField({
  name,
  prop,
  required,
  invalid,
  schema,
  selectedConnection,
  availableConnections,
  initialValue,
}: {
  name: string;
  prop: ToolParameterSchema;
  required: boolean;
  // Set once a submit was refused because this required field was empty.
  invalid: boolean;
  schema: ToolSchema;
  selectedConnection: string;
  availableConnections?: EffectiveConnection[];
  initialValue?: unknown;
}) {
  // A bound connection is the toolkit's choice, not the operator's, so the
  // field is locked and its asterisk would be a demand nobody can meet.
  const bound = name === "connection" && !!selectedConnection;
  return (
    <div>
      <Label className="mb-1 text-xs">
        {name}
        {required && !bound && <span className="ml-0.5 text-destructive">*</span>}
      </Label>
      <p className="mb-1 text-[11px] text-muted-foreground">{prop.description}</p>
      {name === "connection" ? (
        <ConnectionField
          required={required}
          invalid={invalid}
          kind={schema.kind}
          selectedConnection={selectedConnection}
          availableConnections={availableConnections ?? []}
          initialValue={initialValue}
        />
      ) : (
        <FieldInput
          name={name}
          prop={prop}
          required={required}
          invalid={invalid}
          initialValue={initialValue}
        />
      )}
    </div>
  );
}

// ConnectionField picks which connection a call is routed to. Two cases: the
// tool is bound to one already (the toolkit registered it under that
// connection's name), in which case the field is locked and only shows what is
// targeted; or the tool is platform-level (e.g. api_list_endpoints) and takes
// the connection at call time, so the operator picks from the connections the
// caller filtered to the tool's kind.
function ConnectionField({
  required,
  invalid,
  kind,
  selectedConnection,
  availableConnections,
  initialValue,
}: {
  required: boolean;
  invalid: boolean;
  kind: string;
  selectedConnection: string;
  availableConnections: EffectiveConnection[];
  // The connection a replayed audit event ran against. TryItTab keeps it in the
  // history entry's parameters for exactly this, so a replay re-runs against
  // the same target rather than reopening on an empty picker.
  initialValue?: unknown;
}) {
  const bound = !!selectedConnection;
  const none = !bound && availableConnections.length === 0;
  const names = availableConnections.map((c) => c.name);
  const replayed = typeof initialValue === "string" ? initialValue : "";
  return (
    <>
      <ChoiceField
        name="connection"
        required={required && !bound}
        invalid={invalid}
        disabled={bound || none}
        value={bound ? selectedConnection : undefined}
        initial={names.includes(replayed) ? replayed : ""}
        options={bound ? [selectedConnection] : names}
      />
      {none && (
        <Alert variant="warning" className="mt-1 px-2 py-1.5">
          <AlertDescription className="text-[11px]">
            No {kind} connections registered. Add one in Settings to invoke this
            tool.
          </AlertDescription>
        </Alert>
      )}
    </>
  );
}

// A Radix listbox item cannot carry an empty value, but "" is how an optional
// parameter says "do not send me". The unset choice travels under this sentinel
// and is translated back at this boundary, so nothing outside sees it.
const UNSET = "__unset__";

// ChoiceField is the one-of control. A Radix listbox is not a form control, so
// the chosen value is carried into the form's FormData by a hidden input; the
// form's own required-check (which refuses to submit while a required field is
// empty, and says which) is what enforces `required`.
function ChoiceField({
  name,
  required,
  invalid,
  disabled,
  value,
  initial,
  options,
}: {
  name: string;
  required: boolean;
  invalid?: boolean;
  disabled?: boolean;
  // Set for a locked field, whose value the caller owns.
  value?: string;
  // The selection the field opens on: a schema default, or the value a replayed
  // audit event carried. Empty leaves the placeholder showing.
  initial?: string;
  options: string[];
}) {
  const [picked, setPicked] = useState(initial ?? "");
  const current = value ?? picked;
  return (
    <>
      <input type="hidden" name={name} value={current} />
      <Select
        value={current === "" ? undefined : current}
        onValueChange={(v) => setPicked(v === UNSET ? "" : v)}
        disabled={disabled}
      >
        <SelectTrigger
          aria-label={name}
          aria-required={required || undefined}
          aria-invalid={invalid || undefined}
          className="w-full"
        >
          <SelectValue placeholder="-- select --" />
        </SelectTrigger>
        <SelectContent>
          {/* An optional parameter has to be clearable: once a value is picked
              the placeholder is unreachable, so the unset choice is an item. */}
          {!required && <SelectItem value={UNSET}>-- select --</SelectItem>}
          {options.map((o) => (
            <SelectItem key={o} value={o}>
              {o}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </>
  );
}

// The control a parameter gets, decided once from its schema so the renderer
// below is a plain dispatch rather than a chain of overlapping conditions.
type FieldKind = "sql" | "enum" | "urn" | "integer" | "boolean" | "text";

function fieldKind(name: string, prop: ToolParameterSchema): FieldKind {
  if (prop.type === "integer") return "integer";
  if (prop.type === "boolean") return "boolean";
  if (prop.type !== "string") return "text";
  if (prop.format === "sql" || name === "sql") return "sql";
  if (prop.enum) return "enum";
  if (prop.format === "urn") return "urn";
  return "text";
}

function FieldInput({
  name,
  prop,
  required,
  invalid,
  initialValue,
}: {
  name: string;
  prop: ToolParameterSchema;
  required: boolean;
  invalid: boolean;
  initialValue?: unknown;
}) {
  const resolvedDefault = initialValue !== undefined ? initialValue : prop.default;
  const text = String(resolvedDefault ?? "");

  switch (fieldKind(name, prop)) {
    case "sql":
      return <SqlTextarea name={name} required={required} initialValue={text} />;
    case "enum":
      return (
        <ChoiceField
          name={name}
          required={required}
          invalid={invalid}
          initial={text}
          options={prop.enum ?? []}
        />
      );
    case "urn":
      return (
        <Input
          type="text"
          name={name}
          required={required}
          defaultValue={text}
          className="font-mono"
          placeholder="urn:li:dataset:..."
        />
      );
    case "integer":
      return (
        <Input
          type="number"
          name={name}
          required={required}
          defaultValue={resolvedDefault === undefined ? undefined : Number(resolvedDefault)}
          className="w-32"
        />
      );
    case "boolean":
      return (
        <input
          type="checkbox"
          name={name}
          defaultChecked={booleanDefault(prop, initialValue)}
          className="h-4 w-4 rounded border"
        />
      );
    default:
      return <Input type="text" name={name} required={required} defaultValue={text} />;
  }
}

function booleanDefault(prop: ToolParameterSchema, initialValue: unknown): boolean {
  return initialValue === undefined ? prop.default === true : Boolean(initialValue);
}

function SqlTextarea({
  name,
  required,
  initialValue,
}: {
  name: string;
  required: boolean;
  initialValue: string;
}) {
  const [val, setVal] = useState(initialValue);
  return (
    <Textarea
      name={name}
      required={required}
      rows={6}
      value={val}
      onChange={(e) => setVal(e.target.value)}
      className="field-sizing-fixed font-mono"
      placeholder="SELECT ..."
    />
  );
}
