package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const conversationCacheTTL = 30 * time.Second
const conversationCacheMaxBytes = 16 << 20

type conversationCacheEntry struct {
	CapturedAt time.Time       `json:"captured_at"`
	Generation string          `json:"generation"`
	Human      string          `json:"human"`
	Result     webagent.Result `json:"result"`
}
type conversationCacheCapture struct {
	path, key, generation string
	provider              webagent.Provider
	operation             webagent.Operation
	entries               map[string]conversationCacheEntry
}
type conversationCacheContextKey struct{}

// Configure only CLI reads: provider-internal polling and await always remain fresh.
func (a *app) configureConversationCache(cmd *cobra.Command, provider webagent.Provider) {
	for _, child := range cmd.Commands() {
		a.configureConversationCache(child, provider)
	}
	if cmd.RunE == nil {
		return
	}
	read := cmd.Parent() != nil && cmd.Parent().Name() == "conversations" && (cmd.Name() == "list" || cmd.Name() == "detail")
	mutate := cmd.Name() == "research" || cmd.Name() == "ask" || cmd.Name() == "continue" || cmd.Name() == "delete" || (cmd.Name() == "refresh" && cmd.Parent() != nil && cmd.Parent().Name() == "auth")
	if !read && !mutate {
		return
	}
	run := cmd.RunE
	if mutate {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			store, err := a.stateStore()
			if err != nil {
				return err
			}
			marker := conversationCacheGenerationPath(store.Dir, provider)
			if err := invalidateConversationCache(marker); err != nil {
				return fmt.Errorf("invalidate conversation cache before mutation: %w", err)
			}
			// Invalidation is also required after uncertain dispatch; never retry a mutation.
			defer func() {
				if err := invalidateConversationCache(marker); err != nil {
					fmt.Fprintln(a.err, "warning: conversation cache invalidation failed; use --fresh for reads")
				}
			}()
			return run(cmd, args)
		}
		return
	}
	cmd.Flags().Bool("fresh", false, "bypass the shared 30-second conversation read cache")
	if cmd.Long == "" {
		cmd.Long = cmd.Short
	}
	cmd.Long += "\nSuccessful lists and completed details are cached for 30 seconds across CLI invocations. Use --fresh to force a provider read; await always polls fresh."
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.commandContextWithDefault(cmd, 45*time.Second)
		defer cancel()
		store, err := a.stateStore()
		if err != nil {
			return err
		}
		key, err := a.conversationCacheKey(cmd, args, provider, store.Dir)
		if err != nil {
			return err
		}
		lockPath := filepath.Join(store.Dir, "webagent", "read-cache", string(provider)+".lock")
		lock, err := artifacts.AcquireOwnerOnlyFileLock(ctx, lockPath)
		if err != nil {
			if ctx.Err() != nil {
				return commandError("conversation_cache_busy", "timeout", "conversation read cache is busy; no provider request was made", ExitTimeout, nil)
			}
			return fmt.Errorf("acquire conversation cache lock: %w", err)
		}
		defer lock.Release()
		path := filepath.Join(store.Dir, "webagent", "read-cache", string(provider)+".json")
		generation, err := readConversationCacheGeneration(conversationCacheGenerationPath(store.Dir, provider))
		if err != nil {
			return err
		}
		entries := loadConversationCache(path)
		fresh, _ := cmd.Flags().GetBool("fresh")
		now := time.Now().UTC()
		operation := webagent.OperationConversationsList
		if cmd.Name() == "detail" {
			operation = webagent.OperationConversationsDetail
		}
		if entry, ok := entries[key]; ok && !fresh && entry.Generation == generation && entry.Result.Provider == provider && entry.Result.Operation == operation && conversationCacheValid(entry, now) {
			result := entry.Result
			result.Evidence = webagent.Evidence{RunID: webagent.NewRunID(), BuildCommit: entry.Result.Evidence.BuildCommit, BrowserMode: "none", ReadMode: "cache", Cache: &webagent.ReadCacheEvidence{CapturedAt: entry.CapturedAt.Format(time.RFC3339Nano), AgeMS: now.Sub(entry.CapturedAt).Milliseconds(), TTLSeconds: 30, SourceRunID: entry.Result.Evidence.RunID}}
			result.Cleanup = webagent.CleanupEvidence{State: webagent.CleanupNotRequired}
			result.Action = nil
			return a.renderWebAgentResult(ctx, entry.Human, result)
		}
		// --fresh must discard the old entry even if the new read fails.
		delete(entries, key)
		for k, entry := range entries {
			if entry.Generation != generation || !conversationCacheValid(entry, now) {
				delete(entries, k)
			}
		}
		if len(entries) >= 64 {
			entries = make(map[string]conversationCacheEntry)
		}
		if err := saveConversationCache(path, entries); err != nil {
			return err
		}
		capture := &conversationCacheCapture{path: path, key: key, generation: generation, entries: entries, provider: provider, operation: operation}
		old := cmd.Context()
		cmd.SetContext(context.WithValue(ctx, conversationCacheContextKey{}, capture))
		defer cmd.SetContext(old)
		return run(cmd, args)
	}
}

func conversationCacheGenerationPath(root string, provider webagent.Provider) string {
	return filepath.Join(root, "webagent", "read-cache", string(provider)+".generation")
}
func invalidateConversationCache(path string) error {
	return artifacts.WriteOwnerOnlyFileAtomic(path, []byte(webagent.NewRunID()))
}
func readConversationCacheGeneration(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(b), err
}
func conversationCacheValid(entry conversationCacheEntry, now time.Time) bool {
	age := now.Sub(entry.CapturedAt)
	return age >= 0 && age < conversationCacheTTL && entry.Result.Evidence.Cache == nil && entry.Result.Cleanup.State != webagent.CleanupFailed && entry.Result.Cleanup.State != webagent.CleanupPending && entry.Result.OK && (entry.Result.Operation == webagent.OperationConversationsList && entry.Result.State == webagent.StateReady || entry.Result.Operation == webagent.OperationConversationsDetail && entry.Result.State == webagent.StateTerminal) && entry.Result.Validate() == nil
}
func loadConversationCache(path string) map[string]conversationCacheEntry {
	entries := make(map[string]conversationCacheEntry)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > conversationCacheMaxBytes {
		return entries
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return entries
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if decoder.Decode(&entries) != nil || entries == nil {
		return make(map[string]conversationCacheEntry)
	}
	return entries
}
func saveConversationCache(path string, entries map[string]conversationCacheEntry) error {
	b, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	if len(b) > conversationCacheMaxBytes {
		b = []byte("{}")
	}
	return artifacts.WriteOwnerOnlyFileAtomic(path, b)
}
func captureConversationRead(ctx context.Context, human string, result webagent.Result) {
	capture, ok := ctx.Value(conversationCacheContextKey{}).(*conversationCacheCapture)
	if !ok {
		return
	}
	entry := conversationCacheEntry{CapturedAt: time.Now().UTC(), Generation: capture.generation, Human: human, Result: result}
	if result.Provider != capture.provider || result.Operation != capture.operation || !conversationCacheValid(entry, entry.CapturedAt) {
		return
	}
	capture.entries[capture.key] = entry
	// A failed optional cache write must not turn a successful provider read into a retry.
	_ = saveConversationCache(capture.path, capture.entries)
}
func (a *app) conversationCacheKey(cmd *cobra.Command, args []string, provider webagent.Provider, root string) (string, error) {
	if args == nil {
		args = []string{}
	}
	flags := map[string]string{}
	cmd.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Name != "fresh" {
			flags[flag.Name] = flag.Value.String()
		}
	})
	mode, err := a.resolveBrowserMode(cmd)
	if err != nil {
		return "", err
	}
	scope := []any{provider, cmd.Name(), args, flags, a.opts.profile, mode.Mode, a.opts.connection, a.opts.browserURL, a.opts.userDataDir, a.opts.autoConnect, a.opts.channel, a.opts.fingerprintProfile, a.build}
	// Changes to config or owner-only provider templates select a new cache namespace.
	connectionState, err := os.ReadFile(filepath.Join(root, "connections.json"))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	connectionDigest := sha256.Sum256(connectionState)
	scope = append(scope, fmt.Sprintf("%x", connectionDigest))
	configPath, err := config.ResolvePath(a.opts.config)
	if err != nil {
		return "", err
	}
	{
		b, err := os.ReadFile(configPath)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		sum := sha256.Sum256(b)
		scope = append(scope, fmt.Sprintf("%x", sum))
	}
	paths, err := filepath.Glob(filepath.Join(root, "webagent", string(provider), "*.json"))
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		scope = append(scope, []any{filepath.Base(path), info.Size(), info.ModTime().UnixNano()})
	}
	b, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum), nil
}
