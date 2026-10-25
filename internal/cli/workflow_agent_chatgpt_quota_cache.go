package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/chatgpt"
	"github.com/spf13/cobra"
)

const quotaCacheTTL = 120 * time.Second

type quotaCacheEntry struct {
	Scope          string        `json:"scope"`
	Snapshot       quotaSnapshot `json:"snapshot"`
	ResetInSeconds int64         `json:"reset_in_seconds"`
}

func quotaCachePath(root string) string {
	return filepath.Join(root, "webagent", "read-cache", "chatgpt-quota.json")
}

func quotaCacheScope(a *app, cmd *cobra.Command, root string, template chatgpt.RequestTemplate) (string, error) {
	runtimeScope, err := a.conversationCacheScope(cmd, webagent.ProviderChatGPT, root)
	if err != nil {
		return "", err
	}
	secret := sha256.Sum256([]byte(template.Headers["authorization"] + "\x00" + template.CookieHeader + "\x00" + template.Headers["chatgpt-account-id"] + "\x00" + template.BrowserUserAgent + "\x00" + template.CapturedAt))
	combined := sha256.Sum256([]byte(runtimeScope + fmt.Sprintf("%x", secret)))
	return fmt.Sprintf("%x", combined), nil
}

func loadQuotaCache(path, scope string, now time.Time) (quotaSnapshot, time.Duration, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
		return quotaSnapshot{}, 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return quotaSnapshot{}, 0, false
	}
	var entry quotaCacheEntry
	if json.Unmarshal(data, &entry) != nil || entry.Scope != scope || !entry.Snapshot.OK ||
		entry.Snapshot.SchemaVersion != chatgptQuotaSchemaVersion || entry.Snapshot.QuotaScope != chatgptQuotaScope ||
		entry.Snapshot.Source != "direct_chatgpt_usage_api_weekly_window" || entry.Snapshot.SourceURL != chatgpt.Origin+chatgpt.UsagePath ||
		entry.Snapshot.RemainingPercent < 0 || entry.Snapshot.RemainingPercent > 100 ||
		entry.Snapshot.ResetAt == nil || entry.Snapshot.ObservedAt.IsZero() || entry.Snapshot.CacheHit || entry.Snapshot.Guard != nil ||
		entry.ResetInSeconds <= 0 || entry.ResetInSeconds > 7*24*60*60 {
		return quotaSnapshot{}, 0, false
	}
	age := now.Sub(entry.Snapshot.ObservedAt)
	if age < 0 || age >= quotaCacheTTL || !now.Before(*entry.Snapshot.ResetAt) {
		return quotaSnapshot{}, 0, false
	}
	snapshot := entry.Snapshot
	snapshot.ResetDisplay = quotaResetDisplay(snapshot.ResetAt.Sub(now))
	snapshot.CacheHit = true
	snapshot.CacheAgeMS = age.Milliseconds()
	snapshot.CacheTTLSeconds = int(quotaCacheTTL / time.Second)
	return snapshot, time.Duration(entry.ResetInSeconds) * time.Second, true
}

func saveQuotaCache(path, scope string, snapshot quotaSnapshot, resetIn time.Duration) error {
	entry := quotaCacheEntry{Scope: scope, Snapshot: snapshot, ResetInSeconds: int64(resetIn / time.Second)}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return artifacts.WriteOwnerOnlyFileAtomic(path, data)
}

func acquireQuotaCacheLock(ctx context.Context, root string) (*artifacts.OwnerOnlyFileLock, error) {
	return artifacts.AcquireOwnerOnlyFileLock(ctx, filepath.Join(root, "webagent", "read-cache", "chatgpt-quota.lock"))
}
