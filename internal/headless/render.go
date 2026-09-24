// Package headless draws a document in a headless Chrome the platform reaches
// over the DevTools protocol, with the browser cut off from the network: every
// request the document makes is answered by the platform itself.
//
// A document saved to the platform is written by a person and runs inside the
// network perimeter when it is drawn here, so what it can reach is the whole
// question. Three layers stand between a document and the network, and each
// was shown to hold on its own against a renderer with the browser's own
// private-network protection switched off (see the integration tests):
//
//   - Every render runs in its own browser context whose proxy is a dead
//     address. Traffic the platform does not answer -- a WebSocket, anything a
//     worker the platform could not arm tries -- goes to that proxy and fails.
//     Loopback is not exempt, so a document cannot reach the renderer's own
//     DevTools port or the platform beside it.
//   - The page is armed before it navigates: every request it, its frames and
//     its workers make is paused and answered here. Files the page needs are
//     served from the platform's own copies; a public URL is fetched by the
//     platform through a guarded client that refuses internal addresses; the
//     rest are refused.
//   - The WebSocket, WebTransport and WebRTC constructors are removed from
//     every page, frame and worker before its first instruction runs.
//
// Network.setBlockedURLs is deliberately not used: it does not stop a
// WebSocket, and a block that does not block is worse than none.
package headless

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// deadProxy is the proxy every render's browser context uses. Nothing
	// listens there; traffic the platform does not answer fails at it.
	deadProxy = "http://127.0.0.1:1"
	// noBypass removes the browser's implicit loopback exemption, so the dead
	// proxy covers the renderer's own ports and the platform beside it too.
	noBypass = "<-loopback>"

	// originSuffix is the reserved name every render's origin sits under.
	// .invalid never resolves (RFC 2606), so a request that somehow escaped
	// interception could not reach a real host by this name.
	originSuffix = ".render.invalid"

	// publicFetchTimeout bounds one public resource a document names.
	publicFetchTimeout = 15 * time.Second
	// maxPublicBody bounds one public resource a document names.
	maxPublicBody = 16 << 20
	// disposeTimeout bounds tearing a render down after its own deadline has
	// already passed.
	disposeTimeout = 5 * time.Second
)

// scrub removes the constructors that open a connection the platform cannot
// intercept. It is defined non-configurable so the document cannot put them
// back.
const scrub = `(()=>{for(const k of ["WebSocket","WebSocketStream","WebTransport","RTCPeerConnection","webkitRTCPeerConnection","RTCDataChannel"]){try{Object.defineProperty(globalThis,k,{value:undefined,configurable:false,writable:false})}catch(e){}}})();`

// step is one command issued while arming a session.
type step struct {
	method string
	params any
}

// fetchAll pauses every request a session makes.
var fetchAll = step{"Fetch.enable", map[string]any{"patterns": []map[string]string{{"urlPattern": "*"}}}}

// scrubOnNewDocument runs the scrub in every document a frame loads, before
// the document's own scripts.
var scrubOnNewDocument = step{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": scrub}}

// pauseChildren holds every frame and worker a session starts until it is
// armed.
var pauseChildren = step{"Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}}

// freezeAnimations stops the animation timeline of every document a frame
// loads. A tile is one picture, and a page whose infinite CSS animation is
// painted in software never goes idle: it held the renderer at its CPU limit
// for hours and starved its DevTools endpoint (#1868). The animations are put
// where a picture wants them before the capture, by snapAnimations.
var freezeAnimations = []step{
	{"Animation.enable", nil},
	{"Animation.setPlaybackRate", map[string]any{"playbackRate": 0}},
}

// snapAnimations is evaluated in every frame before the capture: an animation
// that ends is shown ended, and one that never does is removed, which shows
// the element as it is styled without it. With the timeline stopped each stays
// where it is put.
const snapAnimations = `(()=>{for(const a of document.getAnimations()){try{const t=a.effect&&a.effect.getComputedTiming();if(t&&Number.isFinite(t.endTime)){a.currentTime=t.endTime}else{a.cancel()}}catch(e){}}})()`

// snapWorld names the isolated world snapAnimations runs in, which reaches a
// sandboxed frame's animations the page's own world cannot.
const snapWorld = "tile-snap"

// File is one same-origin file a page loads.
type File struct {
	Body        []byte
	ContentType string
}

// Page is one document to draw and everything it may load from its own
// origin.
type Page struct {
	// Document is served as the page itself.
	Document []byte
	// Files answers a request for a path on the page's own origin. A path it
	// does not know is answered 404, which is what the document would see
	// from a real server.
	Files func(path string) (File, bool)
	// Ready is a JavaScript expression evaluating to a Promise that resolves
	// to "" once the page is drawn, or to the reason it cannot be.
	Ready string
	// Width and Height are the page's viewport in CSS pixels.
	Width, Height int
	// Scale is image pixels per CSS pixel: 400x300 at 2 is an 800x600 PNG,
	// 1280x960 at 0.625 is one too. Above 1 the page is painted at that pixel
	// density; below 1 it is painted at full size and reduced with a
	// resampling filter, because a browser painting a page at a fraction of
	// its size draws text a few pixels tall and illegible (#1789). Zero is 1.
	Scale float64
	// Dark emulates a reader who prefers a dark color scheme.
	Dark bool
}

// Renderer draws pages in the headless Chrome at endpoint.
type Renderer struct {
	endpoint string
	public   *http.Client

	// withoutProxy and withoutScrub switch a layer off. They exist so the
	// integration tests can show each remaining layer holds on its own; the
	// zero value, which is all New returns, has every layer on.
	withoutProxy bool
	withoutScrub bool
}

// New returns a renderer for the browser at endpoint, the renderer's DevTools
// address (http://127.0.0.1:9222 or ws://127.0.0.1:9222). public fetches the
// public URLs a document names and must refuse internal addresses; nil refuses
// every URL outside the page's own origin.
func New(endpoint string, public *http.Client) *Renderer {
	return &Renderer{endpoint: endpoint, public: public}
}

// Ping reports whether the renderer answers, without drawing anything: a
// caller holding work checks it once rather than claiming work it cannot do.
func (r *Renderer) Ping(ctx context.Context) error {
	_, err := browserSocket(ctx, r.endpoint)
	return err
}

// render is one Render call: its connection, the sessions it owns, and the
// origin its page is served at.
type render struct {
	r    *Renderer
	page Page
	c    *conn
	host string

	mu       sync.Mutex
	sessions map[string]bool
	// frames are the sessions that hold documents -- the page and each frame
	// it started in its own process -- as against workers.
	frames []string
	armErr error
}

// Render draws p and returns the screenshot as a PNG.
//
// It returns when the page reports itself ready or ctx ends. A page whose
// script never lets it settle is abandoned at ctx's deadline, and its browser
// context -- with every frame and worker in it -- is disposed either way. A
// renderer that does not confirm the disposal fails the render as
// unavailable, so a caller does not draw the next page in a browser still
// busy with this one (#1868).
func (r *Renderer) Render(ctx context.Context, p Page) (png []byte, err error) {
	rs := &render{r: r, page: p, host: originHost(), sessions: map[string]bool{}}
	c, err := dial(ctx, r.endpoint, rs.onEvent)
	if err != nil {
		return nil, err
	}
	defer c.close()
	rs.c = c

	var bc struct {
		BrowserContextID string `json:"browserContextId"`
	}
	contextParams := map[string]any{"disposeOnDetach": true, "proxyServer": deadProxy, "proxyBypassList": noBypass}
	if r.withoutProxy {
		contextParams = map[string]any{"disposeOnDetach": true}
	}
	if err := c.call(ctx, "", "Target.createBrowserContext", contextParams, &bc); err != nil {
		return nil, err
	}
	defer func() { png, err = afterTeardown(png, err, rs.dispose(ctx, bc.BrowserContextID)) }()

	session, err := rs.openPage(ctx, bc.BrowserContextID)
	if err != nil {
		return nil, err
	}
	if err := rs.navigate(ctx, session); err != nil {
		return nil, err
	}
	if err := rs.awaitReady(ctx, session); err != nil {
		return nil, err
	}
	if err := rs.failedArming(); err != nil {
		return nil, err
	}
	rs.snap(ctx)
	return rs.screenshot(ctx, session)
}

// originHost is a fresh, unguessable host for one render's page, so no two
// renders -- and no document naming another -- share an origin.
func originHost() string {
	return "t-" + strings.ToLower(rand.Text()) + originSuffix
}

// openPage creates the page in its browser context and arms it before it
// loads anything: interception, the constructor scrub, the viewport and color
// scheme, and the pause that lets each child be armed the same way.
func (rs *render) openPage(ctx context.Context, browserContext string) (string, error) {
	var tgt struct {
		TargetID string `json:"targetId"`
	}
	if err := rs.c.call(ctx, "", "Target.createTarget", map[string]any{
		"url": "about:blank", "browserContextId": browserContext,
	}, &tgt); err != nil {
		return "", err
	}
	var att struct {
		SessionID string `json:"sessionId"`
	}
	if err := rs.c.call(ctx, "", "Target.attachToTarget", map[string]any{
		"targetId": tgt.TargetID, "flatten": true,
	}, &att); err != nil {
		return "", err
	}
	rs.own(att.SessionID)
	rs.addFrame(att.SessionID)

	scheme := "light"
	if rs.page.Dark {
		scheme = "dark"
	}
	steps := []step{
		fetchAll,
		{"Page.enable", nil},
		rs.scrubStep(scrubOnNewDocument),
		freezeAnimations[0],
		freezeAnimations[1],
		{"Emulation.setDeviceMetricsOverride", map[string]any{
			"width": rs.page.Width, "height": rs.page.Height, "deviceScaleFactor": paintDensity(rs.page.Scale), "mobile": false,
		}},
		// A document that honors reduced motion draws its settled state
		// without being frozen into it.
		{"Emulation.setEmulatedMedia", map[string]any{
			"features": []map[string]string{
				{"name": "prefers-color-scheme", "value": scheme},
				{"name": "prefers-reduced-motion", "value": "reduce"},
			},
		}},
		// A document taller than the viewport would otherwise put a scrollbar
		// in the picture, the frame's and the page's alike.
		{"Emulation.setScrollbarsHidden", map[string]any{"hidden": true}},
		pauseChildren,
	}
	for _, s := range steps {
		if err := rs.c.call(ctx, att.SessionID, s.method, s.params, nil); err != nil {
			return "", err
		}
	}
	return att.SessionID, nil
}

// navigate loads the page at its origin.
func (rs *render) navigate(ctx context.Context, session string) error {
	var nav struct {
		ErrorText string `json:"errorText"`
	}
	if err := rs.c.call(ctx, session, "Page.navigate", map[string]any{"url": "http://" + rs.host + "/"}, &nav); err != nil {
		return err
	}
	if nav.ErrorText != "" {
		return fmt.Errorf("headless: the page did not load: %s", nav.ErrorText)
	}
	return nil
}

// awaitReady waits for the page's own readiness promise.
func (rs *render) awaitReady(ctx context.Context, session string) error {
	var res struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	err := rs.c.call(ctx, session, "Runtime.evaluate", map[string]any{
		"expression": rs.page.Ready, "awaitPromise": true, "returnByValue": true,
	}, &res)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New("headless: the document did not finish drawing before the deadline")
		}
		return err
	}
	if d := res.ExceptionDetails; d != nil {
		msg := d.Text
		if d.Exception != nil && d.Exception.Description != "" {
			msg = d.Exception.Description
		}
		return fmt.Errorf("headless: the page's readiness check threw: %s", msg)
	}
	if reason, _ := res.Result.Value.(string); reason != "" {
		return fmt.Errorf("headless: %s", reason)
	}
	return nil
}

// screenshot captures the viewport as it was painted, and reduces it to the
// page's scale when that is below the density it was painted at.
func (rs *render) screenshot(ctx context.Context, session string) ([]byte, error) {
	var shot struct {
		Data string `json:"data"`
	}
	if err := rs.c.call(ctx, session, "Page.captureScreenshot", map[string]any{
		"format": "png",
		"clip": map[string]any{
			"x": 0, "y": 0, "width": rs.page.Width, "height": rs.page.Height, "scale": 1,
		},
	}, &shot); err != nil {
		return nil, err
	}
	png, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return nil, fmt.Errorf("headless: decoding the screenshot: %w", err)
	}
	if len(png) == 0 {
		return nil, errors.New("headless: the renderer returned an empty screenshot")
	}
	if rs.page.Scale <= 0 || rs.page.Scale >= 1 {
		return png, nil
	}
	return reduce(png, rs.page.Width, rs.page.Height, rs.page.Scale)
}

// snap evaluates snapAnimations in every frame of every document session, in
// an isolated world of its own so a sandboxed frame is reached too. A frame
// it cannot reach is drawn as its frozen timeline left it, which is a picture
// rather than a failure.
func (rs *render) snap(ctx context.Context) {
	for _, session := range rs.frameSessions() {
		var tree struct {
			FrameTree frameTree `json:"frameTree"`
		}
		if rs.c.call(ctx, session, "Page.getFrameTree", nil, &tree) != nil {
			continue
		}
		for _, id := range tree.FrameTree.ids() {
			var world struct {
				ExecutionContextID int `json:"executionContextId"`
			}
			if rs.c.call(ctx, session, "Page.createIsolatedWorld", map[string]any{"frameId": id, "worldName": snapWorld}, &world) != nil {
				continue
			}
			_ = rs.c.call(ctx, session, "Runtime.evaluate", map[string]any{ //nolint:errcheck // a frame not snapped is drawn frozen
				"expression": snapAnimations, "contextId": world.ExecutionContextID,
			}, nil)
		}
	}
}

// frameTree is the part of Page.getFrameTree snap reads.
type frameTree struct {
	Frame struct {
		ID string `json:"id"`
	} `json:"frame"`
	ChildFrames []frameTree `json:"childFrames"`
}

// ids is every frame in the tree, the root first.
func (t frameTree) ids() []string {
	out := []string{t.Frame.ID}
	for _, c := range t.ChildFrames {
		out = append(out, c.ids()...)
	}
	return out
}

// afterTeardown is a render's result once its page has been disposed of: as it
// was when the renderer confirmed the disposal; the teardown's failure, and no
// picture, when a render that succeeded was not confirmed; and a failed
// render's own failure, which is what the caller acts on, with the teardown's
// kept beside it.
func afterTeardown(png []byte, err, teardown error) ([]byte, error) {
	switch {
	case teardown == nil:
		return png, err
	case err == nil:
		return nil, teardown
	default:
		return nil, fmt.Errorf("headless: %w; teardown: %v", err, teardown) //nolint:errorlint // the render's failure is the one to unwrap
	}
}

// dispose tears down the render's browser context, closing every page, frame
// and worker in it, and reports a renderer that did not confirm it: that
// renderer may still be busy with the page, and is unavailable until it
// answers again. The context is disposeOnDetach as well, so closing the
// connection is a second path to the same end. It runs after Render's own
// deadline may have passed, so it takes its own.
func (rs *render) dispose(ctx context.Context, browserContext string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), disposeTimeout)
	defer cancel()
	if err := rs.c.call(ctx, "", "Target.disposeBrowserContext", map[string]any{"browserContextId": browserContext}, nil); err != nil {
		return fmt.Errorf("headless: the renderer did not release the page: %w: %w", err, ErrUnavailable)
	}
	return nil
}

// scrubStep is s, or a no-op in its place when the scrub layer is switched off.
func (rs *render) scrubStep(s step) step {
	if rs.r.withoutScrub {
		return step{"Runtime.evaluate", map[string]any{"expression": "0"}}
	}
	return s
}

func (rs *render) own(session string) {
	rs.mu.Lock()
	rs.sessions[session] = true
	rs.mu.Unlock()
}

// addFrame records a session that holds a document.
func (rs *render) addFrame(session string) {
	rs.mu.Lock()
	rs.frames = append(rs.frames, session)
	rs.mu.Unlock()
}

// frameSessions is every session that holds a document.
func (rs *render) frameSessions() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]string(nil), rs.frames...)
}

func (rs *render) owns(session string) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.sessions[session]
}

func (rs *render) noteArmErr(err error) {
	rs.mu.Lock()
	if rs.armErr == nil {
		rs.armErr = err
	}
	rs.mu.Unlock()
}

func (rs *render) failedArming() error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.armErr
}

// onEvent answers the events of this render's own sessions and ignores every
// other.
func (rs *render) onEvent(m message) {
	if !rs.owns(m.SessionID) {
		return
	}
	switch m.Method {
	case "Fetch.requestPaused":
		rs.answer(m)
	case "Target.attachedToTarget":
		rs.arm(m)
	}
}

// arm prepares a frame or worker the page spawned before it runs: its own
// requests paused, the constructor scrub, and the same pause for anything it
// spawns. A child that cannot be armed is never released, so it never runs,
// and the render fails rather than draw a page whose parts ran unarmed.
func (rs *render) arm(m message) {
	var p struct {
		SessionID  string `json:"sessionId"`
		TargetInfo struct {
			Type string `json:"type"`
		} `json:"targetInfo"`
	}
	if json.Unmarshal(m.Params, &p) != nil || p.SessionID == "" {
		return
	}
	rs.own(p.SessionID)
	ctx, cancel := context.WithTimeout(context.Background(), disposeTimeout)
	defer cancel()

	required := []step{
		// A worker's requests are paused on the page that owns it; what it
		// needs of its own is the scrub, before its script runs.
		rs.scrubStep(step{"Runtime.evaluate", map[string]any{"expression": scrub}}),
	}
	document := p.TargetInfo.Type == "iframe" || p.TargetInfo.Type == "page"
	if document {
		required = []step{fetchAll, rs.scrubStep(scrubOnNewDocument), pauseChildren}
	}
	for _, s := range required {
		if err := rs.c.call(ctx, p.SessionID, s.method, s.params, nil); err != nil {
			rs.noteArmErr(fmt.Errorf("headless: a %s the page started could not be secured: %w", p.TargetInfo.Type, err))
			return
		}
	}
	if document {
		// A frame in its own process has its own timeline. One that cannot
		// be frozen is drawn as it runs, which is a picture, not a breach.
		for _, s := range freezeAnimations {
			_ = rs.c.call(ctx, p.SessionID, s.method, s.params, nil) //nolint:errcheck // best effort, as above
		}
		rs.addFrame(p.SessionID)
	}
	_ = rs.c.call(ctx, p.SessionID, "Runtime.runIfWaitingForDebugger", nil, nil) //nolint:errcheck // a child that is not waiting has nothing to release
}
