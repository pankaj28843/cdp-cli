// e2e-public-sources runs the opt-in public fixture lane with bounded Go concurrency.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type fixtureFile struct {
	Version     int       `json:"version"`
	MaxParallel int       `json:"max_parallel"`
	Fixtures    []fixture `json:"fixtures"`
}
type fixture struct {
	Name, Platform, URL, Workflow, Kind, Purpose string
	MinRecords                                   int `json:"min_records"`
}

func main() {
	binary := flag.String("cdp", "cdp", "installed cdp binary")
	fixturesPath := flag.String("fixtures", "testdata/public-source-fixtures.json", "public fixture manifest")
	parallel := flag.Int("parallel", 0, "maximum concurrent source lanes; 0 uses manifest")
	outDir := flag.String("out-dir", "tmp/e2e-public-sources", "local artifact directory")
	flag.Parse()
	data, err := os.ReadFile(*fixturesPath)
	if err != nil {
		fail(err)
	}
	manifest, err := parseManifest(data)
	if err != nil {
		fail(err)
	}
	if *parallel == 0 {
		*parallel = manifest.MaxParallel
	}
	if *parallel < 1 {
		*parallel = 1
	}
	if *parallel > 2 {
		*parallel = 2
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fail(err)
	}
	failed := false
	for _, err := range runFixtures(manifest.Fixtures, *parallel, 750*time.Millisecond, 3*time.Second, func(item fixture) error {
		return run(*binary, *outDir, item)
	}) {
		if err != nil {
			failed = true
			fmt.Fprintln(os.Stderr, err)
		}
	}
	if failed {
		os.Exit(1)
	}
}

func runFixtures(fixtures []fixture, parallel int, launchGap, platformGap time.Duration, runOne func(fixture) error) []error {
	if parallel < 1 {
		parallel = 1
	}
	results := make(chan error, len(fixtures))
	globalPacer := newStartPacer(launchGap)
	lanes := groupFixtureLanes(fixtures)
	globalSlots := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, lane := range lanes {
		wg.Add(1)
		go func(lane []fixture) {
			defer wg.Done()
			platformPacer := newStartPacer(platformGap)
			for _, item := range lane {
				platformPacer.Wait(item.Platform)
				globalPacer.Wait("all-platforms")
				globalSlots <- struct{}{}
				results <- runOne(item)
				<-globalSlots
			}
		}(lane)
	}
	wg.Wait()
	close(results)
	all := make([]error, 0, len(fixtures))
	for err := range results {
		if err != nil {
			all = append(all, err)
		}
	}
	return all
}

func groupFixtureLanes(fixtures []fixture) [][]fixture {
	byPlatform := make(map[string]int, len(fixtures))
	lanes := make([][]fixture, 0, len(fixtures))
	for _, item := range fixtures {
		index, ok := byPlatform[item.Platform]
		if !ok {
			index = len(lanes)
			byPlatform[item.Platform] = index
			lanes = append(lanes, nil)
		}
		lanes[index] = append(lanes[index], item)
	}
	return lanes
}

// startPacer reserves source-local start times so a repeated platform does not
// receive concurrent bursts even while unrelated public sites run in parallel.
type startPacer struct {
	gap  time.Duration
	mu   sync.Mutex
	next map[string]time.Time
}

func newStartPacer(gap time.Duration) *startPacer {
	return &startPacer{gap: gap, next: make(map[string]time.Time)}
}

func (p *startPacer) Wait(platform string) {
	if p.gap <= 0 || platform == "" {
		return
	}
	p.mu.Lock()
	now := time.Now()
	start := now
	if next := p.next[platform]; next.After(start) {
		start = next
	}
	p.next[platform] = start.Add(p.gap)
	p.mu.Unlock()
	if wait := time.Until(start); wait > 0 {
		time.Sleep(wait)
	}
}

func parseManifest(data []byte) (fixtureFile, error) {
	var manifest fixtureFile
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fixtureFile{}, fmt.Errorf("decode public fixture manifest: %w", err)
	}
	if manifest.Version != 1 {
		return fixtureFile{}, fmt.Errorf("unsupported public fixture manifest version %d", manifest.Version)
	}
	if manifest.MaxParallel < 1 || manifest.MaxParallel > 2 {
		return fixtureFile{}, fmt.Errorf("max_parallel must be between 1 and 2")
	}
	if len(manifest.Fixtures) == 0 {
		return fixtureFile{}, fmt.Errorf("public fixture manifest has no fixtures")
	}
	seen := make(map[string]struct{}, len(manifest.Fixtures))
	for _, item := range manifest.Fixtures {
		if item.Name == "" || item.Platform == "" || item.URL == "" || item.Workflow == "" {
			return fixtureFile{}, fmt.Errorf("public fixture must set name, platform, url, and workflow")
		}
		if _, ok := seen[item.Name]; ok {
			return fixtureFile{}, fmt.Errorf("duplicate public fixture name %q", item.Name)
		}
		seen[item.Name] = struct{}{}
		if item.Platform == "reddit" {
			if item.Workflow != "reddit-collect" {
				return fixtureFile{}, fmt.Errorf("Reddit fixture %q with kind %q must use reddit-collect", item.Name, item.Kind)
			}
			switch item.Kind {
			case "subreddit_listing", "thread", "user_profile":
			default:
				return fixtureFile{}, fmt.Errorf("Reddit fixture %q has unsupported kind %q", item.Name, item.Kind)
			}
		} else if item.Platform == "x" {
			if item.Workflow != "x-collect" || item.Kind != "post_thread" && item.Kind != "profile_posts" {
				return fixtureFile{}, fmt.Errorf("X fixture %q must use x-collect with post_thread or profile_posts kind", item.Name)
			}
		} else if item.Platform == "linkedin" {
			if item.Workflow != "linkedin-collect" || item.Kind != "activity_thread" && item.Kind != "company_posts" {
				return fixtureFile{}, fmt.Errorf("LinkedIn fixture %q must use linkedin-collect with activity_thread or company_posts kind", item.Name)
			}
		} else if item.Platform == "hacker-news" {
			if item.Workflow != "hacker-news-collect" || item.Kind != "thread" {
				return fixtureFile{}, fmt.Errorf("Hacker News fixture %q must use hacker-news-collect with thread kind", item.Name)
			}
		} else if item.Platform == "arxiv" {
			if item.Workflow != "arxiv-collect" || item.Kind != "paper" {
				return fixtureFile{}, fmt.Errorf("arXiv fixture %q must use arxiv-collect with paper kind", item.Name)
			}
		} else if item.Kind != "" {
			return fixtureFile{}, fmt.Errorf("non-Reddit fixture %q must not declare kind", item.Name)
		} else if item.Workflow != "rendered-extract" {
			return fixtureFile{}, fmt.Errorf("non-Reddit fixture %q must use rendered-extract", item.Name)
		}
		if item.Purpose == "" {
			return fixtureFile{}, fmt.Errorf("public fixture %q must state its purpose", item.Name)
		}
		if item.MinRecords < 0 {
			return fixtureFile{}, fmt.Errorf("public fixture %q min_records must be non-negative", item.Name)
		}
		if item.Workflow != "arxiv-collect" && item.MinRecords < 1 {
			return fixtureFile{}, fmt.Errorf("public fixture %q must set min_records for its native collector", item.Name)
		}
	}
	return manifest, nil
}

func run(binary, outDir string, item fixture) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args := []string{"--browser-mode", "headed", "--timeout", "75s", "workflow"}
	switch item.Workflow {
	case "reddit-collect":
		args = append(args, "reddit", "collect", item.URL, "--limit", "200", "--wait", "15s", "--json")
	case "x-collect":
		args = append(args, "x", "collect", item.URL, "--limit", "200", "--wait", "15s", "--json")
	case "linkedin-collect":
		args = append(args, "linkedin", "collect", item.URL, "--limit", "200", "--wait", "15s", "--json")
	case "hacker-news-collect":
		args = append(args, "hacker-news", "collect", item.URL, "--limit", "200", "--json")
	case "arxiv-collect":
		args = append(args, "arxiv", "collect", item.URL, "--json")
	default:
		args = append(args, "rendered-extract", item.URL, "--wait", "20s", "--settle", "2s", "--content-extractor", "auto", "--out-dir", filepath.Join(outDir, item.Name), "--json")
	}
	out, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s (%s): %w: %s", item.Name, item.Platform, err, out)
	}
	if err := validateSuccessOutput(item, out); err != nil {
		return fmt.Errorf("%s (%s): %w", item.Name, item.Platform, err)
	}
	fmt.Printf("ok %s (%s)\n", item.Name, item.Platform)
	return nil
}

func validateSuccessOutput(item fixture, out []byte) error {
	var envelope struct {
		OK       bool   `json:"ok"`
		Kind     string `json:"kind"`
		Workflow struct {
			Name         string `json:"name"`
			Count        int    `json:"count"`
			Interactions *int   `json:"interactions"`
		} `json:"workflow"`
		Paper struct {
			Identifier   string `json:"identifier"`
			CanonicalURL string `json:"canonical_url"`
			Title        string `json:"title"`
		} `json:"paper"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil || !envelope.OK {
		return fmt.Errorf("invalid success envelope: %s", out)
	}
	if item.Kind != "" && envelope.Kind != item.Kind {
		return fmt.Errorf("collected kind %q, want %q", envelope.Kind, item.Kind)
	}
	if envelope.Workflow.Name != item.Workflow {
		return fmt.Errorf("workflow name %q, want %q", envelope.Workflow.Name, item.Workflow)
	}
	if envelope.Workflow.Count < item.MinRecords {
		return fmt.Errorf("workflow count %d, want at least %d", envelope.Workflow.Count, item.MinRecords)
	}
	if envelope.Workflow.Interactions == nil {
		return fmt.Errorf("workflow interactions missing")
	}
	if item.Platform == "arxiv" && (envelope.Paper.Identifier == "" || envelope.Paper.CanonicalURL != "/abs/"+envelope.Paper.Identifier || envelope.Paper.Title == "") {
		return fmt.Errorf("invalid versioned paper identity")
	}
	return nil
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
