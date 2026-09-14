# Reading XML in a Managed Script

A managed script's globals are `platform`, `json`, `xml`, `date`, `run` and
`sum`. This page covers `xml`: what it parses, the path language it searches
with, and the bounds it refuses past. The rest of the dialect is
`manage_script command=help`, and the security model is
[Managed Scripts: Security Model](security.md).

## Why it exists

XML is what comes back from SOAP web services, WebDAV `PROPFIND` (whose only
response format is a multistatus document), RSS and Atom feeds, sitemaps, and
most ERP integration surfaces older than about 2015. The gateway can already
send such a request — a string body goes out verbatim under a declared or
caller-set `text/xml` content type — so the platform can talk to these
upstreams. Without `xml` a script could only hold the answer as a string.

## The tree

`xml.decode(s)` returns the document's root element. An element has five
fields:

| Field | What it holds |
| --- | --- |
| `.tag` | the local name, with any namespace prefix resolved away |
| `.ns` | the namespace URI, `""` when the element is in none |
| `.attrs` | a dict of attributes, keyed by local name |
| `.text` | the element's own character data, concatenated and trimmed |
| `.children` | the child elements, in document order |

Names are local, which is the point: an upstream may spell its SOAP prefix
`soap:`, `soapenv:` or `S:`, and a script that matched on the prefix would
break when the upstream regenerated its stubs. The namespace is still on the
element for a script that needs to tell two vocabularies apart.

A descendant's character data belongs to the descendant, so `.text` on a
container element is empty rather than the concatenation of everything below
it. Children are an ordered list rather than a dict keyed by tag, because
repeated siblings and their order are what most documents carry.

`json.encode` renders an element as those same five fields, so a decoded
document can be exported, printed to the run log, or spliced into a dashboard
with `platform.publish_data`.

## Searching

`xml.find(node, path)` returns the first match or `None`; `xml.findall(node,
path)` returns every match as a list. The path language is a deliberately
small subset of XPath:

| Construct | Meaning |
| --- | --- |
| `a/b/c` | child steps |
| `//c` | a descendant at any depth |
| `*` | any element |
| `[@name='value']` | the elements carrying that attribute value |
| `[n]` | the nth match of the step, 1-based |

Paths are relative to the node they are evaluated against, so they never begin
with a single `/`. Predicates apply in order, so `//book[@status='live'][2]`
is the second live book rather than the second book if it is live.

Anything outside the subset — `text()`, `last()`, an axis, a namespace prefix,
a bare `[@attr]` existence test — fails the run where the path was written. A
path language that answers "no matches" to a construct it does not implement
teaches the author that the data is missing, which is the more expensive
mistake.

## Writing

`xml.encode(tree)` serialises an element, or a dict of the same five fields,
back to a document — for assembling a SOAP request body from data rather than
from string templates. Only `tag` is required.

`ns` is per element. A child without one is in no namespace, and the encoder
writes that as `xmlns=""`: correct XML, and rarely what the author meant. Give
every element in an envelope its namespace.

Attributes are written in name order, so the same tree produces the same
document on every run.

## Bounds

Parsing is strict and takes no input from the document about how to parse it:

- A `<!DOCTYPE>` declaration is refused outright. Go resolves no external
  entity, but an internal DTD can still declare entities that expand into each
  other, and no legitimate caller has needed one.
- An unknown entity, a mismatched end tag, a second root element or malformed
  markup is an error rather than a best guess.
- A character encoding other than UTF-8 is refused by name rather than read as
  bytes, which would hand back mojibake that looks like data.
- A document is capped at 8 MiB, 200 levels of nesting and 200,000 elements.
  `xml.encode` is held to the same bounds, which is what turns a tree that
  reaches itself — a dict inside a list inside that dict — into an error rather
  than an unbounded walk.

## The same tree through a tool call

`api_invoke_endpoint` decodes through the same parser. Its `decode` argument
takes `auto` (the default), `xml`, `json` or `text`, and `auto` returns a
parsed tree when the connection's catalog declares an XML media type on the
operation's success response. See
[Response bodies](../server/api-gateway.md#response-bodies).

When the call already decoded, `resp["body"]` is the tree and `xml.decode` is
unnecessary; when it did not, `xml.decode(resp["body"])` produces exactly the
same shape.
