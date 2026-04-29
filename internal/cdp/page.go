package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nhooyr.io/websocket"
)

type PageSession struct {
	client    *Client
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

func CreateTarget(ctx context.Context, endpoint, rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	client, err := Dial(ctx, endpoint)
	if err != nil {
		return "", err
	}
	defer client.Close(websocket.StatusNormalClosure, "done")

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

func AttachToTarget(ctx context.Context, endpoint, targetID string) (*PageSession, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return nil, fmt.Errorf("target id is required")
	}
	client, err := Dial(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := client.Call(ctx, "Target.attachToTarget", map[string]any{
		"targetId": targetID,
		"flatten":  true,
	}, &result); err != nil {
		_ = client.Close(websocket.StatusInternalError, "attach failed")
		return nil, err
	}
	if result.SessionID == "" {
		_ = client.Close(websocket.StatusInternalError, "attach returned empty session")
		return nil, fmt.Errorf("Target.attachToTarget returned an empty session id")
	}
	return &PageSession{client: client, TargetID: targetID, SessionID: result.SessionID}, nil
}

func (s *PageSession) Close(ctx context.Context) error {
	if s == nil || s.client == nil {
		return nil
	}
	if s.SessionID != "" {
		_ = s.client.Call(ctx, "Target.detachFromTarget", map[string]any{"sessionId": s.SessionID}, nil)
	}
	return s.client.Close(websocket.StatusNormalClosure, "done")
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
