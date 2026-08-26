// Package augloop owns the non-browser WebSocket transport used by the
// Microsoft 365 AugLoop provider. Keeping the socket mechanics here lets the
// provider policy receive an injected transport while browser discovery and
// CDP access remain behind the daemon/browserflow boundary.
package augloop

import (
	"context"
	"fmt"
	"net/http"

	"nhooyr.io/websocket"
)

type MessageType uint8

const (
	MessageText   MessageType = 1
	MessageBinary MessageType = 2
)

type StatusCode uint16

const StatusNormalClosure StatusCode = 1000

type Socket interface {
	Write(context.Context, MessageType, []byte) error
	Read(context.Context) (MessageType, []byte, error)
	Close(StatusCode, string) error
}

func Dial(ctx context.Context, rawURL, userAgent string) (Socket, error) {
	return DialWithOrigin(ctx, rawURL, userAgent, "https://m365.cloud.microsoft")
}

func DialWithOrigin(ctx context.Context, rawURL, userAgent, origin string) (Socket, error) {
	conn, _, err := websocket.Dial(ctx, rawURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin":     []string{origin},
			"User-Agent": []string{userAgent},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("connect Microsoft 365 AugLoop: %w", err)
	}
	conn.SetReadLimit(100 << 20)
	return &socket{conn: conn}, nil
}

type socket struct {
	conn *websocket.Conn
}

func (s *socket) Write(ctx context.Context, messageType MessageType, payload []byte) error {
	var wireType websocket.MessageType
	switch messageType {
	case MessageText:
		wireType = websocket.MessageText
	case MessageBinary:
		wireType = websocket.MessageBinary
	default:
		return fmt.Errorf("unsupported AugLoop WebSocket message type %d", messageType)
	}
	return s.conn.Write(ctx, wireType, payload)
}

func (s *socket) Read(ctx context.Context) (MessageType, []byte, error) {
	wireType, payload, err := s.conn.Read(ctx)
	if err != nil {
		return 0, nil, err
	}
	switch wireType {
	case websocket.MessageText:
		return MessageText, payload, nil
	case websocket.MessageBinary:
		return MessageBinary, payload, nil
	default:
		return 0, payload, fmt.Errorf("unsupported AugLoop WebSocket message type %d", wireType)
	}
}

func (s *socket) Close(code StatusCode, reason string) error {
	return s.conn.Close(websocket.StatusCode(code), reason)
}
