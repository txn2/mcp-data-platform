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
  end

  return tbl
end
