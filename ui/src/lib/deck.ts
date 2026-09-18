/**
 * What the viewer knows about a slide deck from its document alone, and the
 * two things it asks of one (#1769).
 *
 * A deck is an HTML asset that loads the reveal.js runtime the platform serves
 * (#1767); nothing else marks it. The document runs in a sandboxed frame with
 * an opaque origin, so the page that frames it cannot call into it. What it
 * can do is post a message, and the runtime answers messages of the shape
 * below by default (its `postMessage` option): the page asks for the overview
 * that way. Printing is different: the runtime lays slides out one per page
 * only when it is initialized in its print view, and a sandboxed document may
 * call `print()` only when its frame grants `allow-modals`. So the page builds
 * a second document for printing, the asset with a print step added at its
 * head, and frames that with the grant.
 */

import { lightRendition } from "@/lib/printLight";

/** The served runtime's script path, which every deck names (#1767). */
export const RUNTIME_PATH = "/portal/vendor/reveal/reveal.js";

/** Whether the document loads the served slide runtime. */
export function isSlideDeck(content: string): boolean {
  return content.includes(RUNTIME_PATH);
}

/**
 * The message the runtime answers by toggling its overview, in the JSON form
 * its postMessage API reads.
 */
export function overviewMessage(): string {
  return JSON.stringify({ method: "toggleOverview", args: [] });
}

/**
 * What the print document posts to the page that framed it once `print()` has
 * returned, which is when the reader has closed the dialog or saved the PDF.
 */
export const PRINTED_MESSAGE = "mcp-data-platform:printed";

/**
 * The print step, added at the head of the print document so it runs before
 * any script the asset carries.
 *
 * The runtime is a global the asset's script tag assigns; the step takes that
 * assignment through a property setter and, before the asset initializes the
 * runtime, queues a `configure` that selects the print view (an option handed
 * to `configure` before `initialize` is applied at initialization, below
 * whatever the asset passes to `initialize` itself) and a listener for the
 * `pdf-ready` event the print view dispatches once every slide is a page. The
 * listener prints. A document that never assigns the runtime is not a deck
 * and prints as it is, once it has loaded.
 *
 * Whatever the document is, the last thing before the dialog opens is the
 * light rendition (lib/printLight): paper is white and a browser prints
 * backgrounds only on request, so a dark document printed as it stands is
 * light text on white (#1772). The rendition is carried into the frame as its
 * own text because the frame's origin is opaque and nothing can be called
 * across it. It runs inside a try: a document this cannot recolour is still a
 * document the reader asked to print.
 */
export const PRINT_STEP = `<script>(function(){
var lightRendition=${lightRendition.toString()};
var printed=false;
function print(){
  if(printed)return;
  printed=true;
  try{lightRendition(document);}catch(e){}
  try{window.print();}catch(e){}
  window.parent.postMessage(${JSON.stringify(PRINTED_MESSAGE)},"*");
}
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
      value.configure({view:"print"});
      value.on("pdf-ready",print);
    }
  }
});
window.addEventListener("load",function(){if(!hooked)print();});
})();</script>`;

/**
 * The asset's document with the print step at its head.
 *
 * The step goes just inside `<head>`, or `<html>` when there is no head tag,
 * so the doctype the document opens with is still the first thing the parser
 * sees; a document with neither tag takes the step first and is parsed as the
 * browser parses any headless fragment.
 */
export function printableDocument(content: string): string {
  const opening = /<head(\s[^>]*)?>/i.exec(content) ?? /<html(\s[^>]*)?>/i.exec(content);
  if (!opening) return PRINT_STEP + content;
  const at = opening.index + opening[0].length;
  return content.slice(0, at) + PRINT_STEP + content.slice(at);
}
