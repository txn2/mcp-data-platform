package pdfhttp

import (
	"bytes"
	"regexp"
)

// PrintStep is added at the head of a document the renderer prints, so it
// runs before any script the document carries.
//
// A deck is an HTML document that loads the slide runtime the platform serves
// (#1767). The runtime is a global the document's script tag assigns; the step
// takes that assignment through a property setter and, before the document
// initializes the runtime, queues a configure that selects the print view with
// every build step of a slide on one page (an option handed to configure
// before initialize is applied at initialization, below whatever the document
// passes to initialize itself) and a listener for the pdf-ready event the
// print view dispatches once every slide is a page. A document that never
// assigns the runtime is not a deck, and is ready once it has loaded and its
// fonts are in.
//
// A map is an HTML document that loads the map runtime the platform serves
// (#2068), and draws its tiles after the document has loaded, so load is too
// early to print it. The step takes the runtime's assignment the same way and
// keeps every map the document creates; once the document has loaded it waits
// for each of them to finish drawing (MapLibre's idle event: style, tiles and
// any transition done), bounded so a map whose tiles never arrive still
// prints, and prints it on one page the size of its screen layout.
//
// Either way the step resolves window.__pdfReady, which Ready waits on.
const PrintStep = `<script>(function(){
var done;
window.__pdfReady=new Promise(function(resolve){done=resolve;});
var runtime;
var hooked=false;
Object.defineProperty(window,"Reveal",{
  configurable:true,
  enumerable:true,
  get:function(){return runtime;},
  set:function(value){
    runtime=value;
    if(!hooked&&value&&typeof value.configure==="function"&&typeof value.on==="function"){
      hooked=true;
      value.configure({view:"print",pdfSeparateFragments:false});
      value.on("pdf-ready",function(){done("");});
    }
  }
});
var maps=[];
var maplibre;
Object.defineProperty(window,"maplibregl",{
  configurable:true,
  enumerable:true,
  get:function(){return maplibre;},
  set:function(value){
    maplibre=value;
    if(value&&typeof value.Map==="function"&&!value.Map.__pdfTracked){
      var Base=value.Map;
      var Tracked=function(options){var m=new Base(options);maps.push(m);return m;};
      Tracked.prototype=Base.prototype;
      Tracked.__pdfTracked=true;
      value.Map=Tracked;
    }
  }
});
function drawn(m){
  return new Promise(function(resolve){
    if(m.loaded()&&m.areTilesLoaded()){resolve();return;}
    m.once("idle",function(){resolve();});
  });
}
// screenPage prints the document on one page the size of its screen layout.
// A map is drawn for the width it was laid out at; printed on a narrower
// paper size its canvas would be cut at the page's edge.
function screenPage(){
  var st=document.createElement("style");
  var h=Math.max(window.innerHeight,document.documentElement.scrollHeight);
  st.textContent="@page{size:"+window.innerWidth+"px "+h+"px;margin:0}";
  document.head.appendChild(st);
}
window.addEventListener("load",function(){
  if(hooked)return;
  var fonts=document.fonts&&document.fonts.ready?document.fonts.ready:Promise.resolve();
  var settled=fonts.then(function(){},function(){});
  if(maps.length){
    var bound=new Promise(function(resolve){setTimeout(resolve,20000);});
    settled=settled.then(function(){return Promise.race([Promise.all(maps.map(drawn)),bound]);}).then(screenPage);
  }
  settled.then(function(){done("");});
});
})();</script>`

// Ready is the expression the renderer waits on before it prints: the step's
// promise, once the document has run far enough to create it.
const Ready = `new Promise(function(resolve){
  var waited = 0;
  (function poll(){
    if (window.__pdfReady) {
      window.__pdfReady.then(function(v){ resolve(v || ""); }, function(e){ resolve(String((e && e.message) || e)); });
      return;
    }
    waited += 50;
    if (waited > 30000) { resolve("the document did not start"); return; }
    setTimeout(poll, 50);
  })();
})`

var (
	headTag = regexp.MustCompile(`(?i)<head(\s[^>]*)?>`)
	htmlTag = regexp.MustCompile(`(?i)<html(\s[^>]*)?>`)
)

// WithPrintStep is the document with PrintStep at its head.
//
// The step goes just inside <head>, or <html> when there is no head tag, so
// the doctype the document opens with is still the first thing the parser
// sees; a document with neither tag takes the step first and is parsed as the
// browser parses any headless fragment.
func WithPrintStep(doc []byte) []byte {
	loc := headTag.FindIndex(doc)
	if loc == nil {
		loc = htmlTag.FindIndex(doc)
	}
	if loc == nil {
		return append([]byte(PrintStep), doc...)
	}
	return bytes.Join([][]byte{doc[:loc[1]], []byte(PrintStep), doc[loc[1]:]}, nil)
}
