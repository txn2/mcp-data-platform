package script

// The data-region contract for platform.publish_data (#1389).
//
// A semi-dynamic dashboard splits its two halves across two owners: the
// presentation lives in the asset, versioned where documents are versioned, and
// the data lives in the script, refreshed on the script's cadence. The seam
// between them is one marked region of the document — a data island the
// dashboard's own code reads at view time — and the platform replaces exactly
// that region's interior on every publish, leaving every other byte of the
// document as its author wrote it.

// DataRegionSelector is the CSS selector the marked data region must answer
// to: exactly one element carrying id="data". By convention that element is
//
//	<script type="application/json" id="data">...</script>
//
// so the island never renders as visible content, but the contract is the id,
// not the tag: the platform replaces the interior of whatever single element
// carries it. A document with no match, or more than one, refuses the publish
// rather than writing anywhere else.
const DataRegionSelector = "#data"
