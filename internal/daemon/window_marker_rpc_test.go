package daemon

import (
	"context"
	"encoding/json"
	"net"
	"testing"
)

func TestWindowMarkerRPCUsesTheDaemonController(t *testing.T) {
	transport := &fakeWindowMarkerTransport{}
	controller := newWindowMarkerController(t.TempDir(), "headed", transport)

	response := callWindowMarkerRPC(t, controller, RPCRequest{
		Method: RPCMethodEnableWindowMarker,
		Params: json.RawMessage(`{"name":"agent"}`),
	})
	if !response.OK {
		t.Fatalf("enable response = %+v", response)
	}
	var enabled WindowMarkerStatus
	if err := json.Unmarshal(response.Result, &enabled); err != nil {
		t.Fatalf("decode enable response: %v", err)
	}
	if enabled.State != "enabled" || enabled.Name != "agent" {
		t.Fatalf("enabled status = %+v", enabled)
	}

	response = callWindowMarkerRPC(t, controller, RPCRequest{Method: RPCMethodWindowMarkerStatus})
	if !response.OK {
		t.Fatalf("status response = %+v", response)
	}

	response = callWindowMarkerRPC(t, controller, RPCRequest{Method: RPCMethodDisableWindowMarker})
	if !response.OK {
		t.Fatalf("disable response = %+v", response)
	}
	var disabled WindowMarkerStatus
	if err := json.Unmarshal(response.Result, &disabled); err != nil {
		t.Fatalf("decode disable response: %v", err)
	}
	if disabled.State != "disabled" || disabled.Enabled {
		t.Fatalf("disabled status = %+v", disabled)
	}
}

func callWindowMarkerRPC(t *testing.T, controller *windowMarkerController, request RPCRequest) RPCResponse {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	go handleRPC(context.Background(), server, nil, holdOptions{}, nil, controller)
	if err := json.NewEncoder(client).Encode(request); err != nil {
		t.Fatalf("write RPC request: %v", err)
	}
	var response RPCResponse
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatalf("read RPC response: %v", err)
	}
	return response
}

var _ windowMarkerTransport = (*fakeWindowMarkerTransport)(nil)
