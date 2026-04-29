package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type Client struct {
	conn *websocket.Conn
	next atomic.Int64
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	ID     int64           `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

func Dial(ctx context.Context, endpoint string) (*Client, error) {
	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("connect websocket: %w", err)
	}
	return &Client{conn: conn}, nil
}

func (c *Client) Close(status websocket.StatusCode, reason string) error {
	return c.conn.Close(status, reason)
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	id := c.next.Add(1)
	req := struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{
		ID:     id,
		Method: method,
		Params: params,
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
