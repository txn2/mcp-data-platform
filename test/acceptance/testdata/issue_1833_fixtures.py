"""Writes the Parquet fixtures the #1833 acceptance criteria read.

Run with pyarrow 25 from the repository root:

    python test/acceptance/testdata/issue_1833_fixtures.py

The files are committed beside this script, so the suite does not need pyarrow;
this is how they were made and how to make them again.
"""

import datetime
import decimal
import os

import pyarrow as pa
import pyarrow.parquet as pq

HERE = os.path.dirname(os.path.abspath(__file__))

# One column of every type the registration mapping declares, with a null in
# every column of the last row. Two rows per row group, so the viewer has a
# later group to page to.
ALL_TYPES = pa.table(
    {
        "b": pa.array([True, False, True, None], pa.bool_()),
        "i8": pa.array([-8, 8, 127, None], pa.int8()),
        "i16": pa.array([-16, 16, 32767, None], pa.int16()),
        "i32": pa.array([-32, 32, 2147483647, None], pa.int32()),
        "i64": pa.array([1, 2, 9007199254740993, None], pa.int64()),
        "u8": pa.array([0, 8, 255, None], pa.uint8()),
        "u16": pa.array([0, 16, 65535, None], pa.uint16()),
        "f32": pa.array([1.5, -2.25, 3.0, None], pa.float32()),
        "f64": pa.array([3.141592653589793, -0.5, 1e300, None], pa.float64()),
        "amount": pa.array(
            [decimal.Decimal("12345.67"), decimal.Decimal("-0.01"), decimal.Decimal("9999999999.99"), None],
            pa.decimal128(12, 2),
        ),
        "wide": pa.array(
            [decimal.Decimal("12345678901234567890123456.123456789012"), decimal.Decimal("0"), decimal.Decimal("-1"), None],
            pa.decimal128(38, 12),
        ),
        "s": pa.array(["alpha", "line\nbreak", "", None], pa.string()),
        "bin": pa.array([b"\x00\x01\xff", b"", b"abc", None], pa.binary()),
        "d": pa.array([datetime.date(2024, 5, 1), datetime.date(1970, 1, 1), datetime.date(2099, 12, 31), None], pa.date32()),
        "ts_ms": pa.array(
            [datetime.datetime(2024, 5, 1, 12, 34, 56, 789000), None, datetime.datetime(1970, 1, 1), None],
            pa.timestamp("ms"),
        ),
        "ts_us": pa.array(
            [
                datetime.datetime(2024, 5, 1, 12, 34, 56, 789123),
                datetime.datetime(1999, 12, 31, 23, 59, 59, 999999),
                datetime.datetime(2024, 1, 1, 0, 0, 0, 1),
                None,
            ],
            pa.timestamp("us"),
        ),
        "tags": pa.array([["a", "b"], [], [None, "c"], None], pa.list_(pa.string())),
        "attrs": pa.array(
            [[("k", 1.5)], [], [("x", None), ("y", 2.0)], None], pa.map_(pa.string(), pa.float64())
        ),
        "st": pa.array(
            [{"a": 1, "n": "x"}, {"a": None, "n": "y"}, {"a": 3, "n": None}, None],
            pa.struct([("a", pa.int64()), ("n", pa.string())]),
        ),
    }
)

# ZSTD, which is what the platform's own Parquet writer uses (tableparquet.Write),
# so the portal viewer's decompressor is exercised on the file the suite opens.
pq.write_table(
    ALL_TYPES, os.path.join(HERE, "issue_1833_all_types.parquet"), row_group_size=2, compression="zstd"
)

# The same mapping over the other physical layouts a writer chooses: DECIMAL
# stored as INT32 and INT64 rather than FIXED_LEN_BYTE_ARRAY, a timestamp as the
# deprecated INT96, a nanosecond timestamp, and JSON-annotated text.
pq.write_table(
    pa.table(
        {
            "dec9": pa.array([decimal.Decimal("1234567.89"), None], pa.decimal128(9, 2)),
            "dec18": pa.array([decimal.Decimal("-1234567890123456.78"), None], pa.decimal128(18, 2)),
            "ts_ns": pa.array([datetime.datetime(2024, 5, 1, 12, 34, 56, 789123), None], pa.timestamp("ns")),
            "doc": pa.array(['{"a": [1, 2]}', None], pa.json_(pa.string())),
        }
    ),
    os.path.join(HERE, "issue_1833_physical.parquet"),
    store_decimal_as_integer=True,
)
pq.write_table(
    pa.table({"ts96": pa.array([datetime.datetime(2024, 5, 1, 12, 34, 56, 789123), None], pa.timestamp("us"))}),
    os.path.join(HERE, "issue_1833_int96.parquet"),
    use_deprecated_int96_timestamps=True,
)

# A TIME column, a UUID column and unsigned 32- and 64-bit columns, which no
# registration declares.
pq.write_table(
    pa.table({"id": pa.array([1], pa.int64()), "at": pa.array([datetime.time(12, 0)], pa.time64("us"))}),
    os.path.join(HERE, "issue_1833_time.parquet"),
)
pq.write_table(
    pa.table({"id": pa.array([1], pa.int64()), "u": pa.array([b"0123456789abcdef"], pa.uuid())}),
    os.path.join(HERE, "issue_1833_uuid.parquet"),
)
pq.write_table(
    pa.table({"id": pa.array([1], pa.int64()), "big": pa.array([4294967295], pa.uint32())}),
    os.path.join(HERE, "issue_1833_uint32.parquet"),
)

# Two columns one apart by case.
pq.write_table(
    pa.table({"Id": pa.array([1], pa.int64()), "id": pa.array(["one"], pa.string())}),
    os.path.join(HERE, "issue_1833_case.parquet"),
)
