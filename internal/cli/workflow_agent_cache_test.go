package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/spf13/cobra"
)

func cacheFixture(t *testing.T, dir, command string, args []string, read func() webagent.Result, mutate func() error) (webagent.Result, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	a := &app{out: &out, err: &errOut, build: BuildInfo{Commit: "cache-test"}, opts: options{stateDir: dir, config: filepath.Join(dir, "config.json"), json: true}}
	root := &cobra.Command{Use: "test", SilenceUsage: true, SilenceErrors: true}
	root.SetContext(context.Background())
	provider := &cobra.Command{Use: "chatgpt"}
	conversations := &cobra.Command{Use: "conversations"}
	for _, name := range []string{"list", "detail", "await"} {
		cmd := &cobra.Command{Use: name, RunE: func(cmd *cobra.Command, args []string) error {
			return a.renderWebAgentResult(cmd.Context(), "fixture output", read())
		}}
		cmd.Flags().Int("limit", 20, "limit")
		conversations.AddCommand(cmd)
	}
	provider.AddCommand(conversations)
	provider.AddCommand(&cobra.Command{Use: "ask", RunE: func(cmd *cobra.Command, args []string) error {
		if mutate != nil {
			return mutate()
		}
		return nil
	}})
	root.AddCommand(provider)
	a.configureConversationCache(provider, webagent.ProviderChatGPT)
	argv := []string{"chatgpt"}
	if command != "ask" {
		argv = append(argv, "conversations")
	}
	argv = append(argv, command)
	argv = append(argv, args...)
	root.SetArgs(argv)
	err := root.Execute()
	var result webagent.Result
	if out.Len() > 0 {
		if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
			t.Fatalf("decode fixture output: %v", decodeErr)
		}
	}
	return result, err
}
func cacheFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func cacheFixtureResult(operation webagent.Operation) webagent.Result {
	result := webagent.NewMetadataResult(webagent.ProviderChatGPT, operation, map[string]any{"number": json.Number("9007199254740993")}, "cache-test", nil)
	if operation == webagent.OperationConversationsDetail {
		result.State = webagent.StateTerminal
	}
	return result
}
func TestConversationCacheTTLAndEligibility(t *testing.T) {
	now := time.Now().UTC()
	entry := conversationCacheEntry{CapturedAt: now, Result: cacheFixtureResult(webagent.OperationConversationsList)}
	for _, test := range []struct {
		age   time.Duration
		valid bool
	}{{-time.Nanosecond, false}, {0, true}, {30*time.Second - time.Nanosecond, true}, {30 * time.Second, false}, {31 * time.Second, false}} {
		if got := conversationCacheValid(entry, now.Add(test.age)); got != test.valid {
			t.Errorf("age %s: %v", test.age, got)
		}
	}
	entry.Result = cacheFixtureResult(webagent.OperationConversationsDetail)
	if !conversationCacheValid(entry, now) {
		t.Fatal("terminal detail must cache")
	}
	entry.Result.State = webagent.StateIncomplete
	if conversationCacheValid(entry, now) {
		t.Fatal("incomplete must not cache")
	}
	entry.Result = cacheFixtureResult(webagent.OperationConversationsAwait)
	if conversationCacheValid(entry, now) {
		t.Fatal("await must not cache")
	}
}
func TestConversationCacheHitFreshExpiryAndNumbers(t *testing.T) {
	dir := cacheFixtureDir(t)
	calls := 0
	read := func() webagent.Result { calls++; return cacheFixtureResult(webagent.OperationConversationsList) }
	first, err := cacheFixture(t, dir, "list", nil, read, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cacheFixture(t, dir, "list", nil, read, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || second.Evidence.ReadMode != "cache" || second.Evidence.Cache == nil || second.Evidence.Cache.SourceRunID != first.Evidence.RunID || second.Evidence.RunID == first.Evidence.RunID || second.Evidence.Target != nil || second.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("calls=%d evidence=%+v", calls, second.Evidence)
	}
	encoded, _ := json.Marshal(loadConversationCache(filepath.Join(dir, "webagent/read-cache/chatgpt.json")))
	if !bytes.Contains(encoded, []byte("9007199254740993")) {
		t.Fatal("JSON number precision lost")
	}
	if _, err = cacheFixture(t, dir, "list", []string{"--fresh"}, read, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("fresh did not read")
	}
	path := filepath.Join(dir, "webagent/read-cache/chatgpt.json")
	entries := loadConversationCache(path)
	for key, entry := range entries {
		entry.CapturedAt = time.Now().Add(-conversationCacheTTL)
		entries[key] = entry
	}
	if err := saveConversationCache(path, entries); err != nil {
		t.Fatal(err)
	}
	if _, err = cacheFixture(t, dir, "list", nil, read, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("expired entry reused")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("cache permissions %v", info.Mode())
	}
	info, _ = os.Stat(filepath.Dir(path))
	if info.Mode().Perm() != 0700 {
		t.Fatalf("cache directory permissions %v", info.Mode())
	}
}
func TestConversationCacheScopeMutationAndAwait(t *testing.T) {
	dir := cacheFixtureDir(t)
	calls := 0
	read := func() webagent.Result { calls++; return cacheFixtureResult(webagent.OperationConversationsList) }
	for _, args := range [][]string{nil, nil, {"--limit", "10"}, {"--limit", "10"}} {
		if _, err := cacheFixture(t, dir, "list", args, read, nil); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("pagination calls=%d", calls)
	}
	if _, err := cacheFixture(t, dir, "ask", nil, read, func() error { return errors.New("uncertain dispatch") }); err == nil {
		t.Fatal("mutation should retain error")
	}
	if _, err := cacheFixture(t, dir, "list", nil, read, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("mutation did not invalidate")
	}
	providerDir := filepath.Join(dir, "webagent/chatgpt")
	if err := os.MkdirAll(providerDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, "request-template.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheFixture(t, dir, "list", nil, read, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatal("auth namespace change reused cache")
	}
	awaitRead := func() webagent.Result { calls++; return cacheFixtureResult(webagent.OperationConversationsAwait) }
	for i := 0; i < 2; i++ {
		if _, err := cacheFixture(t, dir, "await", nil, awaitRead, nil); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 6 {
		t.Fatal("await was cached")
	}
	detailRead := func() webagent.Result { calls++; return cacheFixtureResult(webagent.OperationConversationsDetail) }
	for _, id := range []string{"a", "a", "b"} {
		if _, err := cacheFixture(t, dir, "detail", []string{id}, detailRead, nil); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 8 {
		t.Fatal("detail identity cache incorrect")
	}
}
func TestConversationCacheDoesNotRetainIncompleteOrFailedFresh(t *testing.T) {
	dir := cacheFixtureDir(t)
	calls := 0
	read := func() webagent.Result {
		calls++
		r := cacheFixtureResult(webagent.OperationConversationsDetail)
		r.State = webagent.StateIncomplete
		return r
	}
	for i := 0; i < 2; i++ {
		if _, err := cacheFixture(t, dir, "detail", []string{"a"}, read, nil); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("incomplete was cached")
	}
	good := func() webagent.Result { calls++; return cacheFixtureResult(webagent.OperationConversationsList) }
	if _, err := cacheFixture(t, dir, "list", nil, good, nil); err != nil {
		t.Fatal(err)
	}
	bad := func() webagent.Result {
		calls++
		r := cacheFixtureResult(webagent.OperationConversationsList)
		r.OK = false
		r.State = webagent.StateFailed
		r.Error = &webagent.OperationError{Code: "fixture_failure", ErrClass: "auth", Message: "fixture failure"}
		return r
	}
	if _, err := cacheFixture(t, dir, "list", []string{"--fresh"}, bad, nil); err == nil {
		t.Fatal("fresh failure lost")
	}
	if _, err := cacheFixture(t, dir, "list", nil, good, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 5 {
		t.Fatalf("failed fresh retained old success: calls=%d", calls)
	}
}
func TestConversationCacheConcurrentProcesses(t *testing.T) {
	dir := cacheFixtureDir(t)
	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestConversationCacheProcessHelper$")
			cmd.Env = append(os.Environ(), "CDP_CACHE_TEST_DIR="+dir)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("child: %v %s", err, b)
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		return
	}
	b, err := os.ReadFile(filepath.Join(dir, "provider-calls"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "read\n" {
		t.Fatalf("provider calls: %q", b)
	}
}
func TestConversationCacheProcessHelper(t *testing.T) {
	dir := os.Getenv("CDP_CACHE_TEST_DIR")
	if dir == "" {
		t.Skip("subprocess only")
	}
	_, err := cacheFixture(t, dir, "list", nil, func() webagent.Result {
		f, err := os.OpenFile(filepath.Join(dir, "provider-calls"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(f, "read")
		f.Close()
		time.Sleep(100 * time.Millisecond)
		r := cacheFixtureResult(webagent.OperationConversationsList)
		if os.Getenv("CDP_CACHE_TEST_RATE_LIMIT") == "1" {
			r.OK = false
			r.State = webagent.StateFailed
			r.Error = &webagent.OperationError{Code: "chatgpt_rate_limited", ErrClass: "rate_limit", Message: "synthetic rate limit", RetrySafe: true}
		}
		return r
	}, nil)
	if (err != nil) != (os.Getenv("CDP_CACHE_TEST_RATE_LIMIT") == "1") {
		t.Fatalf("rate-limit exit mismatch: %v", err)
	}
}
func TestConversationCacheOldReadCannotSurviveMutation(t *testing.T) {
	dir := cacheFixtureDir(t)
	calls := 0
	read := func() webagent.Result {
		calls++
		if err := invalidateConversationCache(conversationCacheGenerationPath(dir, webagent.ProviderChatGPT)); err != nil {
			t.Fatal(err)
		}
		return cacheFixtureResult(webagent.OperationConversationsList)
	}
	for i := 0; i < 2; i++ {
		if _, err := cacheFixture(t, dir, "list", nil, read, nil); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("in-flight result survived invalidation")
	}
}
func TestConversationCacheHumanOutput(t *testing.T) {
	dir := cacheFixtureDir(t)
	a := &app{out: &bytes.Buffer{}, err: &bytes.Buffer{}, build: BuildInfo{Commit: "cache-test"}, opts: options{stateDir: dir, config: filepath.Join(dir, "config.json")}}
	root := &cobra.Command{Use: "test"}
	root.SetContext(context.Background())
	provider := &cobra.Command{Use: "chatgpt"}
	conversations := &cobra.Command{Use: "conversations"}
	calls := 0
	conversations.AddCommand(&cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, args []string) error {
		calls++
		return a.renderWebAgentResult(cmd.Context(), "human text", cacheFixtureResult(webagent.OperationConversationsList))
	}})
	provider.AddCommand(conversations)
	root.AddCommand(provider)
	a.configureConversationCache(provider, webagent.ProviderChatGPT)
	for i := 0; i < 2; i++ {
		root.SetArgs([]string{"chatgpt", "conversations", "list"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || strings.Count(a.out.(*bytes.Buffer).String(), "human text") != 2 {
		t.Fatal("human output not preserved")
	}
}

// Optional installed gate uses only synthetic cached conversations. A hit must
// work without a provider template or browser, in separate real CLI processes.
func TestConversationCacheInstalled(t *testing.T) {
	binary := os.Getenv("CDP_CACHE_INSTALLED_BINARY")
	if binary == "" {
		t.Skip("set CDP_CACHE_INSTALLED_BINARY to the installed cdp executable")
	}
	version, err := exec.Command(binary, "version", "--json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var build BuildInfo
	if err := json.Unmarshal(version, &build); err != nil {
		t.Fatal(err)
	}
	// main supplies raw build metadata; version adds source_state for display.
	build.SourceState = ""
	dir := cacheFixtureDir(t)
	a := &app{out: &bytes.Buffer{}, err: &bytes.Buffer{}, build: build}
	root := a.newRoot()
	for name, value := range map[string]string{"config": filepath.Join(dir, "config.json"), "state-dir": dir, "browser-mode": "headed"} {
		if err := root.PersistentFlags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, provider := range []webagent.Provider{webagent.ProviderChatGPT, webagent.ProviderClaude, webagent.ProviderGemini, webagent.ProviderGrok, webagent.ProviderPerplexity, webagent.ProviderTripadvisor} {
		entries := make(map[string]conversationCacheEntry)
		for _, operation := range []string{"list", "detail"} {
			cmd, err := findCommand(root, "workflow agent "+string(provider)+" conversations "+operation)
			if err != nil {
				t.Fatal(err)
			}
			cmd.InitDefaultHelpFlag()
			args := []string{}
			op := webagent.OperationConversationsList
			if operation == "detail" {
				args = []string{"synthetic-conversation"}
				op = webagent.OperationConversationsDetail
			}
			key, err := a.conversationCacheKey(cmd, args, provider, dir)
			if err != nil {
				t.Fatal(err)
			}
			result := cacheFixtureResult(op)
			result.Provider = provider
			result.Evidence.BuildCommit = build.Commit
			entries[key] = conversationCacheEntry{CapturedAt: time.Now().UTC(), Human: "synthetic cached conversation", Result: result}
		}
		if err := saveConversationCache(filepath.Join(dir, "webagent/read-cache", string(provider)+".json"), entries); err != nil {
			t.Fatal(err)
		}
		for _, operation := range []string{"list", "detail"} {
			for i := 0; i < 2; i++ {
				args := []string{"--config", filepath.Join(dir, "config.json"), "--state-dir", dir, "--browser-mode", "headed", "workflow", "agent", string(provider), "conversations", operation}
				if operation == "detail" {
					args = append(args, "synthetic-conversation")
				}
				args = append(args, "--json")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("%s %s installed hit: %v %s", provider, operation, err, output)
				}
				var got webagent.Result
				if err := json.Unmarshal(output, &got); err != nil {
					t.Fatal(err)
				}
				if got.Evidence.ReadMode != "cache" || got.Evidence.Cache == nil || got.Evidence.Target != nil || got.Provider != provider || got.Cleanup.State != webagent.CleanupNotRequired {
					t.Fatalf("%s %s hit: %+v", provider, operation, got)
				}
			}
		}
	}
	// A synthetic 429 must block both installed commands, including --fresh.
	provider := webagent.ProviderChatGPT
	scope, err := a.conversationCacheScope(root, provider, dir)
	if err != nil {
		t.Fatal(err)
	}
	limited := cacheFixtureResult(webagent.OperationConversationsList)
	limited.OK = false
	limited.State = webagent.StateFailed
	limited.Error = &webagent.OperationError{Code: "chatgpt_rate_limited", ErrClass: "rate_limit", Message: "synthetic rate limit", RetrySafe: true, RetryAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}
	entries := map[string]conversationCacheEntry{"cooldown:" + scope: {CapturedAt: time.Now().UTC(), Result: limited}}
	if err := saveConversationCache(filepath.Join(dir, "webagent/read-cache/chatgpt.json"), entries); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"list", "detail"} {
		args := []string{"--config", filepath.Join(dir, "config.json"), "--state-dir", dir, "--browser-mode", "headed", "workflow", "agent", "chatgpt", "conversations", operation}
		if operation == "detail" {
			args = append(args, "synthetic-conversation")
		}
		args = append(args, "--fresh", "--json")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		cancel()
		var got webagent.Result
		if parseErr := json.Unmarshal(output, &got); parseErr != nil {
			t.Fatalf("installed cooldown: %v %s", parseErr, output)
		}
		if err == nil || got.OK || got.Error == nil || got.Error.Code != "chatgpt_rate_limited" || got.Evidence.ReadMode != "rate_limit_cooldown" || got.Evidence.Target != nil || got.Cleanup.State != webagent.CleanupNotRequired {
			t.Fatalf("installed %s cooldown: err=%v result=%+v", operation, err, got)
		}
	}

	// Policy enforcement must run before a cached result can bypass the provider.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"agents":{"disabled_providers":["chatgpt"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(binary, "--config", filepath.Join(dir, "config.json"), "--state-dir", dir, "workflow", "agent", "chatgpt", "conversations", "list", "--json").CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("disabled_by_config")) {
		t.Fatal("cache bypassed disabled provider policy")
	}
}

func TestConversationCacheSharesRateLimitCooldown(t *testing.T) {
	dir := cacheFixtureDir(t)
	calls := 0
	read := func() webagent.Result {
		calls++
		r := cacheFixtureResult(webagent.OperationConversationsList)
		r.OK = false
		r.State = webagent.StateFailed
		r.Error = &webagent.OperationError{Code: "chatgpt_rate_limited", ErrClass: "rate_limit", Message: "provider rate limited", RetrySafe: true}
		return r
	}
	if _, err := cacheFixture(t, dir, "list", nil, read, nil); err == nil {
		t.Fatal("first rate limit lost")
	}
	for _, command := range []string{"list", "detail"} {
		args := []string{"--fresh"}
		if command == "detail" {
			args = append(args, "synthetic-conversation")
		}
		got, err := cacheFixture(t, dir, command, args, read, nil)
		if err == nil || got.OK || got.Error == nil || got.Error.RetryAt == "" || got.Evidence.ReadMode != "rate_limit_cooldown" {
			t.Fatalf("%s cooldown result=%+v err=%v", command, got, err)
		}
		if command == "detail" && got.Operation != webagent.OperationConversationsDetail {
			t.Fatal("cooldown retained list operation")
		}
		if got.Evidence.Target != nil || got.Action != nil || got.Cleanup.State != webagent.CleanupNotRequired {
			t.Fatal("cooldown retained browser/action evidence")
		}
	}
	if calls != 1 {
		t.Fatalf("provider calls=%d want 1", calls)
	}
}

func TestConversationCooldownConcurrentProcesses(t *testing.T) {
	dir := cacheFixtureDir(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestConversationCacheProcessHelper$")
			cmd.Env = append(os.Environ(), "CDP_CACHE_TEST_DIR="+dir, "CDP_CACHE_TEST_RATE_LIMIT=1")
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("child: %v %s", err, b)
			}
		}()
	}
	wg.Wait()
	b, err := os.ReadFile(filepath.Join(dir, "provider-calls"))
	if err != nil || string(b) != "read\n" {
		t.Fatalf("provider calls=%q err=%v", b, err)
	}
}

func TestConversationCooldownExpiryAndScope(t *testing.T) {
	dir := cacheFixtureDir(t)
	calls := 0
	retryAt := time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339Nano)
	read := func() webagent.Result {
		calls++
		r := cacheFixtureResult(webagent.OperationConversationsList)
		r.OK = false
		r.State = webagent.StateFailed
		r.Error = &webagent.OperationError{Code: "chatgpt_rate_limited", ErrClass: "rate_limit", Message: "synthetic rate limit", RetrySafe: true, RetryAt: retryAt}
		return r
	}
	_, _ = cacheFixture(t, dir, "list", nil, read, nil)
	if _, err := cacheFixture(t, dir, "ask", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := cacheFixture(t, dir, "list", nil, read, nil)
	if calls != 1 || got.Error == nil || got.Error.RetryAt != retryAt {
		t.Fatal("mutation bypassed cooldown or Retry-After changed")
	}
	path := filepath.Join(dir, "webagent/read-cache/chatgpt.json")
	entries := loadConversationCache(path)
	for key, entry := range entries {
		if strings.HasPrefix(key, "cooldown:") {
			entry.Result.Error.RetryAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
			entries[key] = entry
		}
	}
	if err := saveConversationCache(path, entries); err != nil {
		t.Fatal(err)
	}
	_, _ = cacheFixture(t, dir, "list", nil, read, nil)
	if calls != 2 {
		t.Fatal("expired cooldown blocked provider")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"browser":{"mode":"headed"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = cacheFixture(t, dir, "list", nil, read, nil)
	if calls != 3 {
		t.Fatal("different configuration reused cooldown")
	}
}

func TestConversationCacheAfterReadRefreshesTemplate(t *testing.T) {
	for _, rateLimited := range []bool{false, true} {
		t.Run(fmt.Sprintf("rate_limited=%v", rateLimited), func(t *testing.T) {
			dir := cacheFixtureDir(t)
			calls := 0
			read := func() webagent.Result {
				calls++
				providerDir := filepath.Join(dir, "webagent/chatgpt")
				if err := os.MkdirAll(providerDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(providerDir, "request-template.json"), []byte(`{"captured_at":"synthetic-new-read-evidence"}`), 0600); err != nil {
					t.Fatal(err)
				}
				r := cacheFixtureResult(webagent.OperationConversationsList)
				if rateLimited {
					r.OK, r.State = false, webagent.StateFailed
					r.Error = &webagent.OperationError{Code: "chatgpt_rate_limited", ErrClass: "rate_limit", Message: "synthetic rate limit", RetrySafe: true, RetryAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}
				}
				return r
			}
			if _, err := cacheFixture(t, dir, "list", nil, read, nil); (err != nil) != rateLimited {
				t.Fatalf("first read error=%v", err)
			}
			command, args, mode := "list", []string(nil), "cache"
			if rateLimited {
				command, args, mode = "detail", []string{"synthetic", "--fresh"}, "rate_limit_cooldown"
			}
			got, err := cacheFixture(t, dir, command, args, read, nil)
			if (err != nil) != rateLimited || calls != 1 || got.Evidence.ReadMode != mode {
				t.Fatalf("calls=%d mode=%s err=%v; refresh during first read must preserve cache/cooldown", calls, got.Evidence.ReadMode, err)
			}
		})
	}
}
