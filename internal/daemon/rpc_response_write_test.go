package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func TestWriteRPCResponseUnblocksWhenContextIsCanceled(t *testing.T) {
	conn := newBlockedRPCConn()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- writeRPCResponse(ctx, conn, RPCResponse{OK: true, Result: json.RawMessage(`{"large":"response"}`)})
	}()

	select {
	case <-conn.started:
	case <-time.After(time.Second):
		t.Fatal("RPC response writer did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("writeRPCResponse error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled RPC response writer remained blocked")
	}
}

func TestWriteRPCResponseHonorsBoundedDeadline(t *testing.T) {
	conn := newBlockedRPCConn()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- writeRPCResponse(ctx, conn, RPCResponse{OK: true})
	}()

	select {
	case <-conn.started:
	case <-time.After(time.Second):
		t.Fatal("RPC response writer did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("writeRPCResponse error = %v, want deadline", err)
		}
	case <-time.After(time.Second):
		t.Fatal("deadline-bounded RPC response writer remained blocked")
	}
}

func TestWriteRPCResponsePreservesCompleteJSONResponse(t *testing.T) {
	server, peer := net.Pipe()
	defer peer.Close()
	done := make(chan error, 1)
	go func() {
		done <- writeRPCResponse(context.Background(), server, RPCResponse{
			OK:     true,
			Result: json.RawMessage(`{"ok":true,"marker":"complete"}`),
		})
	}()

	var response RPCResponse
	if err := json.NewDecoder(peer).Decode(&response); err != nil {
		t.Fatalf("decode RPC response: %v", err)
	}
	if !response.OK || string(response.Result) != `{"ok":true,"marker":"complete"}` {
		t.Fatalf("RPC response = %+v, want complete JSON result", response)
	}
	if err := <-done; err != nil {
		t.Fatalf("writeRPCResponse returned error: %v", err)
	}
	_ = server.Close()
}

type blockedRPCConn struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func newBlockedRPCConn() *blockedRPCConn {
	return &blockedRPCConn{started: make(chan struct{}), closed: make(chan struct{})}
}

func (c *blockedRPCConn) Read([]byte) (int, error) {
	<-c.closed
	return 0, net.ErrClosed
}

func (c *blockedRPCConn) Write([]byte) (int, error) {
	c.startOnce.Do(func() { close(c.started) })
	<-c.closed
	return 0, net.ErrClosed
}

func (c *blockedRPCConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *blockedRPCConn) LocalAddr() net.Addr              { return rpcTestAddr("local") }
func (c *blockedRPCConn) RemoteAddr() net.Addr             { return rpcTestAddr("remote") }
func (c *blockedRPCConn) SetDeadline(time.Time) error      { return nil }
func (c *blockedRPCConn) SetReadDeadline(time.Time) error  { return nil }
func (c *blockedRPCConn) SetWriteDeadline(time.Time) error { return nil }

type rpcTestAddr string

func (a rpcTestAddr) Network() string { return "rpc-test" }
func (a rpcTestAddr) String() string  { return string(a) }
