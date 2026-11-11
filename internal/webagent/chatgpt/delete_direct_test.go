package chatgpt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestDeleteDirectObservedEnvelope(t *testing.T) {
	store := newReadTestStore(t, time.Now().UTC())
	called := 0
	config := DeleteConfig{Store: store, HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		called++
		if request.Method != http.MethodDelete || request.URL.Path != "/backend-api/conversation/id/owned-123" || request.Header.Get("Authorization") != "Bearer test-only" {
			t.Fatalf("unexpected direct delete request: method=%s path=%s auth_present=%t", request.Method, request.URL.Path, request.Header.Get("Authorization") != "")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":true}`))}, nil
	})}}
	result := DeleteConversation(context.Background(), config, "owned-123")
	if !result.OK || result.State != webagent.StateTerminal || result.Action == nil || result.Action.Dispatch != webagent.DispatchPerformed || called != 1 {
		t.Fatalf("direct delete result: ok=%t state=%s action=%+v calls=%d error=%+v", result.OK, result.State, result.Action, called, result.Error)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(store.templatePath), "delete-pending"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("pending journal: entries=%d err=%v", len(entries), err)
	}
	info, err := entries[0].Info()
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("pending file permission: %v %v", info, err)
	}
}

func TestDeleteDirectTransportUncertaintyNeverRetries(t *testing.T) {
	store := newReadTestStore(t, time.Now().UTC())
	called := 0
	config := DeleteConfig{Store: store, HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called++
		return nil, errors.New("test transport timeout")
	})}}
	result := DeleteConversation(context.Background(), config, "owned-123")
	if result.OK || result.Action == nil || result.Action.Dispatch != webagent.DispatchUnknown || result.Action.RetrySafe || result.Error == nil || result.Error.RetrySafe || called != 1 {
		t.Fatalf("uncertain delete result: ok=%t action=%+v calls=%d error=%+v", result.OK, result.Action, called, result.Error)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteDirectRejectsUnprovedEnvelope(t *testing.T) {
	store := newReadTestStore(t, time.Now().UTC())
	config := DeleteConfig{Store: store, HTTPClient: fixedHTTPClient(http.StatusOK, `{"success":false}`)}
	result := DeleteConversation(context.Background(), config, "owned-123")
	if result.OK || result.Action == nil || result.Action.Dispatch != webagent.DispatchPerformed || result.Error == nil || result.Error.RetrySafe {
		t.Fatalf("unproved delete result: ok=%t action=%+v error=%+v", result.OK, result.Action, result.Error)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteDirectNonOKHasTypedNonRetryableFailure(t *testing.T) {
	store := newReadTestStore(t, time.Now().UTC())
	config := DeleteConfig{Store: store, HTTPClient: fixedHTTPClient(http.StatusForbidden, `{}`)}
	result := DeleteConversation(context.Background(), config, "owned-123")
	if result.OK || result.Action == nil || result.Action.Dispatch != webagent.DispatchPerformed ||
		result.Error == nil || result.Error.Code != "chatgpt_delete_http_failed" || result.Error.RetrySafe {
		t.Fatalf("non-OK delete result: ok=%t action=%+v error=%+v", result.OK, result.Action, result.Error)
	}
	data, ok := result.Data.(DeleteData)
	if !ok || data.Metadata["status_code"] != http.StatusForbidden {
		t.Fatalf("non-OK status evidence: %+v", result.Data)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
}
