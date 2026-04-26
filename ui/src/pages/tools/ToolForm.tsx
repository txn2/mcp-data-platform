import { useState } from "react";
import type { ToolSchema, ToolParameterSchema } from "@/api/admin/types";

interface ToolFormProps {
  schema: ToolSchema;
  selectedConnection: string;
  initialValues?: Record<string, unknown>;
  isSubmitting: boolean;
  onSubmit: (params: Record<string, unknown>) => void;
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
