package sourcecheck

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

func TestCheckD2(t *testing.T) {
	require.NoError(t, Check(contenttype.D2, "vpc: {\n  web -> api: http\n  api: {icon: mcp://global/icons/api.svg}\n}\nusers -> vpc.web\n"))
	require.NoError(t, Check(contenttype.D2, "a: {shape: image; icon: \"data:image/png;base64,AAAA\"}"))

	for name, tc := range map[string]struct{ src, want string }{
		"empty":         {"  ", "empty"},
		"parse error":   {"a -> -> b\n", "diagram.d2:1:"},
		"compile error": {"a: {shape: nosuchshape}", "does not compile"},
		"external icon": {"a: {icon: https://icons.terrastruct.com/aws/api.svg}", `"https://icons.terrastruct.com/aws/api.svg"`},
		"link":          {"a: {link: https://example.com}", "https://example.com"},
		"import":        {"...@other", "imports another file"},
		"markdown img":  {"a: |md\n  ![x](http://evil.example.com/p.png)\n|", "http://evil.example.com/p.png"},
	} {
		t.Run(name, func(t *testing.T) {
			err := Check(contenttype.D2, tc.src)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestCheckExcalidraw(t *testing.T) {
	ok := `{"type":"excalidraw","version":2,"elements":[{"type":"rectangle","id":"a"},{"type":"image","id":"b","fileId":"f1","link":"mcp:asset:x"}],
		"files":{"f1":{"mimeType":"image/png","dataURL":"data:image/png;base64,AAAA"}}}`
	require.NoError(t, Check(contenttype.Excalidraw, ok))

	for name, tc := range map[string]struct{ src, want string }{
		"not json":       {`{`, "not valid JSON"},
		"not a scene":    {`{"elements":[]}`, `"type": "excalidraw"`},
		"no elements":    {`{"type":"excalidraw"}`, `"elements"`},
		"untyped":        {`{"type":"excalidraw","elements":[{}]}`, "has no type"},
		"embeddable":     {`{"type":"excalidraw","elements":[{"type":"embeddable","link":"https://www.youtube.com/watch?v=x"}]}`, "frames a web page"},
		"external link":  {`{"type":"excalidraw","elements":[{"type":"text","link":"https://evil.example.com"}]}`, "https://evil.example.com"},
		"external image": {`{"type":"excalidraw","elements":[],"files":{"f1":{"dataURL":"https://evil.example.com/a.png"}}}`, "file f1"},
		"js link":        {`{"type":"excalidraw","elements":[{"type":"text","link":"javascript:alert(1)"}]}`, "javascript:alert(1)"},
	} {
		t.Run(name, func(t *testing.T) {
			err := Check(contenttype.Excalidraw, tc.src)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestCheckLottie(t *testing.T) {
	ok := `{"v":"5.7.4","fr":30,"ip":0,"op":60,"w":100,"h":100,"layers":[{"ty":4,"ks":{"p":{"a":0,"k":[50,50]}}}],
		"assets":[{"id":"image_0","w":10,"h":10,"u":"","p":"data:image/png;base64,AAAA","e":1},{"id":"comp_0","layers":[]}]}`
	require.NoError(t, Check(contenttype.LottieJSON, ok))

	for name, tc := range map[string]struct{ src, want string }{
		"not json":       {`[`, "not valid JSON"},
		"array":          {`[]`, "not valid JSON"},
		"no frame rate":  {`{"ip":0,"op":1,"layers":[]}`, `"fr"`},
		"no layers":      {`{"fr":30,"ip":0,"op":1}`, `"layers"`},
		"expression":     {`{"fr":30,"ip":0,"op":1,"layers":[{"ks":{"r":{"a":0,"k":0,"x":"var $bm_rt = time*360;"}}}]}`, "1 expression"},
		"external image": {`{"fr":30,"ip":0,"op":1,"layers":[],"assets":[{"id":"image_0","u":"https://cdn.example.com/","p":"a.png","e":0}]}`, "https://cdn.example.com/a.png"},
		"external font":  {`{"fr":30,"ip":0,"op":1,"layers":[],"fonts":{"list":[{"fName":"X","fPath":"https://fonts.example.com/x.css"}]}}`, "https://fonts.example.com/x.css"},
	} {
		t.Run(name, func(t *testing.T) {
			err := Check(contenttype.LottieJSON, tc.src)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
