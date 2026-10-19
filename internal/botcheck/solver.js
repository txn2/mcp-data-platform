/*
 * The platform's bot check, for a page that calls a public endpoint (#2173).
 *
 *   <script src="/botcheck/solver.js"></script>
 *   const answer = await PlatformBotCheck.answer("order-lookup");
 *   fetch("/endpoints/order-lookup", {method: "POST", headers: {[PlatformBotCheck.header]: answer}, body});
 *
 * answer() fetches a challenge for the endpoint and solves it: a proof of work
 * is computed here, and a Turnstile or hCaptcha widget is rendered into
 * options.container (an element, required for those providers) and answered
 * by the person. Each answer is good for one request.
 */
(function () {
  "use strict";

  var K = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
  ];
  var W = new Uint32Array(64);

  function rotr(x, n) { return (x >>> n) | (x << (32 - n)); }

  // sha256 hashes an ASCII string (a challenge and a counter are ASCII) and
  // returns the digest's eight 32-bit words.
  function sha256(text) {
    var len = text.length;
    var blocks = ((len + 9 + 63) >> 6);
    var buf = new Uint8Array(blocks * 64);
    for (var i = 0; i < len; i++) { buf[i] = text.charCodeAt(i); }
    buf[len] = 0x80;
    var bitLen = len * 8;
    buf[buf.length - 4] = (bitLen >>> 24) & 0xff;
    buf[buf.length - 3] = (bitLen >>> 16) & 0xff;
    buf[buf.length - 2] = (bitLen >>> 8) & 0xff;
    buf[buf.length - 1] = bitLen & 0xff;
    var h0 = 0x6a09e667, h1 = 0xbb67ae85, h2 = 0x3c6ef372, h3 = 0xa54ff53a;
    var h4 = 0x510e527f, h5 = 0x9b05688c, h6 = 0x1f83d9ab, h7 = 0x5be0cd19;
    for (var b = 0; b < blocks; b++) {
      var o = b * 64;
      for (var t = 0; t < 16; t++) {
        W[t] = (buf[o + t * 4] << 24) | (buf[o + t * 4 + 1] << 16) | (buf[o + t * 4 + 2] << 8) | buf[o + t * 4 + 3];
      }
      for (t = 16; t < 64; t++) {
        var s0 = rotr(W[t - 15], 7) ^ rotr(W[t - 15], 18) ^ (W[t - 15] >>> 3);
        var s1 = rotr(W[t - 2], 17) ^ rotr(W[t - 2], 19) ^ (W[t - 2] >>> 10);
        W[t] = (W[t - 16] + s0 + W[t - 7] + s1) | 0;
      }
      var a = h0, c = h2, d = h3, e = h4, f = h5, g = h6, h = h7, bb = h1;
      for (t = 0; t < 64; t++) {
        var S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
        var ch = (e & f) ^ (~e & g);
        var t1 = (h + S1 + ch + K[t] + W[t]) | 0;
        var S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
        var maj = (a & bb) ^ (a & c) ^ (bb & c);
        var t2 = (S0 + maj) | 0;
        h = g; g = f; f = e; e = (d + t1) | 0; d = c; c = bb; bb = a; a = (t1 + t2) | 0;
      }
      h0 = (h0 + a) | 0; h1 = (h1 + bb) | 0; h2 = (h2 + c) | 0; h3 = (h3 + d) | 0;
      h4 = (h4 + e) | 0; h5 = (h5 + f) | 0; h6 = (h6 + g) | 0; h7 = (h7 + h) | 0;
    }
    return [h0, h1, h2, h3, h4, h5, h6, h7];
  }

  function leadingZeroBits(words) {
    var n = 0;
    for (var i = 0; i < words.length; i++) {
      var w = words[i] >>> 0;
      if (w !== 0) { return n + Math.clz32(w); }
      n += 32;
    }
    return n;
  }

  // work finds the counter whose hash of token~counter has the difficulty's
  // leading zero bits, yielding to the page every few thousand tries.
  function work(token, difficulty) {
    return new Promise(function (resolve) {
      var counter = 0;
      function step() {
        for (var i = 0; i < 5000; i++, counter++) {
          if (leadingZeroBits(sha256(token + "~" + counter)) >= difficulty) {
            resolve(token + "~" + counter);
            return;
          }
        }
        setTimeout(step, 0);
      }
      step();
    });
  }

  function loadScript(src) {
    return new Promise(function (resolve, reject) {
      var s = document.createElement("script");
      s.src = src; s.async = true; s.onload = resolve; s.onerror = reject;
      document.head.appendChild(s);
    });
  }

  function widget(challenge, options) {
    if (!options || !options.container) {
      return Promise.reject(new Error("this deployment's bot check is " + challenge.provider + ", which needs options.container to render its widget into"));
    }
    var turnstile = challenge.provider === "turnstile";
    var src = turnstile ? "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit" : "https://js.hcaptcha.com/1/api.js?render=explicit";
    return loadScript(src).then(function () {
      return new Promise(function (resolve, reject) {
        var api = turnstile ? window.turnstile : window.hcaptcha;
        api.render(options.container, {
          sitekey: challenge.site_key,
          callback: resolve,
          "error-callback": function () { reject(new Error("the " + challenge.provider + " check failed")); }
        });
      });
    });
  }

  function answer(endpoint, options) {
    var base = (options && options.base) || "";
    return fetch(base + "/botcheck/challenge?endpoint=" + encodeURIComponent(endpoint), { credentials: "omit" })
      .then(function (r) {
        if (!r.ok) { throw new Error("the bot check challenge could not be fetched: HTTP " + r.status); }
        return r.json();
      })
      .then(function (challenge) {
        if (challenge.provider === "none") { return ""; }
        if (challenge.provider === "pow") { return work(challenge.token, challenge.difficulty); }
        return widget(challenge, options);
      });
  }

  window.PlatformBotCheck = { answer: answer, header: "X-Bot-Check", sha256: sha256 };
})();
