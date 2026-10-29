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
// The map tracker between the map-tracker markers is the thumbnail capture's
// too (ui/src/lib/thumbnail.ts), so a thumbnail and a PDF wait for a map the
// same way (#2087); TestMapTrackerIsTheThumbnailsToo holds the two copies
// together.
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
      keepScreenLayout();
    }
  }
});
// keepScreenLayout keeps a slide printed as it is laid out on screen (#2343).
// The print view adds its class and measures the slides a frame later; the
// observer runs between the two and makes two repairs:
//
// The print view moves each slide into a page wrapper, so a rule the document
// writes for ".slides > section" (its height, a background it draws in
// ::before) no longer matches. Each such rule in the document's own
// stylesheets is repeated for the slide inside its wrapper. The runtime's own
// stylesheets are left alone: their screen rules are not meant for print.
//
// The print view sets every section's padding to zero, so a deck that insets
// its slides on the section printed them flush against the page's corner. Each
// slide's padding is read with the print class lifted and set back on the
// slide, where it outranks the print rule. A stack holds slides rather than
// being one, and is left as the print view lays it out.
function keepScreenLayout(){
  var root=document.documentElement;
  var watch=new MutationObserver(function(){
    if(!root.classList.contains("reveal-print"))return;
    watch.disconnect();
    Array.prototype.forEach.call(document.styleSheets,function(sheet){
      if(sheet.href&&sheet.href.indexOf("/portal/vendor/reveal/")>=0)return;
      var rules;
      try{rules=sheet.cssRules;}catch(e){return;}
      repeatForPages(sheet,rules);
    });
    keepPadding(root);
  });
  watch.observe(root,{attributes:true,attributeFilter:["class"]});
}
var topSlide=/\.slides\s*>\s*section/;
function repeatForPages(parent,rules){
  for(var i=rules.length-1;i>=0;i--){
    var r=rules[i];
    if(!r.selectorText){
      if(r.cssRules)repeatForPages(r,r.cssRules);
      continue;
    }
    if(!topSlide.test(r.selectorText))continue;
    var sel=r.selectorText.split(",").map(function(s){
      return s.replace(topSlide,".slides > .pdf-page > section");
    }).join(",");
    try{parent.insertRule(sel+"{"+r.style.cssText+"}",i+1);}catch(e){}
  }
}
function keepPadding(root){
  var sides=["padding-top","padding-right","padding-bottom","padding-left"];
  var slides=document.querySelectorAll(".reveal .slides section:not(.stack)");
  root.classList.remove("reveal-print");
  var onScreen=Array.prototype.map.call(slides,function(s){
    var cs=getComputedStyle(s);
    return sides.map(function(p){return cs.getPropertyValue(p);});
  });
  root.classList.add("reveal-print");
  Array.prototype.forEach.call(slides,function(s,i){
    sides.forEach(function(p,j){
      var v=onScreen[i][j];
      if(v&&v!=="0px")s.style.setProperty(p,v,"important");
    });
  });
}
/*map-tracker:start*/
(function(){
var maps=[];
var maplibre;
Object.defineProperty(window,"maplibregl",{
  configurable:true,
  enumerable:true,
  get:function(){return maplibre;},
  set:function(value){
    maplibre=value;
    if(value&&typeof value.Map==="function"&&!value.Map.__tracked){
      var Base=value.Map;
      var Tracked=function(options){var m=new Base(options);maps.push(m);return m;};
      Tracked.prototype=Base.prototype;
      Tracked.__tracked=true;
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
window.__mapCount=function(){return maps.length;};
window.__mapsDrawn=function(bound){
  if(!maps.length)return Promise.resolve();
  var timeout=new Promise(function(resolve){setTimeout(resolve,bound);});
  return Promise.race([Promise.all(maps.map(drawn)),timeout]);
};
})();
/*map-tracker:end*/
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
  if(window.__mapCount()){
    settled=settled.then(function(){return window.__mapsDrawn(20000);}).then(screenPage);
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
