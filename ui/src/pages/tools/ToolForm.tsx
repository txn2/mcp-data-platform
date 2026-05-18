import { useState } from "react";
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
    for (const r of required) {
      if (params[r] === undefined || params[r] === "") return;
    }
    onSubmit(params);
  }

  return (
    <form
      key={`${schema.name}-${selectedConnection}-${formVersion}`}
      onSubmit={handleSubmit}
      className="space-y-3 rounded-lg border bg-card p-4"
    >
      {Object.entries(properties).map(([key, prop]) => {
        const isRequired = required.includes(key);
        if (key === "connection") {
          // Two cases: (1) the tool is bound to a connection already
          // (toolkit registered it under that connection's name), in
          // which case selectedConnection is non-empty and we show
          // a locked select so the operator can see what's targeted.
          // (2) the tool is platform-level (e.g. api_list_endpoints)
          // and takes connection as a parameter at call time, in
          // which case we render an enabled picker over the
          // connections the caller filtered by kind.
          if (selectedConnection) {
            return (
              <div key={key}>
                <label className="mb-1 block text-xs font-medium">{key}</label>
                <p className="mb-1 text-[11px] text-muted-foreground">
                  {prop.description}
                </p>
                <select
                  disabled
                  value={selectedConnection}
                  className="rounded-md border bg-muted px-3 py-1.5 text-sm text-muted-foreground outline-none"
                >
                  <option value={selectedConnection}>{selectedConnection}</option>
                </select>
              </div>
            );
          }
          return (
            <div key={key}>
              <label className="mb-1 block text-xs font-medium">
                {key}
                {isRequired && <span className="ml-0.5 text-red-500">*</span>}
              </label>
              <p className="mb-1 text-[11px] text-muted-foreground">
                {prop.description}
              </p>
              <select
                name="connection"
                required={isRequired}
                defaultValue=""
                disabled={!availableConnections || availableConnections.length === 0}
                className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2 disabled:opacity-50"
              >
                <option value="">-- select --</option>
                {(availableConnections ?? []).map((c) => (
                  <option key={`${c.kind}/${c.name}`} value={c.name}>
                    {c.name}
                  </option>
                ))}
              </select>
              {(!availableConnections || availableConnections.length === 0) && (
                <p className="mt-1 text-[11px] text-amber-600 dark:text-amber-400">
                  No {schema.kind} connections registered. Add one in Settings to
                  invoke this tool.
                </p>
              )}
            </div>
          );
        }
        return (
          <div key={key}>
            <label className="mb-1 block text-xs font-medium">
              {key}
              {isRequired && <span className="ml-0.5 text-red-500">*</span>}
            </label>
            <p className="mb-1 text-[11px] text-muted-foreground">
              {prop.description}
            </p>
            <FieldInput
              name={key}
              prop={prop}
              required={isRequired}
              initialValue={initialValues?.[key]}
            />
          </div>
        );
      })}
      <button
        type="submit"
        disabled={isSubmitting}
        className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
      >
        {isSubmitting ? "Executing…" : "Execute"}
      </button>
    </form>
  );
}

function FieldInput({
  name,
  prop,
  required,
  initialValue,
}: {
  name: string;
  prop: ToolParameterSchema;
  required: boolean;
  initialValue?: unknown;
}) {
  const resolvedDefault = initialValue !== undefined ? initialValue : prop.default;

  if (prop.type === "string" && (prop.format === "sql" || name === "sql")) {
    return (
      <SqlTextarea
        name={name}
        required={required}
        initialValue={String(resolvedDefault ?? "")}
      />
    );
  }
  if (prop.type === "string" && prop.enum) {
    return (
      <select
        name={name}
        required={required}
        defaultValue={String(resolvedDefault ?? "")}
        className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      >
        <option value="">-- select --</option>
        {prop.enum.map((v) => (
          <option key={v} value={v}>
            {v}
          </option>
        ))}
      </select>
    );
  }
  if (prop.type === "string" && prop.format === "urn") {
    return (
      <input
        type="text"
        name={name}
        required={required}
        defaultValue={String(resolvedDefault ?? "")}
        className="w-full rounded-md border bg-background px-3 py-1.5 font-mono text-sm outline-none ring-ring focus:ring-2"
        placeholder="urn:li:dataset:..."
      />
    );
  }
  if (prop.type === "integer") {
    return (
      <input
        type="number"
        name={name}
        required={required}
        defaultValue={
          resolvedDefault !== undefined ? Number(resolvedDefault) : undefined
        }
        className="w-32 rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      />
    );
  }
  if (prop.type === "boolean") {
    const checked =
      initialValue !== undefined ? Boolean(initialValue) : prop.default === true;
    return (
      <input
        type="checkbox"
        name={name}
        defaultChecked={checked}
        className="h-4 w-4 rounded border"
      />
    );
  }
  return (
    <input
      type="text"
      name={name}
      required={required}
      defaultValue={String(resolvedDefault ?? "")}
      className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
    />
  );
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
    <textarea
      name={name}
      required={required}
      rows={6}
      value={val}
      onChange={(e) => setVal(e.target.value)}
      className="w-full rounded-md border bg-background px-3 py-2 font-mono text-sm outline-none ring-ring focus:ring-2"
      placeholder="SELECT ..."
    />
  );
}
