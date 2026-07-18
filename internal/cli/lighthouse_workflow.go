package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

type lighthouseWorkflowOptions struct {
	Categories string
	FormFactor string
	Throttling string
	OutDir     string
	Wait       time.Duration
	Redact     string
}

type lighthouseReport struct {
	Categories map[string]struct {
		Title string  `json:"title"`
		Score float64 `json:"score"`
	} `json:"categories"`
	Audits map[string]struct {
		ID           string   `json:"id"`
		Title        string   `json:"title"`
		Score        *float64 `json:"score"`
		DisplayValue string   `json:"displayValue"`
		Description  string   `json:"description"`
	} `json:"audits"`
}

func runLighthouseWorkflow(ctx context.Context, a *app, rawURL string, opts lighthouseWorkflowOptions) error {
	redact := artifacts.NormalizeMode(opts.Redact)
	if redact != artifacts.ModeNone && redact != artifacts.ModeSafe && redact != artifacts.ModeHeaders {
		return commandError("usage", "usage", "--redact must be none, safe, or headers", ExitUsage, []string{"cdp --browser-mode headless workflow lighthouse 'https://example.com' --redact safe --json"})
	}
	runtime, err := a.requiredDaemonRuntime(ctx)
	if err != nil {
		return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
	}
	port, err := lighthouseDaemonPort(runtime)
	if err != nil {
		return commandError("lighthouse_daemon_endpoint_unsupported", "connection", err.Error(), ExitConnection, []string{"cdp --browser-mode headless daemon keepalive --repair --json", "cdp --browser-mode headless workflow lighthouse 'https://example.com' --json"})
	}
	bin, err := exec.LookPath("lighthouse")
	if err != nil {
		return commandError("dependency_missing", "usage", "Lighthouse CLI was not found on PATH", ExitUsage, []string{"npm install -g lighthouse", "cdp workflow a11y " + rawURL + " --json"})
	}
	outDir := strings.TrimSpace(opts.OutDir)
	if outDir == "" {
		outDir = filepath.Join("tmp", "lighthouse")
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return commandError("artifact_write_failed", "io", fmt.Sprintf("create Lighthouse output directory: %v", err), ExitInternal, []string{"mkdir -p " + outDir})
	}
	prefix := filepath.Join(outDir, "report")
	args := []string{rawURL, "--output=json", "--output=html", "--output-path=" + prefix, "--port=" + port}
	if strings.TrimSpace(opts.Categories) != "" {
		args = append(args, "--only-categories="+strings.TrimSpace(opts.Categories))
	}
	if strings.TrimSpace(opts.FormFactor) != "" {
		args = append(args, "--form-factor="+strings.TrimSpace(opts.FormFactor))
	}
	if strings.TrimSpace(opts.Throttling) != "" {
		args = append(args, "--throttling-method="+strings.TrimSpace(opts.Throttling))
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	combined, err := cmd.CombinedOutput()
	jsonPath := prefix + ".report.json"
	htmlPath := prefix + ".report.html"
	if _, statErr := os.Stat(jsonPath); statErr != nil {
		jsonPath = prefix + ".json"
		htmlPath = prefix + ".html"
	}
	if err != nil {
		message := artifacts.NewRedactor(redact).BodyText(strings.TrimSpace(string(combined)), "lighthouse.stderr")
		if len(message) > 2048 {
			message = message[:2048] + "<truncated>"
		}
		return commandError("lighthouse_failed", "check_failed", fmt.Sprintf("lighthouse failed against daemon-owned Chrome: %v: %s", err, message), ExitCheckFailed, []string{"cdp --browser-mode headless daemon status --json", "cdp workflow a11y " + rawURL + " --json"})
	}
	reportBytes, err := os.ReadFile(jsonPath)
	if err != nil {
		return commandError("artifact_missing", "internal", fmt.Sprintf("read Lighthouse JSON report: %v", err), ExitInternal, []string{"lighthouse " + rawURL + " --output=json --output=html"})
	}
	var parsed lighthouseReport
	if err := json.Unmarshal(reportBytes, &parsed); err != nil {
		return commandError("invalid_lighthouse_report", "internal", fmt.Sprintf("decode Lighthouse report: %v", err), ExitInternal, []string{"jq . " + jsonPath})
	}
	failed := lighthouseFailedAudits(parsed)
	categorySummary := map[string]any{}
	for name, category := range parsed.Categories {
		categorySummary[name] = map[string]any{"title": category.Title, "score": category.Score}
	}
	sort.Slice(failed, func(i, j int) bool { return fmt.Sprint(failed[i]["id"]) < fmt.Sprint(failed[j]["id"]) })
	redactor := artifacts.NewRedactor(redact)
	artifactWarning := "Lighthouse reports can contain page URLs, audit details, and browser state; keep unredacted reports local"
	if redact != artifacts.ModeNone {
		safeFailed := make([]map[string]any, 0, len(failed))
		for _, audit := range failed {
			safeFailed = append(safeFailed, map[string]any{"id": audit["id"], "title": audit["title"], "score": audit["score"]})
		}
		safeReport := map[string]any{"url": redactor.URL(rawURL, "url"), "categories": categorySummary, "failed_audits": safeFailed, "source": "lighthouse-summary"}
		safeJSON, marshalErr := json.MarshalIndent(safeReport, "", "  ")
		if marshalErr != nil {
			return commandError("invalid_lighthouse_report", "internal", fmt.Sprintf("marshal safe Lighthouse summary: %v", marshalErr), ExitInternal, []string{"cdp workflow lighthouse --help"})
		}
		if _, err := writeArtifactFile(jsonPath, append(safeJSON, '\n')); err != nil {
			return err
		}
		if _, err := writeArtifactFile(htmlPath, []byte(lighthouseSafeHTML(redactor.URL(rawURL, "url"), categorySummary, safeFailed))); err != nil {
			return err
		}
		failed = safeFailed
	}
	safety := redactor.Metadata(true, artifactWarning)
	artifactList := []map[string]any{
		lighthouseArtifact("lighthouse-json", jsonPath, safety),
		lighthouseArtifact("lighthouse-html", htmlPath, safety),
	}
	report := map[string]any{
		"ok":            true,
		"url":           redactor.URL(rawURL, "url"),
		"categories":    categorySummary,
		"failed_audits": failed,
		"artifacts": map[string]string{
			"json": jsonPath,
			"html": htmlPath,
		},
		"artifact_list":   artifactList,
		"artifact_safety": safety,
		"workflow":        map[string]any{"name": "lighthouse", "categories": strings.TrimSpace(opts.Categories), "form_factor": strings.TrimSpace(opts.FormFactor), "throttling": strings.TrimSpace(opts.Throttling), "wait": durationString(opts.Wait), "browser_mode": runtime.BrowserMode, "daemon_backed": true},
	}
	return a.render(ctx, fmt.Sprintf("lighthouse\t%d categories\t%d failed audits", len(parsed.Categories), len(failed)), report)
}

func lighthouseDaemonPort(runtime daemon.Runtime) (string, error) {
	port := strings.TrimSpace(runtime.ChromePort)
	if port != "" {
		if number, err := strconv.Atoi(port); err == nil && number > 0 && number <= 65535 {
			return port, nil
		}
	}
	endpoint, err := url.Parse(strings.TrimSpace(runtime.Endpoint))
	if err != nil || endpoint.Hostname() == "" || endpoint.Port() == "" {
		return "", fmt.Errorf("daemon runtime does not expose a loopback Chrome port for Lighthouse")
	}
	host := endpoint.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("daemon Chrome endpoint is not loopback; refusing Lighthouse attachment")
	}
	number, err := strconv.Atoi(endpoint.Port())
	if err != nil || number <= 0 || number > 65535 {
		return "", fmt.Errorf("daemon Chrome endpoint has an invalid port")
	}
	return endpoint.Port(), nil
}

func lighthouseArtifact(kind, path string, safety artifacts.SafetyMetadata) map[string]any {
	artifact := map[string]any{"type": kind, "path": path, "safety": safety}
	if info, err := os.Stat(path); err == nil {
		artifact["bytes"] = info.Size()
	}
	return artifact
}

func lighthouseSafeHTML(rawURL string, categories map[string]any, failed []map[string]any) string {
	var out strings.Builder
	out.WriteString("<!doctype html><meta charset=\"utf-8\"><title>Lighthouse summary</title><h1>Lighthouse summary</h1><p>URL: ")
	out.WriteString(html.EscapeString(rawURL))
	out.WriteString("</p><h2>Categories</h2><ul>")
	names := make([]string, 0, len(categories))
	for name := range categories {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out.WriteString("<li>")
		out.WriteString(html.EscapeString(name))
		out.WriteString(": ")
		out.WriteString(html.EscapeString(fmt.Sprint(categories[name])))
		out.WriteString("</li>")
	}
	out.WriteString("</ul><h2>Failed audits</h2><ul>")
	for _, audit := range failed {
		out.WriteString("<li>")
		out.WriteString(html.EscapeString(fmt.Sprintf("%v: %v", audit["id"], audit["score"])))
		out.WriteString("</li>")
	}
	out.WriteString("</ul>")
	return out.String()
}

func lighthouseFailedAudits(report lighthouseReport) []map[string]any {
	failed := []map[string]any{}
	for id, audit := range report.Audits {
		if audit.Score == nil || *audit.Score >= 1 {
			continue
		}
		failed = append(failed, map[string]any{"id": id, "title": audit.Title, "score": *audit.Score, "display_value": audit.DisplayValue, "description": audit.Description})
	}
	return failed
}
