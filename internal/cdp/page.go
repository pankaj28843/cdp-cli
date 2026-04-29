package cdp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"nhooyr.io/websocket"
)

type PageSession struct {
	client    CommandClient
	close     func(context.Context) error
	TargetID  string `json:"target_id"`
	SessionID string `json:"session_id"`
}

type RuntimeObject struct {
	Type        string          `json:"type"`
	Subtype     string          `json:"subtype,omitempty"`
	Description string          `json:"description,omitempty"`
	Value       json.RawMessage `json:"value,omitempty"`
}

type RuntimeException struct {
	Text string `json:"text,omitempty"`
}

type EvaluateResult struct {
	Object    RuntimeObject     `json:"object"`
	Exception *RuntimeException `json:"exception,omitempty"`
}

type ScreenshotOptions struct {
	Format   string
	Quality  int
	FullPage bool
}

type ScreenshotResult struct {
	Data   []byte
	Format string
}

type NavigationEntry struct {
	ID    int    `json:"id"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}

type NavigationHistory struct {
	CurrentIndex int               `json:"current_index"`
	Entries      []NavigationEntry `json:"entries"`
}

func CreateTarget(ctx context.Context, endpoint, rawURL string) (string, error) {
	client, err := Dial(ctx, endpoint)
	if err != nil {
		return "", err
	}
	defer client.CloseNormal()

	return CreateTargetWithClient(ctx, client, rawURL)
}

func CreateTargetWithClient(ctx context.Context, client CommandClient, rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	var result struct {
		TargetID string `json:"targetId"`
	}
	if err := client.Call(ctx, "Target.createTarget", map[string]any{"url": rawURL}, &result); err != nil {
		return "", err
	}
	if result.TargetID == "" {
		return "", fmt.Errorf("Target.createTarget returned an empty target id")
	}
	return result.TargetID, nil
}

func CloseTargetWithClient(ctx context.Context, client CommandClient, targetID string) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return fmt.Errorf("target id is required")
	}
	var result struct {
		Success bool `json:"success"`
	}
	if err := client.Call(ctx, "Target.closeTarget", map[string]any{"targetId": targetID}, &result); err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("Target.closeTarget returned success=false")
	}
	return nil
}

func ActivateTargetWithClient(ctx context.Context, client CommandClient, targetID string) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return fmt.Errorf("target id is required")
	}
	return client.Call(ctx, "Target.activateTarget", map[string]any{"targetId": targetID}, nil)
}

func AttachToTarget(ctx context.Context, endpoint, targetID string) (*PageSession, error) {
	client, err := Dial(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	session, err := AttachToTargetWithClient(ctx, client, targetID, func(context.Context) error {
		return client.CloseNormal()
	})
	if err != nil {
		_ = client.Close(websocket.StatusInternalError, "attach failed")
		return nil, err
	}
	return session, nil
}

func AttachToTargetWithClient(ctx context.Context, client CommandClient, targetID string, close func(context.Context) error) (*PageSession, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return nil, fmt.Errorf("target id is required")
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := client.Call(ctx, "Target.attachToTarget", map[string]any{
		"targetId": targetID,
		"flatten":  true,
	}, &result); err != nil {
		return nil, err
	}
	if result.SessionID == "" {
		return nil, fmt.Errorf("Target.attachToTarget returned an empty session id")
	}
	return &PageSession{client: client, close: close, TargetID: targetID, SessionID: result.SessionID}, nil
}

func (s *PageSession) Close(ctx context.Context) error {
	if s == nil || s.client == nil {
		return nil
	}
	if s.SessionID != "" {
		_ = s.client.Call(ctx, "Target.detachFromTarget", map[string]any{"sessionId": s.SessionID}, nil)
	}
	if s.close != nil {
		return s.close(ctx)
	}
	return nil
}

func (s *PageSession) Navigate(ctx context.Context, rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	var result struct {
		FrameID string `json:"frameId"`
	}
	if err := s.client.CallSession(ctx, s.SessionID, "Page.navigate", map[string]any{"url": rawURL}, &result); err != nil {
		return "", err
	}
	return result.FrameID, nil
}

func (s *PageSession) Reload(ctx context.Context, ignoreCache bool) error {
	return s.client.CallSession(ctx, s.SessionID, "Page.reload", map[string]any{"ignoreCache": ignoreCache}, nil)
}

func (s *PageSession) NavigationHistory(ctx context.Context) (NavigationHistory, error) {
	var result struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID    int    `json:"id"`
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"entries"`
	}
	if err := s.client.CallSession(ctx, s.SessionID, "Page.getNavigationHistory", map[string]any{}, &result); err != nil {
		return NavigationHistory{}, err
	}
	history := NavigationHistory{CurrentIndex: result.CurrentIndex, Entries: make([]NavigationEntry, 0, len(result.Entries))}
	for _, entry := range result.Entries {
		history.Entries = append(history.Entries, NavigationEntry{
			ID:    entry.ID,
			URL:   entry.URL,
			Title: entry.Title,
		})
	}
	return history, nil
}

func (s *PageSession) NavigateToHistoryEntry(ctx context.Context, entryID int) error {
	return s.client.CallSession(ctx, s.SessionID, "Page.navigateToHistoryEntry", map[string]any{"entryId": entryID}, nil)
}

func (s *PageSession) Evaluate(ctx context.Context, expression string, awaitPromise bool) (EvaluateResult, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return EvaluateResult{}, fmt.Errorf("expression is required")
	}
	var result struct {
		Result           RuntimeObject     `json:"result"`
		ExceptionDetails *RuntimeException `json:"exceptionDetails,omitempty"`
	}
	params := map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  awaitPromise,
	}
	if err := s.client.CallSession(ctx, s.SessionID, "Runtime.evaluate", params, &result); err != nil {
		return EvaluateResult{}, err
	}
	return EvaluateResult{Object: result.Result, Exception: result.ExceptionDetails}, nil
}

func (s *PageSession) Exec(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	method = strings.TrimSpace(method)
	if method == "" {
		return nil, fmt.Errorf("method is required")
	}
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	var raw json.RawMessage
	if err := s.client.CallSession(ctx, s.SessionID, method, params, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *PageSession) CaptureScreenshot(ctx context.Context, opts ScreenshotOptions) (ScreenshotResult, error) {
	format := strings.TrimSpace(opts.Format)
	if format == "" {
		format = "png"
	}
	params := map[string]any{
		"format": format,
	}
	if opts.Quality > 0 {
		params["quality"] = opts.Quality
	}
	if opts.FullPage {
		params["captureBeyondViewport"] = true
	}
	var result struct {
		Data string `json:"data"`
	}
	if err := s.client.CallSession(ctx, s.SessionID, "Page.captureScreenshot", params, &result); err != nil {
		return ScreenshotResult{}, err
	}
	if result.Data == "" {
		return ScreenshotResult{}, fmt.Errorf("Page.captureScreenshot returned empty data")
	}
	data, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		return ScreenshotResult{}, fmt.Errorf("decode screenshot data: %w", err)
	}
	return ScreenshotResult{Data: data, Format: format}, nil
}
