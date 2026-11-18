package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestCallRuntimeCancellationInterruptsPendingRead(t *testing.T) {
	for _, withDeadline := range []bool{false, true} {
		name := "without_deadline"
		if withDeadline {
			name = "before_deadline"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(filepath.Dir(shortInternalStateDir(t)), "rpc.sock")
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithCancel(context.Background())
			if withDeadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
			}
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := CallRuntime(ctx, Runtime{SocketPath: path}, "fixture-session", RPCMethodReadSessionEvent, nil)
				done <- err
			}()
			conn, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			var request RPCRequest
			if err := json.NewDecoder(conn).Decode(&request); err != nil {
				t.Fatal(err)
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled RPC returned %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled RPC remained blocked waiting for an event")
			}
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				t.Fatalf("canceled RPC connection: %v, want EOF", err)
			}
		})
	}
}
