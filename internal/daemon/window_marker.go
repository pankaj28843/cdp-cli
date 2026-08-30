package daemon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

const (
	windowMarkerSchemaVersion = "cdp-window-marker/v1"
	windowMarkerStateFile     = "window-marker.json"
	windowMarkerWorldName     = "cdp_cli_window_marker"
	windowMarkerSetupTimeout  = 5 * time.Second
)

var windowMarkerPalette = [...]string{
	"#d11149", "#c2185b", "#7b1fa2", "#512da8", "#303f9f",
	"#1976d2", "#0277bd", "#00838f", "#00695c", "#2e7d32",
	"#e65100", "#d84315", "#5d4037", "#455a64",
}

// WindowMarkerConfig is the metadata needed to render a marker. It contains
// no page content, browser credentials, or target payloads.
type WindowMarkerConfig struct {
	SchemaVersion string `json:"schema_version"`
	Enabled       bool   `json:"enabled"`
	Name          string `json:"name"`
	Color         string `json:"color"`
	HostID        string `json:"host_id"`
}

// WindowMarkerStatus is safe to return to a CLI consumer. The randomized host
// identity is deliberately reduced to a presence bit rather than exposed.
type WindowMarkerStatus struct {
	SchemaVersion      string `json:"schema_version"`
	State              string `json:"state"`
	Enabled            bool   `json:"enabled"`
	Name               string `json:"name,omitempty"`
	Color              string `json:"color,omitempty"`
	HostIDPresent      bool   `json:"host_id_present"`
	ActiveSessionCount int    `json:"active_session_count"`
	SetupFailureCount  int    `json:"setup_failure_count,omitempty"`
	StatePath          string `json:"state_path"`
	Warning            string `json:"warning,omitempty"`
}

func windowMarkerStatePath(stateDir, browserMode string) string {
	return filepath.Join(stateDir, runtimeModeName(browserMode), windowMarkerStateFile)
}

func newWindowMarkerConfig(name string) (WindowMarkerConfig, error) {
	name = strings.TrimSpace(name)
	if err := validateWindowMarkerName(name); err != nil {
		return WindowMarkerConfig{}, err
	}
	hostBytes := make([]byte, 8)
	if _, err := rand.Read(hostBytes); err != nil {
		return WindowMarkerConfig{}, fmt.Errorf("generate window marker identity: %w", err)
	}
	return WindowMarkerConfig{
		SchemaVersion: windowMarkerSchemaVersion,
		Enabled:       true,
		Name:          name,
		Color:         deriveWindowMarkerColor(name),
		HostID:        "_cdp_marker_" + hex.EncodeToString(hostBytes),
	}, nil
}

func validateWindowMarkerName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("window marker name is required")
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("window marker name must be valid UTF-8")
	}
	if len([]rune(name)) > 128 {
		return fmt.Errorf("window marker name must be at most 128 characters")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("window marker name must not contain control characters")
		}
	}
	return nil
}

func deriveWindowMarkerColor(name string) string {
	digest := sha256.Sum256([]byte(name))
	value := uint16(digest[0])<<8 | uint16(digest[1])
	return windowMarkerPalette[int(value)%len(windowMarkerPalette)]
}

func markerJSONLiteral(value string) string {
	b, _ := json.Marshal(value)
	return string(b)
}

func buildWindowMarkerScript(config WindowMarkerConfig) string {
	name := markerJSONLiteral(config.Name)
	color := markerJSONLiteral(config.Color)
	host := markerJSONLiteral(config.HostID)
	return "(() => {" +
		"  const NAME=" + name + ", COLOR=" + color + ", HOST_ID=" + host + ";" +
		"  const DISABLE_ATTRIBUTE = 'data-cdp-cli-window-marker-disabled';" +
		"  const PREFIX = '\\uD83E\\uDD16 ' + NAME + ' \\u2014 ';" +
		"  function isDisabled(){ try { return !!document.documentElement && document.documentElement.getAttribute(DISABLE_ATTRIBUTE) === HOST_ID; } catch (_) { return false; } }" +
		"  function fixTitle(){ try { if (isDisabled()) return; const title = document.title || ''; if (title.indexOf(PREFIX) !== 0) document.title = PREFIX + title; } catch (_) {} }" +
		"  function draw(){" +
		"    try {" +
		"      if (isDisabled()) return false;" +
		"      if (!document.documentElement) return false;" +
		"      if (document.getElementById(HOST_ID)) return true;" +
		"      const host = document.createElement('div');" +
		"      host.id = HOST_ID;" +
		"      host.style.cssText = 'position:fixed;top:0;left:0;right:0;bottom:0;z-index:2147483647;pointer-events:none;margin:0;padding:0;border:0;background:transparent';" +
		"      const html = '<div style=\"position:fixed;top:0;left:0;right:0;bottom:0;border:6px solid ' + COLOR + ';box-sizing:border-box;pointer-events:none\"></div>'" +
		" + '<div style=\"position:fixed;top:0;left:0;background:' + COLOR + ';color:#fff;font:600 12px/1.45 system-ui,-apple-system,Segoe UI,sans-serif;padding:3px 9px;border-bottom-right-radius:6px;pointer-events:none;white-space:nowrap\">\\uD83E\\uDD16 ' + NAME + '</div>';" +
		"      if (host.attachShadow) host.attachShadow({mode:'closed'}).innerHTML = html; else host.innerHTML = html;" +
		"      document.documentElement.appendChild(host);" +
		"      return true;" +
		"    } catch (_) { return false; }" +
		"  }" +
		"  function ensureDraw(){ if (draw()) return; try { const observer = new MutationObserver(() => { if (draw()) observer.disconnect(); }); observer.observe(document, {childList:true, subtree:true}); } catch (_) {} }" +
		"  function watch(){" +
		"    fixTitle();" +
		"    try {" +
		"      const title = document.querySelector('title'); if (title) new MutationObserver(fixTitle).observe(title, {childList:true, characterData:true, subtree:true});" +
		"      if (document.head) new MutationObserver(fixTitle).observe(document.head, {childList:true, subtree:true});" +
		"      if (document.documentElement) new MutationObserver(() => { if (!document.getElementById(HOST_ID)) draw(); }).observe(document.documentElement, {childList:true});" +
		"    } catch (_) {}" +
		"  }" +
		"  ensureDraw(); if (document.head || document.readyState !== 'loading') watch(); else document.addEventListener('DOMContentLoaded', watch);" +
		"})();"
}

func buildWindowMarkerRemovalScript(config WindowMarkerConfig) string {
	name := markerJSONLiteral(config.Name)
	host := markerJSONLiteral(config.HostID)
	return "(() => {" +
		"  const NAME=" + name + ", HOST_ID=" + host + ";" +
		"  const DISABLE_ATTRIBUTE = 'data-cdp-cli-window-marker-disabled';" +
		"  const PREFIX = '\\uD83E\\uDD16 ' + NAME + ' \\u2014 ';" +
		"  try { if (document.documentElement) document.documentElement.setAttribute(DISABLE_ATTRIBUTE, HOST_ID); const host = document.getElementById(HOST_ID); if (host) host.remove(); const title = document.title || ''; if (title.indexOf(PREFIX) === 0) document.title = title.slice(PREFIX.length); } catch (_) {}" +
		"})();"
}

func loadWindowMarkerConfig(path string) (WindowMarkerConfig, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return WindowMarkerConfig{SchemaVersion: windowMarkerSchemaVersion}, false, nil
	}
	if err != nil {
		return WindowMarkerConfig{}, false, fmt.Errorf("read window marker state: %w", err)
	}
	var config WindowMarkerConfig
	if err := json.Unmarshal(b, &config); err != nil {
		return WindowMarkerConfig{}, false, fmt.Errorf("decode window marker state: %w", err)
	}
	if config.SchemaVersion != windowMarkerSchemaVersion {
		return WindowMarkerConfig{}, false, fmt.Errorf("unsupported window marker state schema %q", config.SchemaVersion)
	}
	if config.Enabled {
		if err := validateWindowMarkerName(config.Name); err != nil {
			return WindowMarkerConfig{}, false, err
		}
		if config.Color != deriveWindowMarkerColor(config.Name) {
			return WindowMarkerConfig{}, false, fmt.Errorf("window marker color does not match its name")
		}
		if !strings.HasPrefix(config.HostID, "_cdp_marker_") {
			return WindowMarkerConfig{}, false, fmt.Errorf("window marker host identity is invalid")
		}
	}
	return config, true, nil
}

func saveWindowMarkerConfig(path string, config WindowMarkerConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create window marker state directory: %w", err)
	}
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode window marker state: %w", err)
	}
	b = append(b, '\n')
	if err := writeFileAtomic(path, b, 0o600); err != nil {
		return fmt.Errorf("write window marker state: %w", err)
	}
	return nil
}

type windowMarkerTransport interface {
	cdp.CommandClient
	SubscribeEvent(string, cdp.EventHandler) func()
}

type windowMarkerSession struct {
	TargetID  string
	SessionID string
	ScriptID  string
}

type windowMarkerController struct {
	opMu          sync.Mutex
	mu            sync.Mutex
	client        windowMarkerTransport
	statePath     string
	config        WindowMarkerConfig
	active        bool
	setupContext  context.Context
	cancelSetup   context.CancelFunc
	unsubAttached func()
	unsubDetached func()
	sessions      map[string]windowMarkerSession
	setupFailures int
	warning       string
}

func newWindowMarkerController(stateDir, browserMode string, client windowMarkerTransport) *windowMarkerController {
	path := windowMarkerStatePath(stateDir, browserMode)
	controller := &windowMarkerController{
		client:    client,
		statePath: path,
		config:    WindowMarkerConfig{SchemaVersion: windowMarkerSchemaVersion},
		sessions:  map[string]windowMarkerSession{},
	}
	config, ok, err := loadWindowMarkerConfig(path)
	if err != nil {
		controller.warning = err.Error()
		return controller
	}
	if ok {
		controller.config = config
	}
	return controller
}

func (m *windowMarkerController) rehydrate(ctx context.Context) error {
	m.mu.Lock()
	enabled := m.config.Enabled
	m.mu.Unlock()
	if !enabled {
		return nil
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.activateLocked(ctx)
}

func (m *windowMarkerController) Enable(ctx context.Context, name string) (WindowMarkerStatus, error) {
	config, err := newWindowMarkerConfig(name)
	if err != nil {
		return m.Status(), err
	}
	m.cancelInFlightSetup()
	m.opMu.Lock()
	defer m.opMu.Unlock()
	deactivateErr := m.deactivateLocked(ctx)
	m.mu.Lock()
	m.config = config
	m.warning = ""
	m.mu.Unlock()
	if err := saveWindowMarkerConfig(m.statePath, config); err != nil {
		return m.Status(), err
	}
	if err := m.activateLocked(ctx); err != nil {
		return m.Status(), err
	}
	if deactivateErr != nil {
		return m.Status(), fmt.Errorf("remove previous window marker: %w", deactivateErr)
	}
	return m.Status(), nil
}

func (m *windowMarkerController) Disable(ctx context.Context) (WindowMarkerStatus, error) {
	m.cancelInFlightSetup()
	m.opMu.Lock()
	defer m.opMu.Unlock()
	deactivateErr := m.deactivateLocked(ctx)
	m.mu.Lock()
	config := WindowMarkerConfig{SchemaVersion: windowMarkerSchemaVersion}
	m.config = config
	m.warning = ""
	m.mu.Unlock()
	if err := saveWindowMarkerConfig(m.statePath, config); err != nil {
		return m.Status(), err
	}
	if deactivateErr != nil {
		return m.Status(), deactivateErr
	}
	return m.Status(), nil
}

func (m *windowMarkerController) Status() WindowMarkerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := "disabled"
	if m.config.Enabled {
		state = "configured"
		if m.active {
			state = "enabled"
		}
	}
	if m.warning != "" {
		state = "error"
	}
	return WindowMarkerStatus{
		SchemaVersion:      windowMarkerSchemaVersion,
		State:              state,
		Enabled:            m.config.Enabled,
		Name:               m.config.Name,
		Color:              m.config.Color,
		HostIDPresent:      m.config.HostID != "",
		ActiveSessionCount: len(m.sessions),
		SetupFailureCount:  m.setupFailures,
		StatePath:          m.statePath,
		Warning:            m.warning,
	}
}

func (m *windowMarkerController) activateLocked(ctx context.Context) error {
	m.mu.Lock()
	if m.active {
		m.mu.Unlock()
		return nil
	}
	setupContext, cancelSetup := context.WithCancel(context.WithoutCancel(ctx))
	m.active = true
	m.setupContext = setupContext
	m.cancelSetup = cancelSetup
	m.mu.Unlock()

	attached := m.client.SubscribeEvent("Target.attachedToTarget", m.handleAttached)
	detached := m.client.SubscribeEvent("Target.detachedFromTarget", m.handleDetached)
	m.mu.Lock()
	m.unsubAttached = attached
	m.unsubDetached = detached
	m.mu.Unlock()
	if err := m.client.Call(ctx, "Target.setAutoAttach", map[string]any{
		"autoAttach":             true,
		"waitForDebuggerOnStart": false,
		"flatten":                true,
	}, nil); err != nil {
		attached()
		detached()
		m.mu.Lock()
		m.active = false
		m.setupContext = nil
		m.cancelSetup = nil
		m.unsubAttached = nil
		m.unsubDetached = nil
		m.mu.Unlock()
		cancelSetup()
		return fmt.Errorf("enable window marker target supervision: %w", err)
	}
	return nil
}

func (m *windowMarkerController) deactivateLocked(ctx context.Context) error {
	m.mu.Lock()
	if !m.active && len(m.sessions) == 0 {
		m.mu.Unlock()
		return nil
	}
	m.active = false
	cancelSetup := m.cancelSetup
	m.setupContext = nil
	m.cancelSetup = nil
	unsubAttached := m.unsubAttached
	unsubDetached := m.unsubDetached
	m.unsubAttached = nil
	m.unsubDetached = nil
	config := m.config
	sessions := make([]windowMarkerSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = map[string]windowMarkerSession{}
	m.mu.Unlock()
	if cancelSetup != nil {
		cancelSetup()
	}
	var firstErr error
	for _, session := range sessions {
		removeCtx, cancel := context.WithTimeout(ctx, windowMarkerSetupTimeout)
		if session.ScriptID != "" {
			if err := m.client.CallSession(removeCtx, session.SessionID, "Page.removeScriptToEvaluateOnNewDocument", map[string]any{
				"identifier": session.ScriptID,
			}, nil); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("remove window marker navigation script from target %s: %w", session.TargetID, err)
			}
		}
		if err := m.client.CallSession(removeCtx, session.SessionID, "Runtime.evaluate", map[string]any{"expression": buildWindowMarkerRemovalScript(config)}, nil); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove window marker from target %s: %w", session.TargetID, err)
		}
		cancel()
		if err := m.client.Call(ctx, "Target.detachFromTarget", map[string]any{"sessionId": session.SessionID}, nil); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("detach window marker target %s: %w", session.TargetID, err)
		}
	}
	if err := m.client.Call(ctx, "Target.setAutoAttach", map[string]any{
		"autoAttach":             false,
		"waitForDebuggerOnStart": false,
		"flatten":                true,
	}, nil); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("disable window marker target supervision: %w", err)
	}
	if unsubAttached != nil {
		unsubAttached()
	}
	if unsubDetached != nil {
		unsubDetached()
	}
	return firstErr
}

func (m *windowMarkerController) handleAttached(event cdp.Event) bool {
	var params struct {
		SessionID  string         `json:"sessionId"`
		TargetInfo cdp.TargetInfo `json:"targetInfo"`
	}
	if err := json.Unmarshal(event.Params, &params); err != nil || params.SessionID == "" || params.TargetInfo.TargetID == "" {
		return true
	}
	if params.TargetInfo.Type != "page" {
		return true
	}
	m.mu.Lock()
	if !m.active || !m.config.Enabled {
		m.mu.Unlock()
		return true
	}
	if _, ok := m.sessions[params.SessionID]; ok {
		m.mu.Unlock()
		return true
	}
	m.sessions[params.SessionID] = windowMarkerSession{TargetID: params.TargetInfo.TargetID, SessionID: params.SessionID}
	setupContext := m.setupContext
	m.mu.Unlock()
	go m.setupSession(setupContext, params.SessionID, params.TargetInfo.TargetID)
	return true
}

func (m *windowMarkerController) handleDetached(event cdp.Event) bool {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(event.Params, &params); err == nil && params.SessionID != "" {
		m.mu.Lock()
		delete(m.sessions, params.SessionID)
		m.mu.Unlock()
	}
	return true
}

func (m *windowMarkerController) setupSession(lifecycle context.Context, sessionID, targetID string) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	ctx, cancel := context.WithTimeout(lifecycle, windowMarkerSetupTimeout)
	defer cancel()
	m.mu.Lock()
	if !m.active || !m.config.Enabled {
		m.mu.Unlock()
		return
	}
	config := m.config
	m.mu.Unlock()
	err := m.client.CallSession(ctx, sessionID, "Page.enable", map[string]any{}, nil)
	var scriptResult struct {
		Identifier string `json:"identifier"`
	}
	if err == nil {
		err = m.client.CallSession(ctx, sessionID, "Page.addScriptToEvaluateOnNewDocument", map[string]any{
			"source":    buildWindowMarkerScript(config),
			"worldName": windowMarkerWorldName,
		}, &scriptResult)
		if scriptResult.Identifier != "" {
			m.mu.Lock()
			if session, ok := m.sessions[sessionID]; ok {
				session.ScriptID = scriptResult.Identifier
				m.sessions[sessionID] = session
			}
			m.mu.Unlock()
		}
		if err == nil && scriptResult.Identifier == "" {
			err = fmt.Errorf("page marker script registration returned no identifier")
		}
		if err == nil {
			err = m.client.CallSession(ctx, sessionID, "Runtime.evaluate", map[string]any{
				"expression": buildWindowMarkerScript(config),
			}, nil)
		}
	}
	if err != nil {
		m.mu.Lock()
		if ctx.Err() != context.Canceled {
			m.setupFailures++
		}
		m.mu.Unlock()
		return
	}
}

func (m *windowMarkerController) close(ctx context.Context) error {
	m.cancelInFlightSetup()
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.deactivateLocked(ctx)
}

func (m *windowMarkerController) cancelInFlightSetup() {
	m.mu.Lock()
	cancel := m.cancelSetup
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
