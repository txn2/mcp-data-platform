import { describe, expect, it } from "vitest";
import type { FileMetaData, SchemaElement } from "hyparquet";
import { codecsOf, parquetColumns } from "./schema";

const leaf = (name: string, el: Partial<SchemaElement>): SchemaElement => ({
  name,
  repetition_type: "OPTIONAL",
  ...el,
});

function columns(...children: SchemaElement[][]) {
  const flat = children.flat();
  const top = children.length;
  return parquetColumns({ schema: [{ name: "schema", num_children: top }, ...flat] });
}

// The mapping manage_table registers a Parquet file with (internal/tableparquet),
// which the schema panel shows as "Registers as". The two have to agree.
describe("parquetColumns", () => {
  it("maps every supported leaf the way a registration declares it", () => {
    const cols = columns(
      [leaf("b", { type: "BOOLEAN" })],
      [leaf("i", { type: "INT32" })],
      [leaf("i8", { type: "INT32", logical_type: { type: "INTEGER", bitWidth: 8, isSigned: true } })],
      [leaf("u16", { type: "INT32", logical_type: { type: "INTEGER", bitWidth: 16, isSigned: false } })],
      [leaf("c16", { type: "INT32", converted_type: "INT_16" })],
      [leaf("big", { type: "INT64" })],
      [leaf("f", { type: "FLOAT" })],
      [leaf("d", { type: "DOUBLE" })],
      [leaf("dec", { type: "FIXED_LEN_BYTE_ARRAY", type_length: 16, logical_type: { type: "DECIMAL", precision: 38, scale: 10 } })],
      [leaf("olddec", { type: "INT32", converted_type: "DECIMAL", precision: 9, scale: 2 })],
      [leaf("s", { type: "BYTE_ARRAY", logical_type: { type: "STRING" } })],
      [leaf("u8s", { type: "BYTE_ARRAY", converted_type: "UTF8" })],
      [leaf("bin", { type: "BYTE_ARRAY" })],
      [leaf("dt", { type: "INT32", logical_type: { type: "DATE" } })],
      [leaf("ts", { type: "INT64", logical_type: { type: "TIMESTAMP", isAdjustedToUTC: false, unit: "MICROS" } })],
      [leaf("i96", { type: "INT96" })],
      [leaf("req", { type: "INT64", repetition_type: "REQUIRED" })],
    );
    expect(cols.map((c) => `${c.name} ${c.trinoType}`)).toEqual([
      "b BOOLEAN", "i INTEGER", "i8 TINYINT", "u16 INTEGER", "c16 SMALLINT", "big BIGINT",
      "f REAL", "d DOUBLE", "dec DECIMAL(38,10)", "olddec DECIMAL(9,2)", "s VARCHAR", "u8s VARCHAR", "bin VARBINARY",
      "dt DATE", "ts TIMESTAMP(6)", "i96 TIMESTAMP(6)", "req BIGINT",
    ]);
    expect(cols.find((c) => c.name === "req")?.nullable).toBe(false);
    expect(cols.find((c) => c.name === "ts")?.parquetType).toBe("INT64 TIMESTAMP(MICROS)");
    expect(cols.find((c) => c.name === "dec")?.parquetType).toBe("FIXED_LEN_BYTE_ARRAY(16) DECIMAL(38,10)");
  });

  it("maps a LIST, a MAP and a group", () => {
    const cols = columns(
      [
        leaf("tags", { num_children: 1, logical_type: { type: "LIST" } }),
        leaf("list", { num_children: 1, repetition_type: "REPEATED" }),
        leaf("element", { type: "BYTE_ARRAY", logical_type: { type: "STRING" } }),
      ],
      [
        leaf("m", { num_children: 1, converted_type: "MAP" }),
        leaf("key_value", { num_children: 2, repetition_type: "REPEATED" }),
        leaf("key", { type: "BYTE_ARRAY", logical_type: { type: "STRING" }, repetition_type: "REQUIRED" }),
        leaf("value", { type: "DOUBLE" }),
      ],
      [leaf("r", { num_children: 2 }), leaf("A", { type: "INT32" }), leaf("n", { type: "BOOLEAN" })],
      [leaf("legacy", { num_children: 1, converted_type: "LIST" }), leaf("element", { type: "INT32", repetition_type: "REPEATED" })],
    );
    expect(cols.map((c) => c.trinoType)).toEqual([
      "ARRAY(VARCHAR)", "MAP(VARCHAR, DOUBLE)", 'ROW("a" INTEGER, "n" BOOLEAN)', "ARRAY(INTEGER)",
    ]);
    expect(cols[0]?.parquetType).toBe("group (LIST)");
  });

  it("marks what a registration refuses, and why", () => {
    const cols = columns(
      [leaf("t", { type: "INT64", logical_type: { type: "TIME", isAdjustedToUTC: false, unit: "MICROS" } })],
      [leaf("u", { type: "FIXED_LEN_BYTE_ARRAY", type_length: 16, logical_type: { type: "UUID" } })],
      [leaf("rep", { type: "INT32", repetition_type: "REPEATED" })],
      [leaf("bad", { num_children: 1 }), leaf("a-b", { type: "INT32" })],
      [leaf("wide", { type: "BYTE_ARRAY", logical_type: { type: "DECIMAL", precision: 40, scale: 0 } })],
      [leaf("u64", { type: "INT64", logical_type: { type: "INTEGER", bitWidth: 64, isSigned: false } })],
      [leaf("u32", { type: "INT32", logical_type: { type: "INTEGER", bitWidth: 32, isSigned: false } })],
    );
    for (const c of cols) {
      expect(c.trinoType, c.name).toBeNull();
      expect(c.refused, c.name).toBeTruthy();
    }
    expect(cols[0]?.refused).toContain("INT64 TIME(MICROS)");
    expect(cols[3]?.refused).toContain('"a-b"');
  });

  // A registration refuses a column by its NAME as readily as by its type
  // (columnsOf in internal/tableparquet/schema.go, internal/tabletype/names.go).
  // A panel that showed "Registers as BIGINT" for one of these would describe a
  // table nobody can create.
  it("marks a column a registration refuses by name", () => {
    const cols = columns(
      [leaf("A\u00f1o", { type: "BYTE_ARRAY", logical_type: { type: "STRING" } })],
      [leaf("net, total", { type: "INT64" })],
      [leaf(" id", { type: "INT64" })],
      [leaf("", { type: "INT64" })],
      [leaf("ID", { type: "INT64" })],
      [leaf("id", { type: "INT64" })],
      [leaf("ok", { type: "INT64" })],
    );
    expect(cols.map((c) => c.trinoType)).toEqual([null, null, null, null, "BIGINT", null, "BIGINT"]);
    expect(cols[0]?.refused).toContain("outside ASCII");
    expect(cols[1]?.refused).toContain("comma");
    expect(cols[2]?.refused).toContain("whitespace");
    expect(cols[3]?.refused).toContain("needs a name");
    expect(cols[5]?.refused).toContain('"ID" and "id"');
  });

  it("refuses a nested field name edged with whitespace, as CheckFieldName does", () => {
    const cols = columns([leaf("r", { num_children: 1 }), leaf(" a", { type: "INT64" })]);
    expect(cols[0]?.trinoType).toBeNull();
    expect(cols[0]?.refused).toContain("whitespace");
  });
});

describe("codecsOf", () => {
  it("names each codec once", () => {
    const meta = {
      row_groups: [
        { columns: [{ meta_data: { codec: "ZSTD" } }, { meta_data: { codec: "SNAPPY" } }] },
        { columns: [{ meta_data: { codec: "ZSTD" } }, {}] },
      ],
    } as unknown as FileMetaData;
    expect(codecsOf(meta)).toEqual(["SNAPPY", "ZSTD"]);
  });
});
