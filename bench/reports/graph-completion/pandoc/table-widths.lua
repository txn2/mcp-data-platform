-- table-widths.lua — set proportional column widths for the rendered PDF/HTML.
-- Print-only formatting: the committed benchmark-report-graph-completion.md
-- keeps plain `| --- |` separators (the mkdocs site sizes columns
-- responsively); this filter only shapes the pandoc render. Tables are matched
-- by their header text so the assignments stay stable regardless of table
-- order.

local stringify = pandoc.utils.stringify

local function header_texts(tbl)
  local out = {}
  local rows = tbl.head and tbl.head.rows or {}
  if #rows > 0 then
    for _, cell in ipairs(rows[1].cells) do
      out[#out + 1] = stringify(cell.contents)
    end
  end
  return out
end

-- longest_cell returns the character length of the longest body cell, which is
-- what decides whether pandoc's default widths are survivable: short numeric
-- cells render fine unruled, a long prose column does not.
local function longest_cell(tbl)
  local longest = 0
  for _, body in ipairs(tbl.bodies or {}) do
    for _, row in ipairs(body.body or {}) do
      for _, cell in ipairs(row.cells) do
        local len = #stringify(cell.contents)
        if len > longest then longest = len end
      end
    end
  end
  return longest
end

local function set_widths(tbl, widths)
  for i = 1, #tbl.colspecs do
    if widths[i] then
      tbl.colspecs[i] = { tbl.colspecs[i][1], widths[i] }
    end
  end
end

function Table(tbl)
  local h = header_texts(tbl)
  local n = #tbl.colspecs
  local key = table.concat(h, "|")

  if n == 2 and (key == "" or key == "|") then
    set_widths(tbl, { 0.30, 0.70 })                 -- report metadata block (empty or absent header)
  elseif key == "Kill condition|Reading" then
    set_widths(tbl, { 0.30, 0.70 })                 -- 8. Kill conditions, applied
  elseif n == 11 and h[1] == "Scale" then
    -- 2. The confirmatory matrix: eleven numeric columns; without explicit
    -- widths pandoc squeezes the headers until they overprint.
    set_widths(tbl, { 0.06, 0.09, 0.07, 0.04, 0.05, 0.09, 0.06, 0.11, 0.10, 0.10, 0.13 })
  elseif n == 3 and h[1] == "Run family" then
    set_widths(tbl, { 0.24, 0.38, 0.38 })           -- 11. Data availability
  elseif longest_cell(tbl) > 60 then
    -- An unruled table gets pandoc's default widths. That is fine for short
    -- numeric cells, but a table that also carries a long prose column gets
    -- squeezed until cells overprint — which shipped once, in a deposited
    -- PDF of a sibling report, because adding a column to a matched table
    -- silently unmatched it. Warn only for that shape, so the warning stays
    -- worth reading.
    io.stderr:write(
      "table-widths.lua: WARNING no width rule for a " .. n ..
      "-column table with a long prose column, header [" .. key .. "].\n" ..
      "  Pandoc's defaults will squeeze it and cells may overprint in the PDF.\n" ..
      "  Add a rule in bench/reports/graph-completion/pandoc/table-widths.lua,\n" ..
      "  then check the rendered page as an image, not just its text layer.\n")
  end

  return tbl
end

-- Long file paths in inline code (`bench/reports/graph-completion/`) are
-- one unbreakable word to TeX and overhang the right margin. Hyphenation alone
-- is not enough: the natural break points in a path are its slashes. Emit the
-- LaTeX for such spans with \allowbreak after each slash. Only spans that both
-- contain a slash and are long enough to matter are touched, so ordinary inline
-- code (`search`, `fetch`) is left exactly as pandoc renders it.
-- Paths here are alphanumerics plus / . - _ ; anything carrying a character
-- with LaTeX meaning is handed back to pandoc untouched rather than escaped by
-- hand, because a half-right escaper is worse than none.
local RISKY = "[\\%^~{}%$#%%&]"

function Code(el)
  if not FORMAT:match("latex") then return nil end
  if #el.text < 24 or not el.text:find("/") then return nil end
  if el.text:find(RISKY) then return nil end
  local escaped = el.text:gsub("_", "\\_")
  local breakable = escaped:gsub("/", "/\\allowbreak{}")
  return pandoc.RawInline("latex", "\\texttt{" .. breakable .. "}")
end
