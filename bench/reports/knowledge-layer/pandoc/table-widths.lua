-- table-widths.lua — set proportional column widths for the rendered PDF/HTML.
-- Print-only formatting: the committed benchmark-report.md keeps plain
-- `| --- |` separators (the mkdocs site sizes columns responsively); this filter
-- only shapes the pandoc render. Tables are matched by their header text so the
-- assignments stay stable regardless of table order.

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

  if n == 2 and h[1] == "" and h[2] == "" then
    set_widths(tbl, { 0.35, 0.65 })                 -- report metadata block
  elseif key == "Study|Directory" then
    set_widths(tbl, { 0.40, 0.60 })                 -- 9. Data availability
  elseif key == "Trap class|The fact the agent must know" then
    set_widths(tbl, { 0.30, 0.70 })                 -- S3 trap classes
  elseif key == "Arm|Name|What the agent is connected to" then
    set_widths(tbl, { 0.20, 0.20, 0.60 })           -- 2.1 arm configurations
  elseif n == 4 and h[1] == "Metric" then
    set_widths(tbl, { 0.23, 0.16, 0.09, 0.52 })     -- S5 lifecycle scorecard
  elseif n == 5 and h[1] == "Metric" then
    -- S5 lifecycle scorecard with the v1.1 comparison column (report v2.0).
    -- Without an explicit rule this table falls through to pandoc's default
    -- widths and the metric names collide with the rate column in the PDF.
    set_widths(tbl, { 0.22, 0.16, 0.09, 0.15, 0.38 })
  elseif n >= 4 and longest_cell(tbl) > 60 then
    -- An unruled table gets pandoc's default widths. That is fine for short
    -- numeric cells, but a table that also carries a long prose column gets
    -- squeezed until cells overprint — which shipped once, in the deposited
    -- report v2.0 PDF, because adding a column to a matched table silently
    -- unmatched it. Warn only for that shape, so the warning stays worth
    -- reading.
    io.stderr:write(
      "table-widths.lua: WARNING no width rule for a " .. n ..
      "-column table with a long prose column, header [" .. key .. "].\n" ..
      "  Pandoc's defaults will squeeze it and cells may overprint in the PDF.\n" ..
      "  Add a rule in bench/reports/knowledge-layer/pandoc/table-widths.lua,\n" ..
      "  then check the rendered page as an image, not just its text layer.\n")
  end

  return tbl
end

-- Long file paths in inline code (`bench/reports/knowledge-layer/figures/`) are
-- one unbreakable word to TeX and overhang the right margin. Hyphenation alone
-- is not enough: the natural break points in a path are its slashes. Emit the
-- LaTeX for such spans with \allowbreak after each slash. Only spans that both
-- contain a slash and are long enough to matter are touched, so ordinary inline
-- code (`search`, `memory_capture`) is left exactly as pandoc renders it.
-- Paths here are alphanumerics plus / . - _ ; anything carrying a character
-- with LaTeX meaning is handed back to pandoc untouched rather than escaped by
-- hand, because a half-right escaper is worse than none (the first attempt
-- escaped specials and then replaced every backslash, mangling its own output).
local RISKY = "[\\%^~{}%$#%%&]"

function Code(el)
  if not FORMAT:match("latex") then return nil end
  if #el.text < 24 or not el.text:find("/") then return nil end
  if el.text:find(RISKY) then return nil end
  local escaped = el.text:gsub("_", "\\_")
  local breakable = escaped:gsub("/", "/\\allowbreak{}")
  return pandoc.RawInline("latex", "\\texttt{" .. breakable .. "}")
end
