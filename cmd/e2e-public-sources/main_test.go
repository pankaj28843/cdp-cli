package main

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func TestRunFixturesBoundsGlobalConcurrencyAndSerializesPlatforms(t *testing.T) {
	fixtures := []fixture{
		{Name: "reddit-one", Platform: "reddit"},
		{Name: "reddit-two", Platform: "reddit"},
		{Name: "x-one", Platform: "x"},
		{Name: "linkedin-one", Platform: "linkedin"},
	}
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	done := make(chan []error, 1)
	var mu sync.Mutex
	active, maxActive := 0, 0
	byPlatform := map[string]int{}
	serial := true
	calls := map[string]int{}
	go func() {
		done <- runFixtures(fixtures, 2, 0, 0, func(item fixture) error {
			mu.Lock()
			active++
			byPlatform[item.Platform]++
			calls[item.Name]++
			if active > maxActive {
				maxActive = active
			}
			if byPlatform[item.Platform] > 1 {
				serial = false
			}
			mu.Unlock()
			entered <- struct{}{}
			<-release
			mu.Lock()
			active--
			byPlatform[item.Platform]--
			mu.Unlock()
			return nil
		})
	}()
	<-entered
	<-entered
	close(release)
	if errs := <-done; len(errs) != 0 {
		t.Fatalf("runFixtures() errors = %v", errs)
	}
	mu.Lock()
	defer mu.Unlock()
	if maxActive != 2 {
		t.Fatalf("maximum active runs = %d, want 2", maxActive)
	}
	if !serial {
		t.Fatal("same-platform fixtures overlapped")
	}
	for _, item := range fixtures {
		if calls[item.Name] != 1 {
			t.Fatalf("fixture %q ran %d times, want once", item.Name, calls[item.Name])
		}
	}
}

func TestRunFixturesReturnsFailureWithoutRetry(t *testing.T) {
	fixtures := []fixture{
		{Name: "reddit-one", Platform: "reddit"},
		{Name: "x-one", Platform: "x"},
		{Name: "linkedin-one", Platform: "linkedin"},
	}
	want := errors.New("synthetic public source failure")
	var mu sync.Mutex
	calls := map[string]int{}
	errs := runFixtures(fixtures, 2, 0, 0, func(item fixture) error {
		mu.Lock()
		defer mu.Unlock()
		calls[item.Name]++
		if item.Name == "x-one" {
			return want
		}
		return nil
	})
	if len(errs) != 1 || !errors.Is(errs[0], want) {
		t.Fatalf("runFixtures() errors = %v, want one %v", errs, want)
	}
	for _, item := range fixtures {
		if calls[item.Name] != 1 {
			t.Fatalf("fixture %q ran %d times, want once", item.Name, calls[item.Name])
		}
	}
}

func TestParseManifestRequiresSemanticMinimumForNativeCollectors(t *testing.T) {
	_, err := parseManifest([]byte(`{
  "version": 1,
  "max_parallel": 2,
  "fixtures": [{
    "name": "x-post",
    "platform": "x",
    "url": "https://x.com/karpathy/status/2079610838143623371",
    "workflow": "x-collect",
    "kind": "post_thread",
    "purpose": "public post"
  }]
}`))
	if err == nil {
		t.Fatal("parseManifest() accepted native collector without min_records")
	}
}

func TestValidateSuccessOutputRequiresMinimumRecordsAndExpectedWorkflow(t *testing.T) {
	item := fixture{Name: "x-post", Platform: "x", Workflow: "x-collect", Kind: "post_thread", MinRecords: 2}
	for _, tt := range []struct {
		name string
		body string
		want bool
	}{
		{"success", `{"ok":true,"kind":"post_thread","workflow":{"name":"x-collect","count":2,"interactions":0}}`, false},
		{"zero records", `{"ok":true,"kind":"post_thread","workflow":{"name":"x-collect","count":0}}`, true},
		{"wrong workflow", `{"ok":true,"kind":"post_thread","workflow":{"name":"rendered-extract","count":2}}`, true},
		{"missing interactions", `{"ok":true,"kind":"post_thread","workflow":{"name":"x-collect","count":2}}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSuccessOutput(item, []byte(tt.body))
			if (err != nil) != tt.want {
				t.Fatalf("validateSuccessOutput() error = %v, want error=%v", err, tt.want)
			}
		})
	}
}

func TestPublicFixtureManifestSemanticMinimums(t *testing.T) {
	data := []byte(`{"version":1,"max_parallel":2,"fixtures":[{"name":"hn","platform":"hacker-news","url":"https://news.ycombinator.com/item?id=46641042","workflow":"hacker-news-collect","kind":"thread","purpose":"public thread","min_records":2}]}`)
	manifest, err := parseManifest(data)
	if err != nil {
		t.Fatalf("parseManifest(): %v", err)
	}
	if got := manifest.Fixtures[0].MinRecords; got != 2 {
		t.Fatalf("min records = %d, want 2", got)
	}
	if _, err := json.Marshal(manifest); err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
}
