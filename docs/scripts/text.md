# Formatting, Patterns and Hashes in a Managed Script

A managed script's globals are `platform`, `json`, `xml`, `date`, `hash`,
`re`, `run` and `sum`. This page covers the text work a pipeline does on
every record: formatting a value with `%` and `str.format`, matching it with
`re`, and fingerprinting it with `hash`. The rest of the dialect is
`manage_script command=help`.

## Formatting with `%` and `.format`

Both take what Python takes. Starlark's own `%` accepts a bare conversion
only and its `str.format` accepts no format spec, so the platform runs a
script's `%` and `.format` through its own implementation of Python's rules.

`"..." % values` takes the conversions `s r d i o x X e E f F g G c` and
`%%`, the flags `-` (left-justify), `0` (zero-pad), `+`, space and `#`
(`0x`, `0o`, a kept decimal point), a width, a `.precision`, `*` in place of
either, and `%(key)s` with a dict on the right.

| Expression | Result |
| --- | --- |
| `"%02X" % 10` | `0A` |
| `"%%%02X" % 32` | `%20` |
| `"%02d-%02d" % (3, 7)` | `03-07` |
| `"%5.2f" % 3.14159` | ` 3.14` |
| `"%-4s\|" % "a"` | `a   \|` |
| `"%(n)03d" % {"n": 7}` | `007` |

`"...".format(...)` takes Python's format spec,
`[[fill]align][sign][#][0][width][,|_][.precision][type]`, with the types
`s d n b o x X c e E f F g G %`, and the conversions `!s` and `!r`.

| Expression | Result |
| --- | --- |
| `"{:02X}".format(10)` | `0A` |
| `"{:>10}".format("a")` | `         a` |
| `"{:.2f}".format(3.14159)` | `3.14` |
| `"{:,}".format(1234567)` | `1,234,567` |
| `"{:*^9}".format("ab")` | `***ab****` |
| `"{:.1%}".format(0.125)` | `12.5%` |

A field with no spec, `"{}"`, is `str()` of the value, as before. A value
written with `%r` or `!r` is Starlark's repr, so a string is quoted with
double quotes. Nested fields (`{:{width}}`) and attribute or element access
in a field (`{a.b}`, `{a[0]}`) are not supported.

A format string written as a literal, or as a module constant, is checked
when the script is saved: one `%` or `.format` would refuse is reported by
`validate` as `invalid-format-string`, with its line, instead of failing the
first run that reaches it.

## Matching with `re`

`re` follows Python's module: the same function names, arguments and
results. It runs on Go's `regexp`, which is RE2.

| Call | Returns |
| --- | --- |
| `re.search(p, s)` | the first match anywhere, or `None` |
| `re.match(p, s)` | a match at the start of `s`, or `None` |
| `re.fullmatch(p, s)` | a match of the whole of `s`, or `None` |
| `re.findall(p, s)` | every match: the text with no group, group 1's text with one, a tuple of the groups with more |
| `re.finditer(p, s)` | every match, as a list of match values |
| `re.sub(p, repl, s, count=0)` | `s` with matches replaced; `repl` uses `\1`, `\g<1>`, `\g<name>`, or is a function of the match |
| `re.split(p, s, maxsplit=0)` | the text between matches, with the captured groups between them |
| `re.compile(p, flags=0)` | a pattern with the same methods, and `.pattern`, `.flags`, `.groups` |
| `re.escape(s)` | `s` with every metacharacter quoted |

A match has `.group()` (the whole match), `.group(n)` or `.group("name")`
(`None` for a group that took no part), `.groups()`, `.groupdict()`,
`.start(n)`, `.end(n)`, `.span(n)` and `.string`. Offsets are byte offsets,
the same offsets Starlark's slicing uses, so `s[m.start(1):m.end(1)]` is
`m.group(1)`.

```python
def main():
    """Reads a page's title and its Open Graph type."""
    page = platform.call("api_invoke_endpoint", {"connection": "site", "method": "GET", "path": "/"})["body"]
    title = re.search(r"(?is)<title[^>]*>(.*?)</title>", page)
    kind = re.search(r'(?i)<meta\s+property="og:type"\s+content="([^"]*)"', page)
    print(title.group(1).strip() if title else "", kind.group(1) if kind else "")
```

Flags are inline, `(?i)`, `(?m)`, `(?s)`, or passed as `flags=re.I`
(`re.IGNORECASE`), `re.M` (`re.MULTILINE`) and `re.S` (`re.DOTALL`).

RE2 matches in time linear in the input and never backtracks, so a pattern
run over a page fetched from the internet cannot spend a run's deadline on
one match. It has no backreferences in a pattern (`(a)\1`) and no lookaround
(`(?=`, `(?!`, `(?<=`, `(?<!`). A pattern ported from Python or Java that
uses them has to be rewritten: a lookahead usually becomes a capture group
around the part to keep. A literal pattern RE2 cannot compile is reported by
`validate` as `invalid-pattern` when the script is saved.

A pattern is compiled once per run, however many times a loop uses it. A
match costs one interpreter step per KiB of input it scans, so a run's step
budget still bounds how much text it searches.

## Hashing with `hash`

| Call | Returns |
| --- | --- |
| `hash.md5(s)`, `hash.sha1(s)`, `hash.sha256(s)`, `hash.sha512(s)` | the lowercase hex digest of the string's (or bytes') bytes |
| `hash.hmac_sha1(key, s)`, `hash.hmac_sha256(key, s)`, `hash.hmac_sha512(key, s)` | the lowercase hex HMAC of `s` under `key` |
| `hash.crc32(s)`, `hash.fnv64(s)` | a non-negative int, for bucketing |

`hash.md5("")` is `d41d8cd98f00b204e9800998ecf8427e`; `hash.sha256("abc")`
is `ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad`.

The digest is computed in Go, so one call hashes a megabyte page in a single
step. Before the module, the only way to hash was SQL through
`platform.query`, which fails for a value near 1 MB because the bound value
is written into a statement Trino caps at 1,000,000 characters.

`hash(x)` called directly is still Starlark's own hash of a value.

Every function on this page is deterministic and has no effect, so a run
that uses them replays from its recording with nothing recorded for them.
