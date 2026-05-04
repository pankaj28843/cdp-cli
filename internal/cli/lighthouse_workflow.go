package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type lighthouseWorkflowOptions struct {
	Categories string
	FormFactor string
	Throttling string
	OutDir     string
	Wait       time.Duration
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
	bin, err := exec.LookPath("lighthouse")
	if err != nil {
		return commandError("dependency_missing", "usage", "Lighthouse CLI was not found on PATH", ExitUsage, []string{"npm install -g lighthouse", "cdp workflow a11y " + rawURL + " --json"})
	}
	outDir := strings.TrimSpace(opts.OutDir)
	if outDir == "" {
		outDir = filepath.Join("tmp", "lighthouse")
	}
	prefix := filepath.Join(outDir, "report")
	args := []string{rawURL, "--output=json", "--output=html", "--output-path=" + prefix, "--chrome-flags=--headless=new"}
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
		return commandError("lighthouse_failed", "check_failed", fmt.Sprintf("lighthouse failed: %v: %s", err, strings.TrimSpace(string(combined))), ExitCheckFailed, []string{"lighthouse " + rawURL + " --output=json --output=html", "cdp workflow a11y " + rawURL + " --json"})
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
	report := map[string]any{
		"ok":            true,
		"url":           rawURL,
		"categories":    categorySummary,
		"failed_audits": failed,
		"artifacts": map[string]string{
			"json": jsonPath,
			"html": htmlPath,
		},
		"workflow": map[string]any{"name": "lighthouse", "categories": strings.TrimSpace(opts.Categories), "form_factor": strings.TrimSpace(opts.FormFactor), "throttling": strings.TrimSpace(opts.Throttling), "wait": durationString(opts.Wait)},
	}
	return a.render(ctx, fmt.Sprintf("lighthouse\t%d categories\t%d failed audits", len(parsed.Categories), len(failed)), report)
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
