package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const maxBufferedEvents = 500

type Client struct {
	conn         *websocket.Conn
	endpoint     string
	next         atomic.Int64
	writeMu      sync.Mutex
	pendingMu    sync.Mutex
	pending      map[int64]chan pendingResponse
	readCancel   context.CancelFunc
	eventMu      sync.Mutex
	eventBuf     []Event
	eventNotify  chan struct{}
	terminalErr  error
	terminalDone bool
}

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
	readCtx, cancel := context.WithCancel(context.Background())
	client := &Client{
		conn:        conn,
		endpoint:    endpoint,
		pending:     map[int64]chan pendingResponse{},
		readCancel:  cancel,
		eventNotify: make(chan struct{}, 1),
	}
	go client.readLoop(readCtx)
	return client, nil
}

func (c *Client) Endpoint() string {
	return c.endpoint
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
	c.writeMu.Lock()
	if err := wsjson.Write(ctx, c.conn, req); err != nil {
		c.writeMu.Unlock()
		c.removePending(id)
		return fmt.Errorf("write cdp command %s: %w", method, err)
	}
	c.writeMu.Unlock()

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

func (c *Client) failPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	c.terminalDone = true
	c.terminalErr = err
	for id, ch := range c.pending {
		ch <- pendingResponse{err: err}
		delete(c.pending, id)
	}
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

func (c *Client) ReadEvent(ctx context.Context) (Event, error) {
	for {
		if event, ok := c.popEvent(); ok {
			return event, nil
		}
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-c.eventNotify:
		}
	}
}

func (c *Client) popEvent() (Event, bool) {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	if len(c.eventBuf) == 0 {
		return Event{}, false
	}
	event := c.eventBuf[0]
	copy(c.eventBuf, c.eventBuf[1:])
	c.eventBuf = c.eventBuf[:len(c.eventBuf)-1]
	return event, true
}

func (c *Client) bufferEvent(event Event) {
	if event.Method == "" {
		return
	}
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	c.eventBuf = append(c.eventBuf, event)
	if len(c.eventBuf) > maxBufferedEvents {
		c.eventBuf = c.eventBuf[len(c.eventBuf)-maxBufferedEvents:]
	}
	select {
	case c.eventNotify <- struct{}{}:
	default:
	}
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
