// Quick preview server for the content viewer. Run:
//
//	go run ./cmd/preview-content-viewer
//
// Then open http://localhost:9090
package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/contentviewer"
)

const viewerHTML = `<!DOCTYPE html>
<html lang="en" data-theme="light">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Name}}</title>
<style>
:root,[data-theme="light"]{--bg:#f5f5f5;--bg-surface:#fff;--text:#1a1a1a;--text-muted:#6b7280;--page-border:#e5e5e5;--badge-bg:#e5e7eb;--badge-text:#374151;--toggle-hover:#f3f4f6;--background:0 0% 96%;--foreground:0 0% 10%;--card:0 0% 100%;--card-foreground:0 0% 10%;--muted:0 0% 96%;--muted-foreground:220 9% 46%;--border:0 0% 90%;--primary:221.2 83.2% 53.3%;--primary-foreground:210 40% 98%}
[data-theme="dark"]{--bg:#111113;--bg-surface:#1a1a1e;--text:#e4e4e7;--text-muted:#71717a;--page-border:#27272a;--badge-bg:#27272a;--badge-text:#a1a1aa;--toggle-hover:#27272a;--background:240 6% 6%;--foreground:240 5% 90%;--card:240 5% 11%;--card-foreground:240 5% 90%;--muted:240 4% 16%;--muted-foreground:240 5% 48%;--border:240 4% 16%;--primary:217.2 91.2% 59.8%;--primary-foreground:222.2 47.4% 11.2%}
*{margin:0;padding:0;box-sizing:border-box}html,body{height:100%}
body{font-family:system-ui,-apple-system,sans-serif;background:var(--bg);color:var(--text);display:flex;flex-direction:column;min-height:100vh}
.header{background:var(--bg-surface);border-bottom:1px solid var(--page-border);padding:12px 24px;display:flex;align-items:center;gap:12px;flex-shrink:0}
.header h1{font-size:16px;font-weight:600;flex:1}
.badge{font-size:11px;background:var(--badge-bg);color:var(--badge-text);padding:2px 8px;border-radius:10px}
.theme-toggle{background:none;border:1px solid var(--page-border);border-radius:6px;padding:4px 8px;cursor:pointer;color:var(--text-muted)}
.content{width:100%;padding:16px;flex:1;display:flex;flex-direction:column}
.content iframe{flex:1;min-height:60vh}
nav{display:flex;gap:8px;padding:12px 24px;background:var(--bg-surface);border-bottom:1px solid var(--page-border)}
nav a{color:var(--text-muted);text-decoration:none;padding:4px 12px;border-radius:6px;font-size:13px}
nav a:hover{background:var(--toggle-hover)}
nav a.active{background:var(--badge-bg);color:var(--badge-text)}
</style>
<script>
(function(){
  var params=new URLSearchParams(location.search);
  var forced=params.get("theme");
  if(forced==="light"||forced==="dark"){var theme=forced}
  else{var saved=null;try{saved=localStorage.getItem("mdp-theme")}catch(e){}
  var theme=saved||(window.matchMedia("(prefers-color-scheme:dark)").matches?"dark":"light");}
  document.documentElement.setAttribute("data-theme",theme);
  document.documentElement.classList.toggle("dark",theme==="dark");
})();
</script>
</head>
<body>
<div class="header">
  <h1>{{.Name}}</h1>
  <span class="badge">{{.ContentType}}</span>
  <button class="theme-toggle" id="tt" onclick="var h=document.documentElement,t=h.getAttribute('data-theme')==='dark'?'light':'dark';h.setAttribute('data-theme',t);h.classList.toggle('dark',t==='dark');try{localStorage.setItem('mdp-theme',t)}catch(e){}">Toggle Theme</button>
</div>
<nav>
  <a href="/?type=markdown">Markdown</a>
  <a href="/?type=svg">SVG</a>
  <a href="/?type=jsx">JSX</a>
  <a href="/?type=html">HTML</a>
  <a href="/?type=plain">Plain Text</a>
</nav>
<div class="content">
  <style>{{.ContentViewerCSS}}</style>
  <div id="content-root"><p style="color:var(--text-muted);padding:16px">Loading...</p></div>
  <script type="application/json" id="content-data">{{.ContentJSON}}</script>
  <script>{{.ContentViewerJS}}</script>
</div>
</body>
</html>`

var samples = map[string][2]string{
	"markdown": {"text/markdown", "# Hello World\n\nThis is a **bold** test with GFM:\n\n| Feature | Status |\n|---------|--------|\n| Tables | Working |\n| Strikethrough | ~~yes~~ |\n\n- [x] Task list\n- [ ] Another task\n\n```go\nfunc main() {\n    fmt.Println(\"Hello\")\n}\n```\n"},
	"svg":      {"image/svg+xml", `<svg viewBox="0 0 200 200" xmlns="http://www.w3.org/2000/svg"><circle cx="100" cy="100" r="80" fill="#3b82f6" opacity="0.8"/><text x="100" y="108" text-anchor="middle" fill="white" font-size="24" font-family="system-ui">SVG</text></svg>`},
	"jsx":      {"text/jsx", "import { useState } from 'react';\n\nexport default function Counter() {\n  const [count, setCount] = useState(0);\n  return (\n    <div style={{padding: '2rem', fontFamily: 'system-ui'}}>\n      <h1>Counter: {count}</h1>\n      <button onClick={() => setCount(c => c + 1)}\n        style={{padding: '8px 16px', fontSize: '16px', cursor: 'pointer'}}>\n        Increment\n      </button>\n    </div>\n  );\n}\n"},
	"html":     {"text/html", "<!DOCTYPE html>\n<html>\n<head><style>body{font-family:system-ui;padding:2rem}h1{color:#3b82f6}</style></head>\n<body><h1>Hello from HTML</h1><p>This is rendered in a sandboxed iframe.</p></body>\n</html>"},
	"plain":    {"text/plain", "This is plain text content.\nIt should be displayed in a <pre> block.\n\nSpecial chars: <script>alert('xss')</script> & \"quotes\""},
}

var viewerTpl = template.Must(template.New("viewer").Parse(viewerHTML))

// newHandler returns the HTTP handler that renders the preview page.
// Extracted from main() so it can be tested.
func newHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		typ := r.URL.Query().Get("type")
		if typ == "" {
			typ = "markdown"
		}
		sample, ok := samples[typ]
		if !ok {
			sample = samples["markdown"]
		}

		contentJSON, _ := json.Marshal(map[string]string{ // #nosec G104 -- string map marshaling cannot fail
			"contentType": sample[0],
			"content":     sample[1],
		})

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = viewerTpl.Execute(w, map[string]any{ // #nosec G104 -- template execution on ResponseWriter; error is logged by http.Server
			"Name":             fmt.Sprintf("Preview: %s", typ),
			"ContentType":      sample[0],
			"ContentJSON":      template.JS(contentJSON),        // #nosec G203 -- dev-only preview with static samples
			"ContentViewerJS":  template.JS(contentviewer.JS),   // #nosec G203 -- embedded bundle, not user input
			"ContentViewerCSS": template.CSS(contentviewer.CSS), // #nosec G203 -- embedded bundle, not user input
		})
	}
}

func main() {
	http.Handle("/", newHandler())

	fmt.Println("Preview server at http://localhost:9090")
	fmt.Println("  http://localhost:9090/?type=markdown")
	fmt.Println("  http://localhost:9090/?type=svg")
	fmt.Println("  http://localhost:9090/?type=jsx")
	fmt.Println("  http://localhost:9090/?type=html")
	fmt.Println("  http://localhost:9090/?type=plain")
	log.Fatal(http.ListenAndServe(":9090", nil)) // #nosec G114 -- dev-only local preview server // nosemgrep: go.lang.security.audit.net.use-tls.use-tls
}
