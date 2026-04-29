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
	conn     *websocket.Conn
	endpoint string
	next     atomic.Int64
	eventMu  sync.Mutex
	eventBuf []Event
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

func Dial(ctx context.Context, endpoint string) (*Client, error) {
	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("connect websocket: %w", err)
	}
	return &Client{conn: conn, endpoint: endpoint}, nil
}

func (c *Client) Endpoint() string {
	return c.endpoint
}

func (c *Client) Close(status websocket.StatusCode, reason string) error {
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
	if err := wsjson.Write(ctx, c.conn, req); err != nil {
		return fmt.Errorf("write cdp command %s: %w", method, err)
	}

	for {
		var resp response
		if err := wsjson.Read(ctx, c.conn, &resp); err != nil {
			return fmt.Errorf("read cdp response %s: %w", method, err)
		}
		if resp.ID != id {
			if event, ok := resp.event(); ok {
				c.bufferEvent(event)
			}
			continue
		}
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
		var resp response
		if err := wsjson.Read(ctx, c.conn, &resp); err != nil {
			return Event{}, err
		}
		if event, ok := resp.event(); ok {
			return event, nil
		}
	}
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
