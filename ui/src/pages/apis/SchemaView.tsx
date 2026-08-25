import { useState } from "react";
import { cn } from "@/lib/utils";

// A resolved OpenAPI schema, rendered as the shape a caller has to produce or
// will receive rather than as the JSON document that describes it. The
// properties of an object are the thing a person reads; the keywords around
// them are noise until they are not, which is what the raw view is for.
//
// The schema arrives already flattened and depth-capped by the platform's own
// resolver, so nothing here follows a $ref or guards against a cycle.

/** JSONSchema is the subset of the resolved schema this renders. */
interface JSONSchema {
  type?: string | string[];
  format?: string;
  description?: string;
  enum?: unknown[];
  properties?: Record<string, JSONSchema>;
  required?: string[];
  items?: JSONSchema;
  [key: string]: unknown;
}

/** MAX_DEPTH bounds the nesting rendered inline. Deeper shapes are readable in
 * the raw view, which every schema block offers. */
const MAX_DEPTH = 3;

/** typeLabel is the one-line reading of what a value is. */
export function typeLabel(schema: JSONSchema | undefined): string {
  if (!schema) return "any";
  const type = Array.isArray(schema.type) ? schema.type.join(" | ") : schema.type;
  if (type === "array") {
    return `array of ${typeLabel(schema.items)}`;
  }
  if (!type) {
    return schema.properties ? "object" : "any";
  }
  return schema.format ? `${type} (${schema.format})` : type;
}

/** asSchema narrows the `unknown` the wire types carry, so a non-object schema
 * (a bare `true`, an absent one) renders as nothing rather than throwing. */
export function asSchema(value: unknown): JSONSchema | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  return value as JSONSchema;
}

/** EnumValues lists an enumeration inline: what a caller may actually send. */
function EnumValues({ values }: { values: unknown[] }) {
  return (
    <span className="ml-1 font-mono text-[11px] text-muted-foreground">
      {values.map((v) => JSON.stringify(v)).join(" | ")}
    </span>
  );
}

/** PropertyRows renders an object's properties, one row each. */
function PropertyRows({ schema, depth }: { schema: JSONSchema; depth: number }) {
  const required = new Set(schema.required ?? []);
  const entries = Object.entries(schema.properties ?? {});
  return (
    <ul className="divide-y">
      {entries.map(([name, prop]) => (
        <li key={name} className="py-1.5">
          <div className="flex flex-wrap items-baseline gap-x-2">
            <code className="font-mono text-[12px] text-foreground">{name}</code>
            <span className="text-[11px] text-muted-foreground">{typeLabel(prop)}</span>
            {required.has(name) && (
              <span className="text-[10px] font-semibold uppercase tracking-wide text-destructive">
                required
              </span>
            )}
          </div>
          {prop.description && (
            <p className="mt-0.5 text-[11px] text-muted-foreground">{prop.description}</p>
          )}
          {prop.enum && <EnumValues values={prop.enum} />}
          {depth < MAX_DEPTH && <NestedShape schema={prop} depth={depth + 1} />}
        </li>
      ))}
    </ul>
  );
}

/** NestedShape indents one level of an object, or of an array's item shape. */
function NestedShape({ schema, depth }: { schema: JSONSchema; depth: number }) {
  const inner = schema.type === "array" ? schema.items : schema;
  if (!inner?.properties || Object.keys(inner.properties).length === 0) return null;
  return (
    <div className="ml-3 mt-1 border-l pl-3">
      <PropertyRows schema={inner} depth={depth} />
    </div>
  );
}

/**
 * objectShapeOf is the shape whose properties are worth listing: the schema
 * itself, or an array's item schema. Undefined when there is nothing to list,
 * so the caller renders the type line alone rather than an empty table.
 */
function objectShapeOf(schema: JSONSchema): JSONSchema | undefined {
  const shape = schema.type === "array" ? schema.items : schema;
  const properties = shape?.properties;
  if (!properties || Object.keys(properties).length === 0) return undefined;
  return shape;
}

/**
 * SchemaView renders one schema: its type line, its properties when it has
 * them, and the raw document behind a toggle for everything the summary leaves
 * out (constraints, oneOf branches, vendor extensions).
 */
export function SchemaView({ value, className }: { value: unknown; className?: string }) {
  const [showRaw, setShowRaw] = useState(false);
  const schema = asSchema(value);
  if (!schema) return null;
  const shape = objectShapeOf(schema);

  return (
    <div className={cn("space-y-1", className)}>
      <div className="flex flex-wrap items-baseline gap-2">
        <span className="text-[11px] text-muted-foreground">{typeLabel(schema)}</span>
        {schema.enum && <EnumValues values={schema.enum} />}
        <button
          type="button"
          onClick={() => setShowRaw((v) => !v)}
          className="ml-auto text-[11px] text-muted-foreground underline-offset-2 hover:underline"
        >
          {showRaw ? "Hide schema" : "Show schema"}
        </button>
      </div>
      {schema.description && (
        <p className="text-[11px] text-muted-foreground">{schema.description}</p>
      )}
      {shape && <PropertyRows schema={shape} depth={1} />}
      {showRaw && (
        <pre className="max-h-72 overflow-auto rounded-md bg-muted p-2 font-mono text-[11px] leading-relaxed">
          {JSON.stringify(schema, null, 2)}
        </pre>
      )}
    </div>
  );
}
