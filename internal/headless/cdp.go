package headless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// discoveryTimeout bounds the one HTTP request that finds the browser's
// DevTools socket. The renderer runs beside the platform, so a slow answer is a
// renderer that is not there.
const discoveryTimeout = 5 * time.Second

// maxMessageBytes bounds one protocol message. A screenshot comes back as one
// base64 message; a tile is far below this, and a message this large is a
// renderer misbehaving rather than a picture.
const maxMessageBytes = 32 << 20

// schemeHTTPS is the scheme of a renderer reached over TLS.
const schemeHTTPS = "https"

// ErrUnavailable marks a render that failed because the renderer could not be
// reached or went away mid-render, as opposed to a document that could not be
// drawn. A caller retries the first later and records the second.
var ErrUnavailable = errors.New("the renderer is not available")

// errConnClosed is returned for a command that was in flight, or issued, after
// the connection to the renderer ended.
var errConnClosed = fmt.Errorf("headless: the connection to the renderer closed: %w", ErrUnavailable)

// message is one DevTools protocol frame: a command, its response, or an
// event. Sessions are flattened, so a command to a page or a worker is the
// same frame carrying that target's session id.
type message struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *protocolError  `json:"error,omitempty"`
}

// protocolError is a command the browser refused.
type protocolError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *protocolError) Error() string {
	return fmt.Sprintf("devtools error %d: %s", e.Code, e.Message)
}

// conn is one DevTools protocol connection to a browser. Commands go out and
// their responses come back over one WebSocket; events are handed to onEvent,
// each on its own goroutine, so a handler that issues commands of its own
// cannot stall the reader it depends on.
type conn struct {
	ws      *websocket.Conn
	nextID  atomic.Int64
	onEvent func(message)
	// life bounds the reader. It outlives the context the connection was
	// dialed under, which is one command's, and ends with close.
	life context.Context
	end  context.CancelFunc

	mu      sync.Mutex
	pending map[int64]chan message
	closed  bool
	done    chan struct{}
}

// browserSocket finds the browser-level DevTools socket behind endpoint, which
// names the renderer by its HTTP address (http://127.0.0.1:9222) or by the
// ws:// form of it. The browser reports its socket under the address it
// listens on inside its own container; the host and port are replaced with the
// ones the platform reached it at, which is the only address that works from
// here.
func browserSocket(ctx context.Context, endpoint string) (string, error) {
	base, err := url.Parse(endpoint)
	if err != nil || base.Host == "" {
		return "", fmt.Errorf("headless: renderer address %q is not a URL with a host", endpoint)
	}
	secure := base.Scheme == schemeHTTPS || base.Scheme == "wss"
	scheme := "http"
	if secure {
		scheme = schemeHTTPS
	}
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+base.Host+"/json/version", http.NoBody)
	if err != nil {
		return "", fmt.Errorf("headless: building the discovery request: %w", err)
	}
	res, err := http.DefaultClient.Do(req) // #nosec G107 G704 -- the operator-configured renderer, which runs beside the platform by design
	if err != nil {
		return "", fmt.Errorf("headless: no renderer answers at %s: %w: %w", base.Host, err, ErrUnavailable)
	}
	defer res.Body.Close() //nolint:errcheck // read-only discovery response
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("headless: the renderer at %s answered discovery with HTTP %d: %w", base.Host, res.StatusCode, ErrUnavailable)
	}
	return socketFrom(res.Body, base.Host, secure)
}

// socketFrom reads the browser's socket out of its discovery document and
// points it at host, the address the platform reached the renderer at.
func socketFrom(doc io.Reader, host string, secure bool) (string, error) {
	var v struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(doc).Decode(&v); err != nil {
		return "", fmt.Errorf("headless: renderer at %s sent an unreadable discovery document: %w", host, err)
	}
	socket, err := url.Parse(v.WebSocketDebuggerURL)
	if err != nil || !strings.HasPrefix(socket.Scheme, "ws") {
		return "", fmt.Errorf("headless: renderer at %s reported no DevTools socket", host)
	}
	socket.Host = host
	if secure {
		socket.Scheme = "wss"
	}
	return socket.String(), nil
}

// dial opens a connection to the renderer behind endpoint.
func dial(ctx context.Context, endpoint string, onEvent func(message)) (*conn, error) {
	socket, err := browserSocket(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	ws, _, err := websocket.Dial(ctx, socket, nil) //nolint:bodyclose // coder/websocket closes the handshake response itself
	if err != nil {
		return nil, fmt.Errorf("headless: opening the DevTools socket: %w: %w", err, ErrUnavailable)
	}
	ws.SetReadLimit(maxMessageBytes)
	life, end := context.WithCancel(context.WithoutCancel(ctx))
	c := &conn{ws: ws, onEvent: onEvent, life: life, end: end, pending: map[int64]chan message{}, done: make(chan struct{})}
	go c.read()
	return c, nil
}

// read delivers responses to the command waiting on them and events to
// onEvent, until the socket ends; then every command still waiting fails.
func (c *conn) read() {
	defer c.shut()
	for {
		_, data, err := c.ws.Read(c.life)
		if err != nil {
			return
		}
		var m message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		if c.onEvent != nil && m.Method != "" {
			go c.onEvent(m)
		}
	}
}

// shut marks the connection closed and releases every waiting command.
func (c *conn) shut() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.done)
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
}

// close ends the connection. It is safe to call more than once.
func (c *conn) close() {
	_ = c.ws.Close(websocket.StatusNormalClosure, "") //nolint:errcheck // best-effort close
	c.end()
	c.shut()
}

// call issues one command on session ("" is the browser itself) and decodes
// its result into out, which may be nil. It returns when the browser answers,
// ctx ends, or the connection closes, whichever is first.
func (c *conn) call(ctx context.Context, session, method string, params, out any) error {
	raw := json.RawMessage("{}")
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("headless: encoding %s: %w", method, err)
		}
		raw = b
	}
	id := c.nextID.Add(1)
	ch := make(chan message, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errConnClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()

	frame, err := json.Marshal(message{ID: id, Method: method, Params: raw, SessionID: session})
	if err != nil {
		c.forget(id)
		return fmt.Errorf("headless: encoding %s: %w", method, err)
	}
	if err := c.ws.Write(ctx, websocket.MessageText, frame); err != nil {
		c.forget(id)
		return fmt.Errorf("headless: sending %s: %w", method, err)
	}
	return c.await(ctx, id, ch, method, out)
}

// await waits for the answer to command id and decodes its result into out.
func (c *conn) await(ctx context.Context, id int64, ch chan message, method string, out any) error {
	select {
	case m, ok := <-ch:
		if !ok {
			return errConnClosed
		}
		if m.Error != nil {
			return fmt.Errorf("headless: %s: %w", method, m.Error)
		}
		if out == nil || len(m.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(m.Result, out); err != nil {
			return fmt.Errorf("headless: decoding %s: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		c.forget(id)
		return fmt.Errorf("headless: %s: %w", method, ctx.Err())
	}
}

// forget stops waiting for a command's answer.
func (c *conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}
