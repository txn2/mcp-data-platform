// Recovers a page whose entry script or preloaded module did not load (#2495).
//
// During a rolling update index.html can come from a replica on one build and
// ask a replica on the other for its hashed files, which answers 404 (#2139),
// and the entry never runs to recover on its own. vite.config.ts writes this
// file inline into index.html, above the tags the build injects, so it cannot
// 404 itself and its listener exists before those tags load. On the first
// failed build file it reloads; if a reload already happened in the last 30
// seconds it says what is happening instead of leaving a blank page. It shares
// its storage key and window with src/lib/chunkReload.ts, so the two never
// reload each other in a loop, and it stands aside once the app has drawn
// anything, which is where that recovery takes over. __ASSETS__ is replaced
// with the build's asset path.
(function () {
  var KEY = "mcp-portal-chunk-reload-at";
  var WINDOW_MS = 30000;
  var ASSETS = "__ASSETS__";
  var handled = false;

  function assetURL(el) {
    var tag = el && el.tagName ? el.tagName.toLowerCase() : "";
    if (tag === "script" && el.type === "module") return el.src;
    if (tag === "link" && /^(modulepreload|stylesheet)$/i.test(el.rel || "")) return el.href;
    return "";
  }

  function isBuildAsset(el) {
    var url = assetURL(el);
    if (!url) return false;
    try {
      var u = new URL(url, location.href);
      return u.origin === location.origin && u.pathname.indexOf(ASSETS) === 0;
    } catch (e) {
      return false;
    }
  }

  function appDrawn() {
    var root = document.getElementById("root");
    return !!(root && root.childNodes.length);
  }

  function lastReloadAt() {
    try {
      return Number(sessionStorage.getItem(KEY)) || 0;
    } catch (e) {
      return 0;
    }
  }

  function recordReload(now) {
    try {
      sessionStorage.setItem(KEY, String(now));
    } catch (e) {
      // Without storage the reload still happens, once per page.
    }
  }

  function showMessage() {
    var root = document.getElementById("root");
    if (!root) {
      document.addEventListener("DOMContentLoaded", showMessage);
      return;
    }
    var box = document.createElement("div");
    box.setAttribute("role", "alert");
    box.style.cssText =
      "font-family:system-ui,sans-serif;max-width:28rem;margin:20vh auto;padding:0 16px;text-align:center;color:#333;background:#fff";
    var p = document.createElement("p");
    p.textContent = "The portal is being updated. Reload in a moment.";
    var b = document.createElement("button");
    b.type = "button";
    b.textContent = "Reload";
    b.style.cssText = "font:inherit;padding:6px 16px;cursor:pointer";
    b.onclick = function () {
      location.reload();
    };
    box.appendChild(p);
    box.appendChild(b);
    root.replaceChildren(box);
  }

  window.addEventListener(
    "error",
    function (ev) {
      if (handled || !isBuildAsset(ev.target) || appDrawn()) return;
      handled = true;
      var now = Date.now();
      var last = lastReloadAt();
      if (last && now - last < WINDOW_MS) {
        showMessage();
        return;
      }
      recordReload(now);
      location.reload();
    },
    true,
  );
})();
