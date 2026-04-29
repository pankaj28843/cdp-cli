package cdp

import (
	"context"
	"encoding/json"

	"nhooyr.io/websocket"
)

func Exec(ctx context.Context, endpoint, method string, params json.RawMessage) (json.RawMessage, error) {
	client, err := Dial(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer client.Close(websocket.StatusNormalClosure, "done")

	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	var raw json.RawMessage
	if err := client.Call(ctx, method, params, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}
