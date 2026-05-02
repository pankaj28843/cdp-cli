package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func (a *app) newStorageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Inspect and mutate browser application storage",
	}
	cmd.AddCommand(a.newStorageListCommand())
	cmd.AddCommand(a.newStorageGetCommand())
	cmd.AddCommand(a.newStorageSetCommand())
	cmd.AddCommand(a.newStorageDeleteCommand())
	cmd.AddCommand(a.newStorageClearCommand())
	cmd.AddCommand(a.newStorageSnapshotCommand())
	cmd.AddCommand(a.newStorageDiffCommand())
	cmd.AddCommand(a.newStorageCookiesCommand())
	cmd.AddCommand(a.newStorageIndexedDBCommand())
	cmd.AddCommand(a.newStorageCacheCommand())
	cmd.AddCommand(a.newStorageServiceWorkersCommand())
	return cmd
}

func (a *app) newStorageListCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var include string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List localStorage, sessionStorage, cookies, and quota for a page",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			includeSet, err := parseStorageInclude(include)
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			snapshot, collectorErrors, err := collectStorageSnapshot(ctx, session, target, includeSet)
			if err != nil {
				return err
			}
			report := map[string]any{
				"ok":               true,
				"target":           pageRow(target),
				"storage":          snapshot,
				"collector_errors": collectorErrors,
			}
			human := fmt.Sprintf("storage\tlocal:%d\tsession:%d\tcookies:%d", snapshot.LocalStorage.Count, snapshot.SessionStorage.Count, len(snapshot.Cookies))
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&include, "include", "localStorage,sessionStorage,cookies,quota", "comma-separated storage areas: localStorage,sessionStorage,cookies,indexeddb,cache,serviceWorkers,quota,all")
	return cmd
}

func (a *app) newStorageGetCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "get <localStorage|sessionStorage> <key>",
		Short: "Read one Web Storage value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := normalizeWebStorageBackend(args[0])
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runWebStorageOperation(ctx, session, "get", backend, args[1], "")
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("%s\t%s\tfound=%t", result.Backend, result.Key, result.Found)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	return cmd
}

func (a *app) newStorageSetCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "set <localStorage|sessionStorage> <key> <value|@file>",
		Short: "Set one Web Storage value",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := normalizeWebStorageBackend(args[0])
			if err != nil {
				return err
			}
			value, source, err := readStorageValueInput(args[2])
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runWebStorageOperation(ctx, session, "set", backend, args[1], value)
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result, "value_source": source}
			human := fmt.Sprintf("%s\t%s\tset", result.Backend, result.Key)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	return cmd
}

func (a *app) newStorageDeleteCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:     "delete <localStorage|sessionStorage> <key>",
		Aliases: []string{"rm"},
		Short:   "Delete one Web Storage value",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := normalizeWebStorageBackend(args[0])
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runWebStorageOperation(ctx, session, "delete", backend, args[1], "")
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("%s\t%s\tdeleted=%t", result.Backend, result.Key, result.Found)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	return cmd
}

func (a *app) newStorageClearCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "clear <localStorage|sessionStorage>",
		Short: "Clear one Web Storage area",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := normalizeWebStorageBackend(args[0])
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runWebStorageOperation(ctx, session, "clear", backend, "", "")
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("%s\tcleared=%d", result.Backend, result.Cleared)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	return cmd
}

func (a *app) newStorageSnapshotCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var include string
	var outPath string
	var redact string
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Write a local forensic storage snapshot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			includeSet, err := parseStorageInclude(include)
			if err != nil {
				return err
			}
			redact = strings.ToLower(strings.TrimSpace(redact))
			if redact == "" {
				redact = "none"
			}
			if redact != "none" && redact != "safe" {
				return commandError("usage", "usage", "--redact must be none or safe", ExitUsage, []string{"cdp storage snapshot --redact safe --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			snapshot, collectorErrors, err := collectStorageSnapshot(ctx, session, target, includeSet)
			if err != nil {
				return err
			}
			applyStorageRedaction(&snapshot, redact)
			meta := map[string]any{
				"include":          setKeys(includeSet),
				"redact":           redact,
				"collector_errors": collectorErrors,
			}
			if strings.TrimSpace(outPath) != "" && redact == "none" {
				meta["local_artifact_warning"] = "storage snapshot may include cookies, tokens, localStorage values, and sessionStorage values; keep this artifact local"
			}
			report := map[string]any{
				"ok":       true,
				"target":   pageRow(target),
				"snapshot": snapshot,
				"storage":  meta,
			}
			if strings.TrimSpace(outPath) != "" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal storage snapshot: %v", err), ExitInternal, []string{"cdp storage snapshot --json"})
				}
				writtenPath, err := writeArtifactFile(outPath, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "storage-snapshot", "path": writtenPath, "bytes": len(b) + 1}
				report["artifacts"] = []map[string]any{{"type": "storage-snapshot", "path": writtenPath}}
			}
			human := fmt.Sprintf("storage-snapshot\tlocal:%d\tsession:%d\tcookies:%d", snapshot.LocalStorage.Count, snapshot.SessionStorage.Count, len(snapshot.Cookies))
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&include, "include", "localStorage,sessionStorage,cookies,quota", "comma-separated storage areas: localStorage,sessionStorage,cookies,indexeddb,cache,serviceWorkers,quota,all")
	cmd.Flags().StringVar(&outPath, "out", "", "optional path for the JSON storage snapshot artifact")
	cmd.Flags().StringVar(&redact, "redact", "none", "redaction preset for output and artifacts: none or safe")
	return cmd
}

func (a *app) newStorageDiffCommand() *cobra.Command {
	var leftPath string
	var rightPath string
	cmd := &cobra.Command{
		Use:   "diff --left before.json --right after.json",
		Short: "Diff two storage snapshot artifacts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(leftPath) == "" || strings.TrimSpace(rightPath) == "" {
				return commandError("usage", "usage", "--left and --right are required", ExitUsage, []string{"cdp storage diff --left before.local.json --right after.local.json --json"})
			}
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			left, err := readStorageSnapshotFile(leftPath)
			if err != nil {
				return commandError("usage", "usage", fmt.Sprintf("read --left snapshot: %v", err), ExitUsage, []string{"cdp storage snapshot --out before.local.json --json"})
			}
			right, err := readStorageSnapshotFile(rightPath)
			if err != nil {
				return commandError("usage", "usage", fmt.Sprintf("read --right snapshot: %v", err), ExitUsage, []string{"cdp storage snapshot --out after.local.json --json"})
			}
			diff := diffStorageSnapshots(left, right)
			report := map[string]any{
				"ok":       true,
				"left":     leftPath,
				"right":    rightPath,
				"diff":     diff,
				"has_diff": storageDiffHasChanges(diff),
			}
			human := fmt.Sprintf("storage-diff\tchanged=%t", storageDiffHasChanges(diff))
			return a.render(ctx, human, report)
		},
	}
	cmd.Flags().StringVar(&leftPath, "left", "", "left/before storage snapshot JSON path")
	cmd.Flags().StringVar(&rightPath, "right", "", "right/after storage snapshot JSON path")
	return cmd
}

func (a *app) newStorageCookiesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cookies",
		Short: "List, set, and delete cookies",
	}
	cmd.AddCommand(a.newStorageCookiesListCommand())
	cmd.AddCommand(a.newStorageCookiesSetCommand())
	cmd.AddCommand(a.newStorageCookiesDeleteCommand())
	return cmd
}

func (a *app) newStorageCookiesListCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var rawURL string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cookies for a URL or selected page",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			cookieURL, err := storageCommandURL(ctx, session, target, rawURL)
			if err != nil {
				return err
			}
			cookies, err := getStorageCookies(ctx, session, cookieURL)
			if err != nil {
				return storageCommandFailed("list cookies", target.TargetID, err)
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "url": cookieURL, "cookies": cookies, "storage": map[string]any{"count": len(cookies), "names": cookieNames(cookies)}}
			human := fmt.Sprintf("cookies\t%d", len(cookies))
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&rawURL, "url", "", "URL whose applicable cookies should be listed; defaults to selected page URL")
	return cmd
}

func (a *app) newStorageCookiesSetCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var rawURL string
	var name string
	var value string
	var domain string
	var path string
	var secure bool
	var httpOnly bool
	var sameSite string
	var expires float64
	cmd := &cobra.Command{
		Use:   "set --name <name> --value <value>",
		Short: "Set one cookie",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(name) == "" {
				return commandError("usage", "usage", "--name is required", ExitUsage, []string{"cdp storage cookies set --name feature_flag --value enabled --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			cookieURL, err := storageCommandURL(ctx, session, target, rawURL)
			if err != nil {
				return err
			}
			params := map[string]any{"name": name, "value": value, "url": cookieURL}
			if strings.TrimSpace(domain) != "" {
				params["domain"] = domain
			}
			if strings.TrimSpace(path) != "" {
				params["path"] = path
			}
			if secure {
				params["secure"] = true
			}
			if httpOnly {
				params["httpOnly"] = true
			}
			if strings.TrimSpace(sameSite) != "" {
				params["sameSite"] = sameSite
			}
			if expires > 0 {
				params["expires"] = expires
			}
			var result map[string]any
			if err := execSessionJSON(ctx, session, "Network.setCookie", params, &result); err != nil {
				return storageCommandFailed("set cookie", target.TargetID, err)
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "url": cookieURL, "cookie": map[string]any{"name": name, "domain": domain, "path": path}, "result": result}
			human := fmt.Sprintf("cookie\t%s\tset", name)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&rawURL, "url", "", "URL to associate with the cookie; defaults to selected page URL")
	cmd.Flags().StringVar(&name, "name", "", "cookie name")
	cmd.Flags().StringVar(&value, "value", "", "cookie value")
	cmd.Flags().StringVar(&domain, "domain", "", "cookie domain")
	cmd.Flags().StringVar(&path, "path", "", "cookie path")
	cmd.Flags().BoolVar(&secure, "secure", false, "mark the cookie secure")
	cmd.Flags().BoolVar(&httpOnly, "http-only", false, "mark the cookie HTTP-only")
	cmd.Flags().StringVar(&sameSite, "same-site", "", "cookie SameSite value: Strict, Lax, or None")
	cmd.Flags().Float64Var(&expires, "expires", 0, "cookie expiration as seconds since Unix epoch; 0 creates a session cookie")
	return cmd
}

func (a *app) newStorageCookiesDeleteCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var rawURL string
	var name string
	var domain string
	var path string
	cmd := &cobra.Command{
		Use:     "delete --name <name>",
		Aliases: []string{"rm"},
		Short:   "Delete matching cookies",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(name) == "" {
				return commandError("usage", "usage", "--name is required", ExitUsage, []string{"cdp storage cookies delete --name feature_flag --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			cookieURL, err := storageCommandURL(ctx, session, target, rawURL)
			if err != nil {
				return err
			}
			params := map[string]any{"name": name}
			if strings.TrimSpace(domain) != "" || strings.TrimSpace(path) != "" {
				if strings.TrimSpace(domain) != "" {
					params["domain"] = domain
				}
				if strings.TrimSpace(path) != "" {
					params["path"] = path
				}
			} else {
				params["url"] = cookieURL
			}
			if err := execSessionJSON(ctx, session, "Network.deleteCookies", params, nil); err != nil {
				return storageCommandFailed("delete cookie", target.TargetID, err)
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "url": cookieURL, "cookie": map[string]any{"name": name, "domain": domain, "path": path}}
			human := fmt.Sprintf("cookie\t%s\tdeleted", name)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&rawURL, "url", "", "URL whose matching cookie should be deleted; defaults to selected page URL")
	cmd.Flags().StringVar(&name, "name", "", "cookie name")
	cmd.Flags().StringVar(&domain, "domain", "", "cookie domain")
	cmd.Flags().StringVar(&path, "path", "", "cookie path")
	return cmd
}

func (a *app) newStorageIndexedDBCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "indexeddb",
		Short: "List, read, write, delete, and clear IndexedDB records",
	}
	cmd.AddCommand(a.newStorageIndexedDBListCommand())
	cmd.AddCommand(a.newStorageIndexedDBGetCommand())
	cmd.AddCommand(a.newStorageIndexedDBPutCommand())
	cmd.AddCommand(a.newStorageIndexedDBDumpCommand())
	cmd.AddCommand(a.newStorageIndexedDBDeleteCommand())
	cmd.AddCommand(a.newStorageIndexedDBClearCommand())
	return cmd
}

func (a *app) newStorageIndexedDBListCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List IndexedDB databases, object stores, indexes, and counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runIndexedDBOperation(ctx, session, indexedDBListExpression())
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("indexeddb\tdatabases=%d", len(result.Databases))
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	return cmd
}

func (a *app) newStorageIndexedDBGetCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var keyJSON bool
	cmd := &cobra.Command{
		Use:   "get <database> <store> <key>",
		Short: "Read one IndexedDB record",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runIndexedDBOperation(ctx, session, indexedDBGetExpression(args[0], args[1], args[2], keyJSON))
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("indexeddb\t%s/%s\tfound=%t", result.Database, result.Store, result.Found)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().BoolVar(&keyJSON, "key-json", false, "parse <key> as JSON instead of using it as a string")
	return cmd
}

func (a *app) newStorageIndexedDBPutCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var keyJSON bool
	cmd := &cobra.Command{
		Use:   "put <database> <store> <key> <value|@file>",
		Short: "Create or replace one IndexedDB record",
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			value, source, err := readStorageValueInput(args[3])
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runIndexedDBOperation(ctx, session, indexedDBPutExpression(args[0], args[1], args[2], value, keyJSON))
			if err != nil {
				return err
			}
			result.ValueSource = source
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("indexeddb\t%s/%s\tput", result.Database, result.Store)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().BoolVar(&keyJSON, "key-json", false, "parse <key> as JSON instead of using it as a string")
	return cmd
}

func (a *app) newStorageIndexedDBDumpCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var limit int
	var offset int
	var pageSize int
	var cursor string
	var direction string
	var keysOnly bool
	var valuesOnly bool
	var outPath string
	cmd := &cobra.Command{
		Use:   "dump <database> <store>",
		Short: "Dump a bounded range of IndexedDB records",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			direction = strings.ToLower(strings.TrimSpace(direction))
			if direction == "" {
				direction = "next"
			}
			if direction != "next" && direction != "nextunique" && direction != "prev" && direction != "prevunique" {
				return commandError("usage", "usage", "--direction must be next, nextunique, prev, or prevunique", ExitUsage, []string{"cdp storage indexeddb dump app records --direction next --json"})
			}
			if limit < 0 || offset < 0 || pageSize < 0 {
				return commandError("usage", "usage", "--limit, --offset, and --page-size must be non-negative", ExitUsage, []string{"cdp storage indexeddb dump app records --limit 100 --json"})
			}
			if keysOnly && valuesOnly {
				return commandError("usage", "usage", "--keys-only and --values-only cannot be combined", ExitUsage, []string{"cdp storage indexeddb dump app records --keys-only --json"})
			}
			if pageSize > 0 {
				limit = pageSize
			}
			if limit == 0 {
				limit = 500
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runIndexedDBOperation(ctx, session, indexedDBDumpExpression(args[0], args[1], indexedDBDumpOptions{
				Limit:      limit,
				Offset:     offset,
				PageSize:   pageSize,
				Cursor:     cursor,
				Direction:  direction,
				KeysOnly:   keysOnly,
				ValuesOnly: valuesOnly,
			}))
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			if strings.TrimSpace(outPath) != "" {
				b, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return commandError("internal", "internal", fmt.Sprintf("marshal indexeddb dump report: %v", err), ExitInternal, []string{"cdp storage indexeddb dump app records --json"})
				}
				writtenPath, err := writeArtifactFile(outPath, append(b, '\n'))
				if err != nil {
					return err
				}
				report["artifact"] = map[string]any{"type": "storage-indexeddb-dump", "path": writtenPath, "bytes": len(b) + 1}
				report["artifacts"] = []map[string]any{{"type": "storage-indexeddb-dump", "path": writtenPath}}
				report["local_artifact_warning"] = "IndexedDB dump may include application data and local records; keep this artifact local"
			}
			human := fmt.Sprintf("indexeddb-dump\t%s/%s\trecords=%d", result.Database, result.Store, result.Count)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().IntVar(&limit, "limit", 500, "maximum records to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "number of records to skip before returning results")
	cmd.Flags().IntVar(&pageSize, "page-size", 0, "page size for cursor-style pagination; overrides --limit when set")
	cmd.Flags().StringVar(&cursor, "cursor", "", "opaque cursor returned by a previous dump page")
	cmd.Flags().StringVar(&direction, "direction", "next", "cursor direction: next, nextunique, prev, or prevunique")
	cmd.Flags().BoolVar(&keysOnly, "keys-only", false, "include keys without values")
	cmd.Flags().BoolVar(&valuesOnly, "values-only", false, "include values without keys")
	cmd.Flags().StringVar(&outPath, "out", "", "optional path for the JSON IndexedDB dump artifact")
	return cmd
}

func (a *app) newStorageIndexedDBDeleteCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var keyJSON bool
	cmd := &cobra.Command{
		Use:     "delete <database> <store> <key>",
		Aliases: []string{"rm"},
		Short:   "Delete one IndexedDB record",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runIndexedDBOperation(ctx, session, indexedDBDeleteExpression(args[0], args[1], args[2], keyJSON))
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("indexeddb\t%s/%s\tdeleted=%t", result.Database, result.Store, result.Deleted)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().BoolVar(&keyJSON, "key-json", false, "parse <key> as JSON instead of using it as a string")
	return cmd
}

func (a *app) newStorageIndexedDBClearCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "clear <database> <store>",
		Short: "Clear one IndexedDB object store",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runIndexedDBOperation(ctx, session, indexedDBClearExpression(args[0], args[1]))
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("indexeddb\t%s/%s\tcleared=%d", result.Database, result.Store, result.Cleared)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	return cmd
}

func (a *app) newStorageCacheCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "List, read, write, delete, and clear Cache Storage entries",
	}
	cmd.AddCommand(a.newStorageCacheListCommand())
	cmd.AddCommand(a.newStorageCacheGetCommand())
	cmd.AddCommand(a.newStorageCachePutCommand())
	cmd.AddCommand(a.newStorageCacheDeleteCommand())
	cmd.AddCommand(a.newStorageCacheClearCommand())
	return cmd
}

func (a *app) newStorageCacheListCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var cacheName string
	var requestURLContains string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Cache Storage caches and request metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runCacheStorageOperation(ctx, session, cacheStorageListExpression(cacheName, requestURLContains))
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("cache-storage\tcaches=%d\trequests=%d", len(result.Caches), result.RequestCount)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&cacheName, "cache", "", "limit output to one Cache Storage cache name")
	cmd.Flags().StringVar(&requestURLContains, "request-url-contains", "", "only include cached requests whose URL contains this text")
	return cmd
}

func (a *app) newStorageCacheGetCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var maxBodyBytes int
	cmd := &cobra.Command{
		Use:   "get <cache> <request-url>",
		Short: "Read one Cache Storage response",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if maxBodyBytes < 0 {
				return commandError("usage", "usage", "--max-body-bytes must be non-negative", ExitUsage, []string{"cdp storage cache get app-cache https://example.com/api --max-body-bytes 4096 --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runCacheStorageOperation(ctx, session, cacheStorageGetExpression(args[0], args[1], maxBodyBytes))
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("cache-storage\t%s\tfound=%t", result.Cache, result.Found)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().IntVar(&maxBodyBytes, "max-body-bytes", 4096, "maximum cached response body bytes to include inline")
	return cmd
}

func (a *app) newStorageCachePutCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var contentType string
	var status int
	cmd := &cobra.Command{
		Use:   "put <cache> <request-url> <body|@file>",
		Short: "Create or replace one Cache Storage response",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if status < 200 || status > 599 {
				return commandError("usage", "usage", "--status must be between 200 and 599", ExitUsage, []string{"cdp storage cache put app-cache https://example.com/api '{\"ok\":true}' --status 200 --json"})
			}
			body, source, err := readStorageValueInput(args[2])
			if err != nil {
				return err
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runCacheStorageOperation(ctx, session, cacheStoragePutExpression(args[0], args[1], body, contentType, status))
			if err != nil {
				return err
			}
			result.BodySource = source
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("cache-storage\t%s\tput\t%s", result.Cache, result.RequestURL)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&contentType, "content-type", "text/plain; charset=utf-8", "Content-Type header for the cached response")
	cmd.Flags().IntVar(&status, "status", 200, "HTTP status for the cached response")
	return cmd
}

func (a *app) newStorageCacheDeleteCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:     "delete <cache> <request-url>",
		Aliases: []string{"rm"},
		Short:   "Delete one Cache Storage request entry",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runCacheStorageOperation(ctx, session, cacheStorageDeleteExpression(args[0], args[1]))
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("cache-storage\t%s\tdeleted=%t", result.Cache, result.Deleted)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	return cmd
}

func (a *app) newStorageCacheClearCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var all bool
	cmd := &cobra.Command{
		Use:   "clear [cache]",
		Short: "Delete one Cache Storage cache or all caches",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cacheName := ""
			if len(args) > 0 {
				cacheName = args[0]
			}
			if strings.TrimSpace(cacheName) == "" && !all {
				return commandError("usage", "usage", "cache name or --all is required", ExitUsage, []string{"cdp storage cache clear app-cache --json", "cdp storage cache clear --all --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runCacheStorageOperation(ctx, session, cacheStorageClearExpression(cacheName, all))
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("cache-storage\tcleared=%d", len(result.Cleared))
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().BoolVar(&all, "all", false, "delete every Cache Storage cache for the selected origin")
	return cmd
}

func (a *app) newStorageServiceWorkersCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "service-workers",
		Aliases: []string{"service-worker", "sw"},
		Short:   "List and unregister service workers for the selected origin",
	}
	cmd.AddCommand(a.newStorageServiceWorkersListCommand())
	cmd.AddCommand(a.newStorageServiceWorkersUnregisterCommand())
	return cmd
}

func (a *app) newStorageServiceWorkersListCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List service worker registrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runServiceWorkerOperation(ctx, session, serviceWorkerListExpression())
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("service-workers\t%d", result.Count)
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	return cmd
}

func (a *app) newStorageServiceWorkersUnregisterCommand() *cobra.Command {
	var targetID string
	var urlContains string
	var titleContains string
	var scope string
	var all bool
	cmd := &cobra.Command{
		Use:   "unregister",
		Short: "Unregister one service worker scope or every scope",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(scope) == "" && !all {
				return commandError("usage", "usage", "--scope or --all is required", ExitUsage, []string{"cdp storage service-workers unregister --scope https://example.com/ --json", "cdp storage service-workers unregister --all --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Second)
			defer cancel()
			session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)
			result, err := runServiceWorkerOperation(ctx, session, serviceWorkerUnregisterExpression(scope, all))
			if err != nil {
				return err
			}
			report := map[string]any{"ok": true, "target": pageRow(target), "storage": result}
			human := fmt.Sprintf("service-workers\tunregistered=%d", len(result.Unregistered))
			return a.render(ctx, human, report)
		},
	}
	addStorageTargetFlags(cmd, &targetID, &urlContains)
	cmd.Flags().StringVar(&scope, "scope", "", "service worker registration scope URL to unregister")
	cmd.Flags().BoolVar(&all, "all", false, "unregister every service worker registration for the selected origin")
	return cmd
}
