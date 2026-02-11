/**
 * Mermaid diagram fullscreen viewer with zoom and pan controls.
 *
 * mkdocs-material renders mermaid diagrams inside a CLOSED Shadow DOM,
 * making the SVG inaccessible from outside JavaScript. We work around
 * this by fetching the page's own HTML source, parsing it with
 * DOMParser to extract the original mermaid source text, and
 * re-rendering via mermaid.render() into a fullscreen overlay.
 */
(function () {
  "use strict";

  var MIN_SCALE = 0.25;
  var MAX_SCALE = 5;
  var ZOOM_STEP = 0.25;
  var ZOOM_WHEEL_FACTOR = 0.001;
  var PROCESSED_ATTR = "data-mermaid-fs";

  var state = { scale: 1, panX: 0, panY: 0, dragging: false, lastX: 0, lastY: 0 };
  var diagramSources = [];

  // Extract mermaid source text by fetching the page's own HTML.
  // The server always sends <pre class="mermaid"><code>...</code></pre>,
  // regardless of what Material does client-side.
  function fetchAndAttach() {
    fetch(location.href)
      .then(function (r) { return r.text(); })
      .then(function (html) {
        var doc = new DOMParser().parseFromString(html, "text/html");
        diagramSources = [];
        var seen = {};
        doc.querySelectorAll("pre.mermaid").forEach(function (pre) {
          var code = pre.querySelector("code");
          var text = (code || pre).textContent.trim();
          if (text && !seen[text]) {
            seen[text] = true;
            diagramSources.push(text);
          }
        });
        attachButtons();
      });
  }

  function isDarkMode() {
    return document.body.getAttribute("data-md-color-scheme") === "slate";
  }

  function createOverlay(source) {
    if (typeof mermaid === "undefined" || !mermaid.render) return;

    var dark = isDarkMode();
    var savedConfig = mermaid.getConfig ? mermaid.getConfig() : null;
    mermaid.initialize({ startOnLoad: false, theme: dark ? "dark" : "neutral" });

    var id = "mermaid-fs-render-" + Date.now();
    mermaid.render(id, source).then(function (result) {
      // Restore original mermaid config so inline diagrams aren't affected
      if (savedConfig) mermaid.initialize(savedConfig);
      var overlay = document.createElement("div");
      overlay.className = "mermaid-fullscreen-overlay";
      if (!dark) overlay.classList.add("mermaid-fs-light");

      var viewport = document.createElement("div");
      viewport.className = "mermaid-fs-viewport";
      viewport.innerHTML = result.svg;

      var svg = viewport.querySelector("svg");
      if (svg) {
        svg.removeAttribute("width");
        svg.removeAttribute("height");
        svg.removeAttribute("style");
        svg.style.width = "85vw";
        svg.style.maxHeight = "80vh";
        svg.style.borderRadius = "12px";
        svg.style.padding = "1.5rem";
        svg.style.boxSizing = "border-box";
      }

      var toolbar = document.createElement("div");
      toolbar.className = "mermaid-fs-toolbar";

      var buttons = [
        { label: "\u2212", title: "Zoom out", action: function () { zoom(-ZOOM_STEP); } },
        { label: "Reset", title: "Reset zoom and position", action: resetView },
        { label: "+", title: "Zoom in", action: function () { zoom(ZOOM_STEP); } },
        { label: "\u2715", title: "Close (Esc)", action: closeOverlay }
      ];

      buttons.forEach(function (b) {
        var btn = document.createElement("button");
        btn.className = "mermaid-fs-btn" + (b.label === "\u2715" ? " mermaid-fs-close" : "");
        btn.textContent = b.label;
        btn.title = b.title;
        btn.addEventListener("click", function (e) { e.stopPropagation(); b.action(); });
        toolbar.appendChild(btn);
      });

      overlay.appendChild(viewport);
      overlay.appendChild(toolbar);

      overlay.addEventListener("click", function (e) {
        if (e.target === overlay) closeOverlay();
      });

      overlay.addEventListener("wheel", function (e) {
        e.preventDefault();
        zoom(-e.deltaY * ZOOM_WHEEL_FACTOR);
      }, { passive: false });

      viewport.addEventListener("mousedown", function (e) {
        state.dragging = true;
        state.lastX = e.clientX;
        state.lastY = e.clientY;
        viewport.style.cursor = "grabbing";
      });
      document.addEventListener("mousemove", handleMouseMove);
      document.addEventListener("mouseup", function () {
        state.dragging = false;
        if (viewport) viewport.style.cursor = "grab";
      });

      viewport.addEventListener("touchstart", function (e) {
        if (e.touches.length === 1) {
          state.dragging = true;
          state.lastX = e.touches[0].clientX;
          state.lastY = e.touches[0].clientY;
        }
      }, { passive: true });
      document.addEventListener("touchmove", handleTouchMove, { passive: false });
      document.addEventListener("touchend", function () { state.dragging = false; });

      document.addEventListener("keydown", handleKeyDown);

      document.body.appendChild(overlay);
      document.body.style.overflow = "hidden";
      resetView();
    }).catch(function () {
      if (savedConfig) mermaid.initialize(savedConfig);
    });
  }

  function handleMouseMove(e) {
    if (!state.dragging) return;
    state.panX += e.clientX - state.lastX;
    state.panY += e.clientY - state.lastY;
    state.lastX = e.clientX;
    state.lastY = e.clientY;
    applyTransform();
  }

  function handleTouchMove(e) {
    if (!state.dragging || e.touches.length !== 1) return;
    e.preventDefault();
    state.panX += e.touches[0].clientX - state.lastX;
    state.panY += e.touches[0].clientY - state.lastY;
    state.lastX = e.touches[0].clientX;
    state.lastY = e.touches[0].clientY;
    applyTransform();
  }

  function handleKeyDown(e) {
    if (e.key === "Escape") closeOverlay();
  }

  function applyTransform() {
    var vp = document.querySelector(".mermaid-fs-viewport");
    if (vp) {
      vp.style.transform =
        "translate(" + state.panX + "px, " + state.panY + "px) scale(" + state.scale + ")";
    }
  }

  function zoom(delta) {
    state.scale = Math.min(MAX_SCALE, Math.max(MIN_SCALE, state.scale + delta));
    applyTransform();
  }

  function resetView() {
    state.scale = 1;
    state.panX = 0;
    state.panY = 0;
    applyTransform();
  }

  function closeOverlay() {
    var overlay = document.querySelector(".mermaid-fullscreen-overlay");
    if (overlay) {
      overlay.remove();
      document.body.style.overflow = "";
      document.removeEventListener("keydown", handleKeyDown);
      document.removeEventListener("mousemove", handleMouseMove);
      document.removeEventListener("touchmove", handleTouchMove);
      state.dragging = false;
    }
  }

  function attachButtons() {
    var mermaidEls = document.querySelectorAll("div.mermaid");
    var sourceIndex = 0;

    mermaidEls.forEach(function (el) {
      if (el.hasAttribute(PROCESSED_ATTR)) return;
      if (el.closest(".mermaid-fullscreen-overlay")) return;

      var source = diagramSources[sourceIndex];
      sourceIndex++;
      if (!source) return;

      el.setAttribute(PROCESSED_ATTR, "true");

      var wrapper = document.createElement("div");
      wrapper.className = "mermaid-wrapper";
      el.parentNode.insertBefore(wrapper, el);
      wrapper.appendChild(el);

      var btn = document.createElement("button");
      btn.className = "mermaid-expand-btn";
      btn.title = "View fullscreen";
      btn.setAttribute("aria-label", "View diagram fullscreen");
      btn.innerHTML =
        '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" ' +
        'stroke="currentColor" stroke-width="2">' +
        '<path d="M8 3H5a2 2 0 00-2 2v3m18 0V5a2 2 0 00-2-2h-3' +
        'm0 18h3a2 2 0 002-2v-3M3 16v3a2 2 0 002 2h3"/></svg>';

      btn.addEventListener("click", function (e) {
        e.preventDefault();
        e.stopPropagation();
        createOverlay(source);
      });

      wrapper.appendChild(btn);
    });
  }

  // Wait for Material to finish rendering, then fetch sources and attach.
  // Use a MutationObserver to detect when <div class="mermaid"> appears.
  var debounceTimer = null;
  var observer = new MutationObserver(function () {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(function () {
      var divMermaids = document.querySelectorAll("div.mermaid:not([" + PROCESSED_ATTR + "])");
      if (divMermaids.length > 0) {
        fetchAndAttach();
      }
    }, 500);
  });

  function init() {
    // If div.mermaid already exists, fetch and attach immediately
    if (document.querySelectorAll("div.mermaid").length > 0) {
      fetchAndAttach();
    }
    observer.observe(document.body, { childList: true, subtree: true });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", function () {
      setTimeout(init, 500);
    });
  } else {
    setTimeout(init, 500);
  }

  // Hook into mkdocs-material instant navigation
  var hookInterval = setInterval(function () {
    if (typeof document$ !== "undefined") {
      clearInterval(hookInterval);
      document$.subscribe(function () {
        setTimeout(fetchAndAttach, 1500);
      });
    }
  }, 250);
})();
