import type { FileMetaData, LogicalType, SchemaElement } from "hyparquet";

/**
 * A Parquet file's columns as the portal shows them, and the Trino type each
 * would be registered as (#1833).
 *
 * The mapping is the one manage_table registers a Parquet file with, in
 * internal/tableparquet (schema.go and leaf.go): the schema panel and a
 * registration have to agree, or the viewer would describe a table nobody can
 * create. A column the registration refuses is shown with the reason in place
 * of a type.
 */

export interface ParquetColumn {
  name: string;
  /** The physical type, then its annotation, as the file declares them. */
  parquetType: string;
  nullable: boolean;
  /** The Trino type a registration declares, or null when it refuses the column. */
  trinoType: string | null;
  /** Why a registration refuses the column; null when it declares it. */
  refused: string | null;
}

interface Node {
  el: SchemaElement;
  children: Node[];
}

/** Rebuilds the schema tree the footer lists depth-first. */
export function schemaTree(schema: SchemaElement[]): Node {
  let i = 0;
  const walk = (): Node => {
    const el = schema[i++] as SchemaElement;
    const children: Node[] = [];
    for (let c = 0; c < (el.num_children ?? 0) && i < schema.length; c++) children.push(walk());
    return { el, children };
  };
  return walk();
}

/** The top-level columns of a file, each described and mapped. */
export function parquetColumns(metadata: Pick<FileMetaData, "schema">): ParquetColumn[] {
  const root = schemaTree(metadata.schema);
  // A column's name is held to the same rules as its type, so a file the
  // registration refuses by name is not shown with a type it would register
  // as: columnsOf in internal/tableparquet/schema.go checks the name and the
  // case-insensitive collision before it maps the type.
  const byLower = new Map<string, string>();
  for (const node of root.children) {
    const lower = node.el.name.toLowerCase();
    if (!byLower.has(lower)) byLower.set(lower, node.el.name);
  }
  return root.children.map((node) => {
    const mapped = safeColumnType(node, byLower);
    return {
      name: node.el.name,
      parquetType: describe(node),
      nullable: node.el.repetition_type !== "REQUIRED",
      trinoType: "type" in mapped ? mapped.type : null,
      refused: "refused" in mapped ? mapped.refused : null,
    };
  });
}

/** A top-level column's type, or why a registration refuses the column. */
function safeColumnType(node: Node, byLower: Map<string, string>): Mapped {
  const name = node.el.name;
  const nameErr = checkName(name);
  if (nameErr) return { refused: nameErr };
  const prior = byLower.get(name.toLowerCase());
  if (prior !== undefined && prior !== name) {
    return {
      refused:
        `the columns "${prior}" and "${name}" are one column to the reader, which matches a Parquet ` +
        "column by name without regard to case; rename one of them",
    };
  }
  return safeFieldType(node);
}

type Mapped = { type: string } | { refused: string };

function safeFieldType(node: Node): Mapped {
  try {
    return { type: fieldType(node) };
  } catch (err) {
    return { refused: err instanceof Error ? err.message : String(err) };
  }
}

/** What a field is in the file: a group's annotation, or a leaf's physical and logical type. */
export function describe(node: Node): string {
  const el = node.el;
  if (node.children.length > 0 || !el.type) {
    const annotation = el.logical_type?.type ?? el.converted_type;
    return annotation ? `group (${annotation})` : "group";
  }
  let s = el.type === "FIXED_LEN_BYTE_ARRAY" && el.type_length ? `${el.type}(${el.type_length})` : el.type;
  const annotation = logicalText(el.logical_type) ?? el.converted_type;
  if (annotation) s += ` ${annotation}`;
  return s;
}

function logicalText(lt: LogicalType | undefined): string | undefined {
  if (!lt) return undefined;
  switch (lt.type) {
    case "DECIMAL":
      return `DECIMAL(${lt.precision},${lt.scale})`;
    case "INTEGER":
      return `INT(${lt.bitWidth},${lt.isSigned})`;
    case "TIMESTAMP":
    case "TIME":
      return `${lt.type}(${lt.unit}${lt.isAdjustedToUTC ? ", UTC" : ""})`;
    default:
      return lt.type;
  }
}

function fail(what: string): never {
  throw new Error(`${what}, which no registered column type reads back exactly`);
}

const FIELD_NAME_RUNE = /^[A-Za-z0-9_.$ ]+$/;

/**
 * Why a Hive column or ROW field cannot carry this name, or null.
 *
 * The counterpart of CheckName in internal/tabletype/names.go, whose refusals
 * this repeats: a registration answers with them, and the panel has to name
 * the same column.
 */
function checkName(name: string): string | null {
  if (name === "") return "a key is empty, and a column needs a name";
  if (name.trim() !== name) {
    return `the key "${name}" begins or ends with whitespace, which a Hive column name may not`;
  }
  if (name.includes(",")) return `the key "${name}" holds a comma, which a Hive column name may not`;
  // eslint-disable-next-line no-control-regex
  if (/[^\x00-\x7F]/.test(name)) {
    return (
      `the key "${name}" holds a character outside ASCII, and the reader leaves such a column ` +
      "empty on every row; rename it using ASCII"
    );
  }
  return null;
}

/** Why a ROW field cannot carry this name, or null (CheckFieldName's rule). */
function checkFieldName(name: string): string | null {
  const err = checkName(name);
  if (err) return err;
  if (!FIELD_NAME_RUNE.test(name)) {
    return (
      `the nested key "${name}" holds a character a field inside a row may not, which may hold only ` +
      "letters, digits, '_', '.', '$' and spaces; rename it"
    );
  }
  return null;
}

function fieldType(node: Node): string {
  const el = node.el;
  if (el.repetition_type === "REPEATED") fail("a repeated field outside a LIST or MAP group");
  if (node.children.length === 0 && el.type) return leafType(el);
  const annotation = el.logical_type?.type ?? el.converted_type;
  if (annotation === "LIST") return listType(node);
  if (annotation === "MAP" || annotation === "MAP_KEY_VALUE") return mapType(node);
  if (annotation) fail(`a group annotated ${annotation}`);
  return rowType(node);
}

function rowType(node: Node): string {
  if (node.children.length === 0) fail("a group with no fields");
  const fields = node.children.map((child) => {
    const err = checkFieldName(child.el.name);
    if (err) throw new Error(err);
    return `"${child.el.name.toLowerCase()}" ${fieldType(child)}`;
  });
  return `ROW(${fields.join(", ")})`;
}

function listType(node: Node): string {
  const rep = node.children[0];
  if (node.children.length !== 1 || !rep || rep.el.repetition_type !== "REPEATED") {
    fail("a LIST group not shaped as one repeated child");
  }
  if (rep.children.length === 0) return `ARRAY(${leafType(rep.el)})`;
  if (rep.children.length > 1 || rep.el.name === "array" || rep.el.name === `${node.el.name}_tuple`) {
    return `ARRAY(${rowType(rep)})`;
  }
  return `ARRAY(${fieldType(rep.children[0] as Node)})`;
}

function mapType(node: Node): string {
  const kv = node.children[0];
  if (node.children.length !== 1 || !kv || kv.children.length !== 2) {
    fail("a MAP group not shaped as one repeated key/value pair");
  }
  const [key, value] = kv.children as [Node, Node];
  if (key.children.length > 0) fail("a MAP whose key is a group");
  return `MAP(${leafType(key.el)}, ${fieldType(value)})`;
}

const SIGNED: Record<number, string> = { 8: "TINYINT", 16: "SMALLINT", 32: "INTEGER", 64: "BIGINT" };
// No unsigned 32- or 64-bit entry: Trino's Parquet reader reads a UINT32 as
// signed and refuses every declaration that would hold a UINT64
// (internal/tableparquet/leaf.go).
const UNSIGNED: Record<number, string> = { 8: "SMALLINT", 16: "INTEGER" };
const CONVERTED_INT: Record<string, string> = {
  INT_8: "TINYINT", INT_16: "SMALLINT", INT_32: "INTEGER", INT_64: "BIGINT",
  UINT_8: "SMALLINT", UINT_16: "INTEGER",
};

/** The type a primitive with no annotation is declared as. */
const PLAIN: Record<string, string> = {
  BOOLEAN: "BOOLEAN", INT32: "INTEGER", INT64: "BIGINT", INT96: "TIMESTAMP(6)",
  FLOAT: "REAL", DOUBLE: "DOUBLE", BYTE_ARRAY: "VARBINARY",
};

/** The type an annotated primitive is declared as, by physical type then annotation. */
const ANNOTATED: Record<string, Record<string, string>> = {
  INT32: { DATE: "DATE" },
  INT64: { TIMESTAMP: "TIMESTAMP(6)", TIMESTAMP_MILLIS: "TIMESTAMP(6)", TIMESTAMP_MICROS: "TIMESTAMP(6)" },
  BYTE_ARRAY: { STRING: "VARCHAR", UTF8: "VARCHAR", ENUM: "VARCHAR", JSON: "VARCHAR" },
};

/** The Trino type of one primitive field, or a refusal naming its Parquet type. */
export function leafType(el: SchemaElement): string {
  const lt = el.logical_type;
  const annotation = lt ? logicalText(lt) : el.converted_type;
  const what = `a Parquet ${el.type}${annotation ? ` ${annotation}` : ""}`;
  const mapped = decimalType(el) ?? scalarType(el);
  if (mapped === undefined || mapped === null) fail(what);
  return mapped;
}

/** A DECIMAL, by the logical or the converted annotation; null for one Trino cannot hold. */
function decimalType(el: SchemaElement): string | null | undefined {
  const numbers = decimalNumbers(el);
  if (!numbers) return undefined;
  const [p, s] = numbers;
  const valid = p >= 1 && p <= 38 && s >= 0 && s <= p;
  return valid ? `DECIMAL(${p},${s})` : null;
}

/** A DECIMAL's precision and scale, or undefined for a field that is not one. */
function decimalNumbers(el: SchemaElement): [number, number] | undefined {
  const lt = el.logical_type;
  if (lt?.type === "DECIMAL") return [lt.precision, lt.scale];
  if (el.converted_type === "DECIMAL") return [el.precision ?? 0, el.scale ?? 0];
  return undefined;
}

function scalarType(el: SchemaElement): string | undefined {
  const lt = el.logical_type;
  if (lt?.type === "INTEGER") return (lt.isSigned ? SIGNED : UNSIGNED)[lt.bitWidth];
  const physical = el.type ?? "";
  const annotation = lt ? lt.type : el.converted_type;
  if (!annotation) return PLAIN[physical];
  return CONVERTED_INT[annotation] ?? ANNOTATED[physical]?.[annotation];
}

/** The compression codecs a file's column chunks use, each named once. */
export function codecsOf(metadata: FileMetaData): string[] {
  const seen = new Set<string>();
  for (const group of metadata.row_groups) {
    for (const chunk of group.columns) {
      if (chunk.meta_data?.codec) seen.add(chunk.meta_data.codec);
    }
  }
  return [...seen].sort();
}
