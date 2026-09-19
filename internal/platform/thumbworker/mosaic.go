package thumbworker

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/headless"
)

// mosaicCSS lays out one to four member tiles the way a collection's tile has
// always looked: one fills it, two sit side by side, three put the first
// across the top, four make a grid, on a dark backing with a 4px gap.
const mosaicCSS = `html,body{margin:0;width:400px;height:300px;background:#1e293b;overflow:hidden}
.m{display:grid;gap:4px;width:400px;height:300px}
.m img{width:100%;height:100%;object-fit:cover;display:block;min-width:0;min-height:0}
.n1{grid-template-columns:1fr;grid-template-rows:1fr}
.n2{grid-template-columns:1fr 1fr;grid-template-rows:1fr}
.n3,.n4{grid-template-columns:1fr 1fr;grid-template-rows:1fr 1fr}
.n3 img:first-child{grid-column:1 / 3}`

// mosaicReady resolves once every member tile has decoded.
const mosaicReady = `Promise.all(Array.prototype.map.call(document.images, function(i){ return i.decode(); }))
  .then(function(){ return ""; }, function(){ return "a member tile could not be decoded"; })`

// mosaicPage is the page a collection's tile is drawn from, with its member
// tiles served beside it.
func mosaicPage(tiles [][]byte) headless.Page {
	files := map[string]headless.File{}
	imgs := make([]string, 0, len(tiles))
	for i, t := range tiles {
		p := fmt.Sprintf("/m/%d.png", i)
		files[p] = headless.File{Body: t, ContentType: tileContentType}
		imgs = append(imgs, `<img alt="" src="`+p+`">`)
	}
	doc := `<!DOCTYPE html><html><head><meta charset="utf-8"><style>` + mosaicCSS + `</style></head><body>` +
		fmt.Sprintf(`<div class="m n%d">`, len(tiles)) + strings.Join(imgs, "") + `</div></body></html>`
	return headless.Page{
		Document: []byte(doc),
		Files: func(p string) (headless.File, bool) {
			f, ok := files[p]
			return f, ok
		},
		Ready:  mosaicReady,
		Width:  tileWidth,
		Height: tileHeight,
		Scale:  tileScale,
	}
}
