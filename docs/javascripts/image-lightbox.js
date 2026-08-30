/**
 * Click a screenshot to open it full screen.
 *
 * The screenshots on this site are 1440x900 captures rendered into a content
 * column a third that wide, so the detail a paragraph is pointing at -- a
 * status badge, one row of a table, the text in a drawer -- is unreadable at
 * rest. Clicking one opens it at the size it was captured.
 *
 * Only content images are wired up: an image inside a link already has a job,
 * and the theme's own chrome (logos, icons, the rail mark) is not content.
 * Material's instant navigation swaps the page without a reload, so binding
 * happens per document rather than once (see the document$ subscription at the
 * bottom, which mirrors the rail's own tracker in main.html).
 */
(function () {
  "use strict";

  var BOUND_ATTR = "data-lightbox-bound";
  var overlay = null;
  var lastFocus = null;

  function build() {
    if (overlay) return overlay;
    overlay = document.createElement("div");
    overlay.className = "img-lightbox";
    overlay.setAttribute("role", "dialog");
    overlay.setAttribute("aria-modal", "true");
    overlay.hidden = true;
    overlay.innerHTML =
      '<button class="img-lightbox__close" type="button" aria-label="close">&times;</button>' +
      '<figure class="img-lightbox__figure">' +
      '<img class="img-lightbox__img" alt="">' +
      '<figcaption class="img-lightbox__caption"></figcaption>' +
      "</figure>";
    document.body.appendChild(overlay);

    overlay.addEventListener("click", function (e) {
      // Anywhere except the image itself closes: the backdrop is the target
      // on a click beside the figure, and the close button is explicit.
      if (e.target.classList.contains("img-lightbox__img")) return;
      close();
    });
    return overlay;
  }

  function open(img) {
    var box = build();
    var full = box.querySelector(".img-lightbox__img");
    var cap = box.querySelector(".img-lightbox__caption");
    full.src = img.currentSrc || img.src;
    full.alt = img.alt || "";
    cap.textContent = img.alt || "";
    cap.hidden = !img.alt;
    lastFocus = document.activeElement;
    box.hidden = false;
    document.body.classList.add("img-lightbox-open");
    box.querySelector(".img-lightbox__close").focus();
  }

  function close() {
    if (!overlay || overlay.hidden) return;
    overlay.hidden = true;
    document.body.classList.remove("img-lightbox-open");
    // Release the source so a large capture is not held once dismissed.
    overlay.querySelector(".img-lightbox__img").removeAttribute("src");
    if (lastFocus && lastFocus.focus) lastFocus.focus();
    lastFocus = null;
  }

  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") close();
  });

  function bind() {
    var images = document.querySelectorAll(".md-content img, .md-typeset img");
    images.forEach(function (img) {
      if (img.hasAttribute(BOUND_ATTR)) return;
      if (img.closest("a")) return;
      img.setAttribute(BOUND_ATTR, "");
      img.classList.add("img-zoomable");
      img.setAttribute("role", "button");
      img.setAttribute("tabindex", "0");
      if (!img.title) img.title = "Click to view full size";
      img.addEventListener("click", function () {
        open(img);
      });
      img.addEventListener("keydown", function (e) {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          open(img);
        }
      });
    });
  }

  if (window.document$ && typeof window.document$.subscribe === "function") {
    window.document$.subscribe(function () {
      close();
      bind();
    });
  } else if (document.readyState !== "loading") {
    bind();
  } else {
    document.addEventListener("DOMContentLoaded", bind);
  }
})();
