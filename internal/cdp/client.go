package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	maxBufferedEvents = 500
	maxReadBytes      = 100 << 20
	cdpWriteTimeout   = 5 * time.Second
)

type Client struct {
	conn         *websocket.Conn
	endpoint     string
	next         atomic.Int64
	writeGate    chan struct{}
	pendingMu    sync.Mutex
	pending      map[int64]chan pendingResponse
	readCancel   context.CancelFunc
	eventMu      sync.Mutex
	eventBuf     []Event
	eventNotify  chan struct{}
	handlerMu    sync.Mutex
	handlers     map[string]map[uint64]EventHandler
	nextHandler  atomic.Uint64
	terminalErr  error
	terminalDone bool
	terminalWait chan struct{}
	terminalOnce sync.Once
}

// EventHandler observes an unsolicited CDP event. Returning true consumes the
// event before it reaches the shared event queue. This lets daemon internals
// react to protocol events without polluting unrelated event consumers.
type EventHandler func(Event) bool

type CommandClient interface {
	Call(ctx context.Context, method string, params any, result any) error
	CallSession(ctx context.Context, sessionID, method string, params any, result any) error
}

type Event struct {
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	ID        int64           `json:"id,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

type pendingResponse struct {
	resp response
	err  error
}

func Dial(ctx context.Context, endpoint string) (*Client, error) {
	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("connect websocket: %w", err)
	}
	conn.SetReadLimit(maxReadBytes)
	readCtx, cancel := context.WithCancel(context.Background())
	client := &Client{
		conn:         conn,
		endpoint:     endpoint,
		writeGate:    make(chan struct{}, 1),
		pending:      map[int64]chan pendingResponse{},
		readCancel:   cancel,
		eventNotify:  make(chan struct{}),
		handlers:     map[string]map[uint64]EventHandler{},
		terminalWait: make(chan struct{}),
	}
	client.writeGate <- struct{}{}
	go client.readLoop(readCtx)
	return client, nil
}

func (c *Client) Endpoint() string {
	return c.endpoint
}

// SubscribeEvent registers one handler for a CDP method and returns an
// idempotent unsubscribe function. Handlers run on the read-loop goroutine and
// must return quickly; a handler that needs I/O should start its own bounded
// goroutine.
func (c *Client) SubscribeEvent(method string, handler EventHandler) func() {
	method = strings.TrimSpace(method)
	if method == "" || handler == nil {
		return func() {}
	}
	id := c.nextHandler.Add(1)
	c.handlerMu.Lock()
	if c.handlers[method] == nil {
		c.handlers[method] = map[uint64]EventHandler{}
	}
	c.handlers[method][id] = handler
	c.handlerMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.handlerMu.Lock()
			defer c.handlerMu.Unlock()
			if handlers := c.handlers[method]; handlers != nil {
				delete(handlers, id)
				if len(handlers) == 0 {
					delete(c.handlers, method)
				}
			}
		})
	}
}

// Done is closed when the shared browser transport becomes unusable. Daemon
// owners can use it to stop serving a stale connection and reconnect instead
// of waiting for an unrelated heartbeat or command to notice.
func (c *Client) Done() <-chan struct{} {
	return c.terminalWait
}

// Err returns the terminal transport error, if the client has stopped.
func (c *Client) Err() error {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	return c.terminalErr
}

func (c *Client) Close(status websocket.StatusCode, reason string) error {
	if c.readCancel != nil {
		c.readCancel()
	}
	return c.conn.Close(status, reason)
}

func (c *Client) CloseNormal() error {
	return c.Close(websocket.StatusNormalClosure, "done")
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	return c.CallSession(ctx, "", method, params, result)
}

func (c *Client) CallSession(ctx context.Context, sessionID, method string, params any, result any) error {
	id := c.next.Add(1)
	respCh := make(chan pendingResponse, 1)
	if err := c.addPending(id, respCh); err != nil {
		return err
	}
	req := struct {
		ID        int64  `json:"id"`
		SessionID string `json:"sessionId,omitempty"`
		Method    string `json:"method"`
		Params    any    `json:"params"`
	}{
		ID:        id,
		SessionID: sessionID,
		Method:    method,
		Params:    params,
	}
	if err := c.acquireWrite(ctx); err != nil {
		c.removePending(id)
		return fmt.Errorf("write cdp command %s: %w", method, err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		c.releaseWrite()
		c.removePending(id)
		return fmt.Errorf("write cdp command %s: %w", method, ctxErr)
	}
	// A caller timeout must not tear down the shared transport merely because
	// the command response was slow. A separate write bound still prevents a
	// blocked websocket write from holding the gate forever; nhooyr closes the
	// connection when this bound expires, which lets the daemon reconnect.
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), cdpWriteTimeout)
	err := wsjson.Write(writeCtx, c.conn, req)
	writeTimedOut := errors.Is(writeCtx.Err(), context.DeadlineExceeded)
	cancelWrite()
	c.releaseWrite()
	if err != nil {
		if writeTimedOut {
			_ = c.conn.CloseNow()
		}
		c.removePending(id)
		return fmt.Errorf("write cdp command %s: %w", method, err)
	}

	var pending pendingResponse
	select {
	case pending = <-respCh:
	case <-ctx.Done():
		c.removePending(id)
		return fmt.Errorf("read cdp response %s: %w", method, ctx.Err())
	}
	if pending.err != nil {
		return fmt.Errorf("read cdp response %s: %w", method, pending.err)
	}
	resp := pending.resp
	if resp.Error != nil {
		return fmt.Errorf("cdp %s failed: %s (%d)", method, resp.Error.Message, resp.Error.Code)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Result, result); err != nil {
		return fmt.Errorf("decode cdp response %s: %w", method, err)
	}
	return nil
}

func (c *Client) acquireWrite(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.terminalWait:
		if err := c.Err(); err != nil {
			return err
		}
		return fmt.Errorf("cdp connection is closed")
	case <-c.writeGate:
		if err := c.Err(); err != nil {
			c.releaseWrite()
			return err
		}
		return nil
	}
}

func (c *Client) releaseWrite() {
	c.writeGate <- struct{}{}
}

func (c *Client) addPending(id int64, ch chan pendingResponse) error {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.terminalDone {
		if c.terminalErr != nil {
			return c.terminalErr
		}
		return fmt.Errorf("cdp connection is closed")
	}
	c.pending[id] = ch
	return nil
}

func (c *Client) removePending(id int64) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	delete(c.pending, id)
}

func (c *Client) readLoop(ctx context.Context) {
	for {
		var resp response
		if err := wsjson.Read(ctx, c.conn, &resp); err != nil {
			c.failPending(err)
			return
		}
		if event, ok := resp.event(); ok {
			if c.dispatchEvent(event) {
				continue
			}
			c.bufferEvent(event)
			continue
		}
		if resp.ID == 0 {
			continue
		}
		c.pendingMu.Lock()
		ch := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.pendingMu.Unlock()
		if ch != nil {
			ch <- pendingResponse{resp: resp}
		}
	}
}

func (c *Client) dispatchEvent(event Event) bool {
	c.handlerMu.Lock()
	registered := c.handlers[event.Method]
	handlers := make([]EventHandler, 0, len(registered))
	for _, handler := range registered {
		handlers = append(handlers, handler)
	}
	c.handlerMu.Unlock()
	consumed := false
	for _, handler := range handlers {
		if invokeEventHandler(handler, event) {
			consumed = true
		}
	}
	return consumed
}

func invokeEventHandler(handler EventHandler, event Event) (consumed bool) {
	defer func() {
		if recover() != nil {
			consumed = false
		}
	}()
	return handler(event)
}

func (c *Client) failPending(err error) {
	c.pendingMu.Lock()
	c.terminalDone = true
	c.terminalErr = err
	c.terminalOnce.Do(func() {
		close(c.terminalWait)
	})
	for id, ch := range c.pending {
		ch <- pendingResponse{err: err}
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
}

func (c *Client) DrainEvents() []Event {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	if len(c.eventBuf) == 0 {
		return nil
	}
	events := append([]Event(nil), c.eventBuf...)
	c.eventBuf = nil
	return events
}

func (c *Client) DrainSessionEvents(sessionID string) []Event {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	if len(c.eventBuf) == 0 {
		return nil
	}
	matched := make([]Event, 0)
	retained := make([]Event, 0, len(c.eventBuf))
	for _, event := range c.eventBuf {
		if event.SessionID == sessionID {
			matched = append(matched, event)
			continue
		}
		retained = append(retained, event)
	}
	c.eventBuf = retained
	return matched
}

func (c *Client) ReadEvent(ctx context.Context) (Event, error) {
	return c.readEvent(ctx, "", false)
}

func (c *Client) ReadSessionEvent(ctx context.Context, sessionID string) (Event, error) {
	return c.readEvent(ctx, sessionID, true)
}

func (c *Client) readEvent(ctx context.Context, sessionID string, exactSession bool) (Event, error) {
	for {
		event, ok, notify := c.popEventOrWait(sessionID, exactSession)
		if ok {
			return event, nil
		}
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-notify:
		}
	}
}

func (c *Client) popEventOrWait(sessionID string, exactSession bool) (Event, bool, <-chan struct{}) {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	if c.eventNotify == nil {
		c.eventNotify = make(chan struct{})
	}
	index := -1
	if !exactSession && len(c.eventBuf) > 0 {
		index = 0
	} else if exactSession {
		for i, event := range c.eventBuf {
			if event.SessionID == sessionID {
				index = i
				break
			}
		}
	}
	if index < 0 {
		return Event{}, false, c.eventNotify
	}
	event := c.eventBuf[index]
	copy(c.eventBuf[index:], c.eventBuf[index+1:])
	c.eventBuf[len(c.eventBuf)-1] = Event{}
	c.eventBuf = c.eventBuf[:len(c.eventBuf)-1]
	return event, true, nil
}

func (c *Client) bufferEvent(event Event) {
	if event.Method == "" {
		return
	}
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	if c.eventNotify == nil {
		c.eventNotify = make(chan struct{})
	}
	c.eventBuf = append(c.eventBuf, event)
	if len(c.eventBuf) > maxBufferedEvents {
		c.eventBuf = c.eventBuf[len(c.eventBuf)-maxBufferedEvents:]
	}
	close(c.eventNotify)
	c.eventNotify = make(chan struct{})
}

func (r response) event() (Event, bool) {
	if r.Method == "" {
		return Event{}, false
	}
	return Event{
		SessionID: r.SessionID,
		Method:    r.Method,
		Params:    r.Params,
	}, true
}
