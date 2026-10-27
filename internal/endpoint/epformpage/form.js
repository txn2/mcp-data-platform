/*
 * A form the platform renders from the spec its endpoint answers (#2177,
 * #2331).
 *
 * The page reads its configuration from #form-config, fetches the form's spec
 * from /endpoints/{name} (passing on the query string it was opened with, so
 * form_spec(req) reads a link's token or id from req["query"]), renders the
 * fields, and posts the submission back with the form token the spec came
 * with. A viewer token, the one the page was signed for from the browser's
 * session or the one in the form_viewer parameter, is sent as X-Form-Viewer;
 * it and the embed marker are never passed to the form. A bot check the
 * endpoint names is answered with /botcheck/solver.js before each request it
 * applies to. Framed, the page posts its height to its parent, which the
 * embed snippet sizes the frame by.
 */
(function () {
  "use strict";

  var cfg = JSON.parse(document.getElementById("form-config").textContent);
  var app = document.getElementById("app");
  var params = new URLSearchParams(window.location.search);
  var viewer = cfg.viewer || params.get(cfg.viewer_param);
  params.delete(cfg.viewer_param);
  params.delete(cfg.embed_param);
  var theme = params.get(cfg.theme_param);
  params.delete(cfg.theme_param);
  if (theme === "dark" || theme === "light") {
    document.documentElement.setAttribute("data-theme", theme);
  }

  var state = { spec: null, token: "", tokenField: "", honeypot: "" };

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    Object.keys(attrs || {}).forEach(function (k) {
      if (k === "text") { node.textContent = attrs[k]; } else if (attrs[k] !== false && attrs[k] != null) { node.setAttribute(k, attrs[k] === true ? "" : attrs[k]); }
    });
    (children || []).forEach(function (c) { if (c) { node.appendChild(c); } });
    return node;
  }

  function status(text, kind) {
    app.replaceChildren(el("p", { "class": "status " + (kind || ""), role: kind === "err" ? "alert" : "status", text: text }));
  }

  // The height is posted to any parent: it says nothing but how tall the
  // page is, and the snippet checks the message came from this origin.
  function postHeight() {
    if (window.parent === window) { return; }
    window.parent.postMessage({ type: cfg.height_message, endpoint: cfg.endpoint, height: document.documentElement.scrollHeight }, "*");
  }
  if (typeof ResizeObserver === "function") {
    new ResizeObserver(postHeight).observe(document.body);
  }
  window.addEventListener("load", postHeight);

  // signIn asks the person to sign in, in a tab of its own: a frame on
  // another site is sent no session, and the sign-in brings that tab back to
  // this form.
  function signIn() {
    app.replaceChildren(el("p", { "class": "status" }, [
      document.createTextNode("Sign in to fill in this form. "),
      el("a", { href: cfg.sign_in, target: "_blank", rel: "noopener", text: "Sign in" })
    ]));
  }

  function checked(method) {
    if (!cfg.bot_check) { return false; }
    var m = cfg.checked_methods || [];
    return m.length === 0 || m.indexOf(method) >= 0 || (method === "HEAD" && m.indexOf("GET") >= 0);
  }

  function headers(method) {
    var h = {};
    if (viewer) { h[cfg.viewer_header] = viewer; }
    if (!checked(method)) { return Promise.resolve(h); }
    var container = document.getElementById("check");
    return window.PlatformBotCheck.answer(cfg.endpoint, { container: container }).then(function (answer) {
      if (answer) { h[window.PlatformBotCheck.header] = answer; }
      return h;
    });
  }

  function message(body, fallback) {
    if (body && typeof body.error === "string") { return body.error; }
    if (body && typeof body.detail === "string") { return body.detail; }
    return fallback;
  }

  function readJSON(r) {
    return r.json().catch(function () { return {}; }).then(function (body) { return { status: r.status, ok: r.ok, body: body }; });
  }

  function load() {
    var query = params.toString();
    var url = cfg.url + (query ? "?" + query : "");
    return headers("GET")
      .then(function (h) { return fetch(url, { headers: h, credentials: "omit" }); })
      .then(readJSON)
      .then(function (res) {
        if (!res.ok || !res.body.spec) { status(message(res.body, "The form could not be loaded."), "err"); return; }
        state.spec = res.body.spec;
        state.token = res.body.form_token;
        state.tokenField = res.body.token_field;
        state.honeypot = res.body.honeypot_field;
        render();
      })
      .catch(function (e) { status("The form could not be loaded: " + e.message, "err"); });
  }

  function control(f, value) {
    var id = "f-" + f.name;
    var common = { id: id, name: f.name, required: f.required, "aria-describedby": f.help ? id + "-help" : null };
    switch (f.type) {
      case "long_text":
        var ta = el("textarea", Object.assign({ maxlength: f.max_length }, common));
        ta.value = value == null ? "" : String(value);
        return ta;
      case "single_choice":
        var sel = el("select", common, [el("option", { value: "", text: "Choose…" })]);
        (f.choices || []).forEach(function (c) { sel.appendChild(el("option", { value: c.value, text: c.label || c.value })); });
        sel.value = value == null ? "" : String(value);
        return sel;
      case "multiple_choice":
        var chosen = Array.isArray(value) ? value.map(String) : [];
        return el("div", { role: "group" }, (f.choices || []).map(function (c, i) {
          return el("label", { "class": "option", "for": id + "-" + i }, [
            el("input", { type: "checkbox", id: id + "-" + i, name: f.name, value: c.value, checked: chosen.indexOf(c.value) >= 0 }),
            document.createTextNode(c.label || c.value)
          ]);
        }));
      case "checkbox":
        return el("label", { "class": "option", "for": id }, [
          el("input", { type: "checkbox", id: id, name: f.name, value: "true", required: f.required, checked: value === true || value === "true" }),
          document.createTextNode(f.label || f.name)
        ]);
      case "number":
        var n = el("input", Object.assign({ type: "number", step: "any", min: f.min, max: f.max }, common));
        n.value = value == null ? "" : String(value);
        return n;
      case "file":
        // accept only guides the picker: the platform judges each file's
        // type from its bytes.
        return el("input", Object.assign({ type: "file", multiple: (f.max_files || 1) > 1, accept: (f.accept || []).join(",") || null }, common));
      default:
        var type = { email: "email", date: "date" }[f.type] || "text";
        var input = el("input", Object.assign({ type: type, maxlength: f.max_length, autocomplete: f.sensitive ? "off" : null }, common));
        input.value = value == null ? "" : String(value);
        return input;
    }
  }

  function field(f) {
    var prefill = (state.spec.prefill || {})[f.name];
    if (f.type === "hidden") {
      var hidden = el("input", { type: "hidden", name: f.name });
      hidden.value = prefill == null ? "" : String(prefill);
      return hidden;
    }
    var label = f.label || f.name;
    var wrap;
    if (f.type === "multiple_choice") {
      wrap = el("fieldset", { "class": "field" }, [el("legend", { text: label }, [f.required ? el("span", { "class": "req", "aria-hidden": "true", text: "*" }) : null])]);
    } else if (f.type === "checkbox") {
      wrap = el("div", { "class": "field" });
    } else {
      wrap = el("div", { "class": "field" }, [el("label", { "for": "f-" + f.name, text: label }, [f.required ? el("span", { "class": "req", "aria-hidden": "true", text: "*" }) : null])]);
    }
    wrap.appendChild(control(f, prefill));
    if (f.help) { wrap.appendChild(el("p", { "class": "help", id: "f-" + f.name + "-help", text: f.help })); }
    wrap.appendChild(el("p", { "class": "error", id: "f-" + f.name + "-error", role: "alert", hidden: true }));
    return wrap;
  }

  function render() {
    var form = el("form", { novalidate: true });
    state.spec.fields.forEach(function (f) { form.appendChild(field(f)); });
    var trap = el("div", { "class": "trap", "aria-hidden": "true" }, [
      el("label", { "for": "f-trap", text: "Leave this empty" }),
      el("input", { type: "text", id: "f-trap", name: state.honeypot, tabindex: "-1", autocomplete: "off" })
    ]);
    form.appendChild(trap);
    form.appendChild(el("p", { "class": "error", id: "form-error", role: "alert", hidden: true }));
    // The page is sent from the button and from Enter in a one-line field,
    // never by the form's own submission: inside an asset the page inherits
    // the asset's sandbox, which has no allow-forms, and a browser stops a
    // form's submission there before its submit event fires.
    var button = el("button", { type: "button", text: "Submit" });
    button.addEventListener("click", function () { submit(form); });
    form.addEventListener("keydown", function (e) {
      if (e.key === "Enter" && e.target.tagName === "INPUT" && e.target.type !== "checkbox") {
        e.preventDefault();
        submit(form);
      }
    });
    form.addEventListener("submit", function (e) { e.preventDefault(); });
    form.appendChild(button);
    app.replaceChildren(form);
  }

  function values(form) {
    var out = {};
    state.spec.fields.forEach(function (f) {
      var inputs = form.querySelectorAll('[name="' + f.name + '"]');
      if (f.type === "file") {
        return;
      }
      if (f.type === "multiple_choice") {
        out[f.name] = Array.prototype.filter.call(inputs, function (i) { return i.checked; }).map(function (i) { return i.value; });
      } else if (f.type === "checkbox") {
        out[f.name] = inputs[0] && inputs[0].checked ? "true" : "false";
      } else if (inputs[0]) {
        out[f.name] = inputs[0].value;
      }
    });
    out[state.tokenField] = state.token;
    var trap = form.querySelector('[name="' + state.honeypot + '"]');
    out[state.honeypot] = trap ? trap.value : "";
    return out;
  }

  function showErrors(form, errors) {
    form.querySelectorAll(".error").forEach(function (p) { p.hidden = true; p.textContent = ""; });
    var first = null;
    Object.keys(errors || {}).forEach(function (name) {
      var p = document.getElementById("f-" + name + "-error") || document.getElementById("form-error");
      p.textContent = (p.id === "form-error" ? name + ": " : "") + errors[name];
      p.hidden = false;
      first = first || p;
    });
    if (first) { first.scrollIntoView({ block: "center" }); }
  }

  function formError(form, text) {
    var p = document.getElementById("form-error");
    p.textContent = text;
    p.hidden = false;
  }

  // body is the submission: JSON, or multipart/form-data when the form has
  // a file field, which carries each chosen file under its field's name.
  function body(form) {
    var plain = values(form);
    var files = state.spec.fields.filter(function (f) { return f.type === "file"; });
    if (files.length === 0) { return { json: JSON.stringify(plain) }; }
    var data = new FormData();
    Object.keys(plain).forEach(function (name) {
      var v = plain[name];
      (Array.isArray(v) ? v : [v]).forEach(function (item) { data.append(name, item); });
    });
    files.forEach(function (f) {
      var input = form.querySelector('[name="' + f.name + '"]');
      Array.prototype.forEach.call((input && input.files) || [], function (file) { data.append(f.name, file, file.name); });
    });
    return { multipart: data };
  }

  function submit(form) {
    var button = form.querySelector("button");
    if (button.disabled) { return; }
    button.disabled = true;
    var sent = body(form);
    headers("POST")
      .then(function (h) {
        // A multipart body's Content-Type, with its boundary, is the browser's.
        if (sent.json) { h["Content-Type"] = "application/json"; }
        return fetch(cfg.url, { method: "POST", headers: h, body: sent.json || sent.multipart, credentials: "omit" });
      })
      .then(readJSON)
      .then(function (res) {
        if (res.status === 422) { showErrors(form, res.body.errors); return; }
        if (!res.ok) { formError(form, message(res.body, "The form could not be sent.")); return; }
        if (res.body.redirect) { window.location.assign(res.body.redirect); return; }
        status(res.body.confirmation || "Thank you. Your response was received.", "ok");
      })
      .catch(function (e) { formError(form, "The form could not be sent: " + e.message); })
      .finally(function () { button.disabled = false; });
  }

  if (cfg.refused) {
    status(cfg.refused, "err");
  } else if (cfg.sign_in) {
    signIn();
  } else {
    load();
  }
})();
