package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

type storageSnapshot struct {
	URL            string                      `json:"url,omitempty"`
	Origin         string                      `json:"origin,omitempty"`
	LocalStorage   storageAreaSnapshot         `json:"local_storage"`
	SessionStorage storageAreaSnapshot         `json:"session_storage"`
	Cookies        []map[string]any            `json:"cookies,omitempty"`
	IndexedDB      []indexedDBDatabase         `json:"indexeddb,omitempty"`
	CacheStorage   []cacheStorageCache         `json:"cache_storage,omitempty"`
	ServiceWorkers []serviceWorkerRegistration `json:"service_workers,omitempty"`
	Quota          map[string]any              `json:"quota,omitempty"`
	Dump           *indexedDBOperationResult   `json:"indexeddb_dump,omitempty"`
}

type storageAreaSnapshot struct {
	Count   int            `json:"count"`
	Keys    []string       `json:"keys"`
	Entries []storageEntry `json:"entries"`
	Error   string         `json:"error,omitempty"`
}

type storageEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Bytes int    `json:"bytes"`
}

type webStorageBackend struct {
	JSName string
	Output string
}

type webStorageOperationResult struct {
	URL      string `json:"url,omitempty"`
	Origin   string `json:"origin,omitempty"`
	Backend  string `json:"backend"`
	Key      string `json:"key,omitempty"`
	Value    string `json:"value,omitempty"`
	Found    bool   `json:"found,omitempty"`
	Bytes    int    `json:"bytes,omitempty"`
	Cleared  int    `json:"cleared,omitempty"`
	Previous string `json:"previous,omitempty"`
}

type indexedDBOperationResult struct {
	URL         string                 `json:"url,omitempty"`
	Origin      string                 `json:"origin,omitempty"`
	Operation   string                 `json:"operation"`
	Available   bool                   `json:"available"`
	Found       bool                   `json:"found,omitempty"`
	Database    string                 `json:"database,omitempty"`
	Store       string                 `json:"store,omitempty"`
	Key         any                    `json:"key,omitempty"`
	Value       any                    `json:"value,omitempty"`
	Previous    any                    `json:"previous,omitempty"`
	KeySource   string                 `json:"key_source,omitempty"`
	ValueSource string                 `json:"value_source,omitempty"`
	Created     bool                   `json:"created,omitempty"`
	Updated     bool                   `json:"updated,omitempty"`
	Deleted     bool                   `json:"deleted,omitempty"`
	Cleared     int                    `json:"cleared,omitempty"`
	Count       int                    `json:"count"`
	Limit       int                    `json:"limit,omitempty"`
	Offset      int                    `json:"offset,omitempty"`
	PageSize    int                    `json:"page_size,omitempty"`
	Cursor      string                 `json:"cursor,omitempty"`
	NextCursor  string                 `json:"next_cursor,omitempty"`
	HasMore     bool                   `json:"has_more,omitempty"`
	Direction   string                 `json:"direction,omitempty"`
	Records     []indexedDBRecord      `json:"records,omitempty"`
	Databases   []indexedDBDatabase    `json:"databases,omitempty"`
	Stores      []indexedDBObjectStore `json:"stores,omitempty"`
}

type indexedDBRecord struct {
	Key   any `json:"key,omitempty"`
	Value any `json:"value,omitempty"`
}

type indexedDBDatabase struct {
	Name    string                 `json:"name"`
	Version int                    `json:"version,omitempty"`
	Stores  []indexedDBObjectStore `json:"stores"`
	Error   string                 `json:"error,omitempty"`
}

type indexedDBObjectStore struct {
	Name          string           `json:"name"`
	KeyPath       any              `json:"key_path,omitempty"`
	AutoIncrement bool             `json:"auto_increment,omitempty"`
	Count         int              `json:"count"`
	Indexes       []indexedDBIndex `json:"indexes,omitempty"`
	Error         string           `json:"error,omitempty"`
}

type indexedDBIndex struct {
	Name       string `json:"name"`
	KeyPath    any    `json:"key_path,omitempty"`
	Unique     bool   `json:"unique,omitempty"`
	MultiEntry bool   `json:"multi_entry,omitempty"`
}

type cacheStorageOperationResult struct {
	URL          string                `json:"url,omitempty"`
	Origin       string                `json:"origin,omitempty"`
	Operation    string                `json:"operation"`
	Available    bool                  `json:"available"`
	Found        bool                  `json:"found,omitempty"`
	Cache        string                `json:"cache,omitempty"`
	RequestURL   string                `json:"request_url,omitempty"`
	RequestCount int                   `json:"request_count,omitempty"`
	CacheNames   []string              `json:"cache_names,omitempty"`
	Caches       []cacheStorageCache   `json:"caches,omitempty"`
	Response     *cacheStorageResponse `json:"response,omitempty"`
	Body         *cacheStorageBody     `json:"body,omitempty"`
	BodySource   string                `json:"body_source,omitempty"`
	Created      bool                  `json:"created,omitempty"`
	Updated      bool                  `json:"updated,omitempty"`
	Deleted      bool                  `json:"deleted,omitempty"`
	Cleared      []string              `json:"cleared,omitempty"`
}

type cacheStorageCache struct {
	Name     string                `json:"name"`
	Count    int                   `json:"count"`
	Requests []cacheStorageRequest `json:"requests"`
	Error    string                `json:"error,omitempty"`
}

type cacheStorageRequest struct {
	URL      string                `json:"url"`
	Method   string                `json:"method,omitempty"`
	Response *cacheStorageResponse `json:"response,omitempty"`
	Error    string                `json:"error,omitempty"`
}

type cacheStorageResponse struct {
	Status      int    `json:"status,omitempty"`
	StatusText  string `json:"status_text,omitempty"`
	Type        string `json:"type,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

type cacheStorageBody struct {
	Text     string `json:"text,omitempty"`
	Bytes    int    `json:"bytes"`
	Omitted  bool   `json:"omitted,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

type serviceWorkerOperationResult struct {
	URL           string                      `json:"url,omitempty"`
	Origin        string                      `json:"origin,omitempty"`
	Operation     string                      `json:"operation"`
	Available     bool                        `json:"available"`
	Found         bool                        `json:"found,omitempty"`
	Count         int                         `json:"count"`
	Scope         string                      `json:"scope,omitempty"`
	Registrations []serviceWorkerRegistration `json:"registrations,omitempty"`
	Unregistered  []serviceWorkerRegistration `json:"unregistered,omitempty"`
}

type serviceWorkerRegistration struct {
	ScopeURL       string             `json:"scope_url"`
	UpdateViaCache string             `json:"update_via_cache,omitempty"`
	Active         *serviceWorkerInfo `json:"active,omitempty"`
	Waiting        *serviceWorkerInfo `json:"waiting,omitempty"`
	Installing     *serviceWorkerInfo `json:"installing,omitempty"`
	Result         *bool              `json:"result,omitempty"`
}

type serviceWorkerInfo struct {
	ScriptURL string `json:"script_url,omitempty"`
	State     string `json:"state,omitempty"`
}

type storageDiffReport struct {
	LocalStorage   storageAreaDiff `json:"local_storage"`
	SessionStorage storageAreaDiff `json:"session_storage"`
	Cookies        storageAreaDiff `json:"cookies"`
	IndexedDB      storageAreaDiff `json:"indexeddb"`
	CacheStorage   storageAreaDiff `json:"cache_storage"`
	ServiceWorkers storageAreaDiff `json:"service_workers"`
	Summary        map[string]int  `json:"summary"`
}

type storageAreaDiff struct {
	Added   []storageDiffItem `json:"added"`
	Removed []storageDiffItem `json:"removed"`
	Changed []storageDiffItem `json:"changed"`
}

type storageDiffItem struct {
	Key    string `json:"key"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

func addStorageTargetFlags(cmd *cobra.Command, targetID, urlContains *string) {
	cmd.Flags().StringVar(targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(urlContains, "url-contains", "", "use the first page whose URL contains this text")
}

func parseStorageInclude(value string) (map[string]bool, error) {
	set := parseCSVSet(value)
	if len(set) == 0 {
		return defaultStorageIncludeSet(), nil
	}
	if set["all"] {
		return allStorageIncludeSet(), nil
	}
	out := map[string]bool{}
	for key := range set {
		switch strings.ToLower(key) {
		case "localstorage", "local", "local_storage":
			out["localStorage"] = true
		case "sessionstorage", "session", "session_storage":
			out["sessionStorage"] = true
		case "cookies", "cookie":
			out["cookies"] = true
		case "indexeddb", "indexed", "idb":
			out["indexedDB"] = true
		case "cache", "cachestorage", "cache_storage", "caches":
			out["cacheStorage"] = true
		case "serviceworkers", "serviceworker", "service_workers", "service-worker", "service-workers", "sw":
			out["serviceWorkers"] = true
		case "quota", "usage":
			out["quota"] = true
		default:
			return nil, commandError("usage", "usage", fmt.Sprintf("unknown storage include %q", key), ExitUsage, []string{"cdp storage list --include localStorage,sessionStorage,cookies,cache,serviceWorkers --json"})
		}
	}
	return out, nil
}

func defaultStorageIncludeSet() map[string]bool {
	return map[string]bool{"localStorage": true, "sessionStorage": true, "cookies": true, "quota": true}
}

func allStorageIncludeSet() map[string]bool {
	return map[string]bool{"localStorage": true, "sessionStorage": true, "cookies": true, "indexedDB": true, "cacheStorage": true, "serviceWorkers": true, "quota": true}
}

func normalizeWebStorageBackend(value string) (webStorageBackend, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "localstorage", "local", "local_storage":
		return webStorageBackend{JSName: "localStorage", Output: "localStorage"}, nil
	case "sessionstorage", "session", "session_storage":
		return webStorageBackend{JSName: "sessionStorage", Output: "sessionStorage"}, nil
	default:
		return webStorageBackend{}, commandError("usage", "usage", "backend must be localStorage or sessionStorage", ExitUsage, []string{"cdp storage get localStorage feature --json"})
	}
}

func collectStorageSnapshot(ctx context.Context, session *cdp.PageSession, target cdp.TargetInfo, includeSet map[string]bool) (storageSnapshot, []map[string]string, error) {
	collectorErrors := []map[string]string{}
	snapshot, err := collectWebStorageSnapshot(ctx, session)
	if err != nil {
		return storageSnapshot{}, nil, err
	}
	if snapshot.URL == "" {
		snapshot.URL = target.URL
	}
	if snapshot.Origin == "" {
		snapshot.Origin = originForURL(snapshot.URL)
	}
	if !includeSet["localStorage"] {
		snapshot.LocalStorage = storageAreaSnapshot{}
	}
	if !includeSet["sessionStorage"] {
		snapshot.SessionStorage = storageAreaSnapshot{}
	}
	if includeSet["cookies"] {
		cookies, err := getStorageCookies(ctx, session, snapshot.URL)
		if err != nil {
			collectorErrors = append(collectorErrors, collectorError("cookies", err))
		} else {
			snapshot.Cookies = cookies
		}
	}
	if includeSet["indexedDB"] {
		indexedDBResult, err := runIndexedDBOperation(ctx, session, indexedDBListExpression())
		if err != nil {
			collectorErrors = append(collectorErrors, collectorError("indexeddb", err))
		} else {
			snapshot.IndexedDB = indexedDBResult.Databases
		}
	}
	if includeSet["cacheStorage"] {
		cacheResult, err := runCacheStorageOperation(ctx, session, cacheStorageListExpression("", ""))
		if err != nil {
			collectorErrors = append(collectorErrors, collectorError("cache_storage", err))
		} else {
			snapshot.CacheStorage = cacheResult.Caches
		}
	}
	if includeSet["serviceWorkers"] {
		serviceWorkerResult, err := runServiceWorkerOperation(ctx, session, serviceWorkerListExpression())
		if err != nil {
			collectorErrors = append(collectorErrors, collectorError("service_workers", err))
		} else {
			snapshot.ServiceWorkers = serviceWorkerResult.Registrations
		}
	}
	if includeSet["quota"] && snapshot.Origin != "" {
		quota, err := getStorageQuota(ctx, session, snapshot.Origin)
		if err != nil {
			collectorErrors = append(collectorErrors, collectorError("quota", err))
		} else {
			snapshot.Quota = quota
		}
	}
	return snapshot, collectorErrors, nil
}

func collectWebStorageSnapshot(ctx context.Context, session *cdp.PageSession) (storageSnapshot, error) {
	result, err := session.Evaluate(ctx, storageSnapshotExpression(), false)
	if err != nil {
		return storageSnapshot{}, storageCommandFailed("inspect storage", session.TargetID, err)
	}
	if result.Exception != nil {
		return storageSnapshot{}, commandError("javascript_exception", "runtime", fmt.Sprintf("storage javascript exception: %s", result.Exception.Text), ExitCheckFailed, []string{"cdp storage list --json"})
	}
	var snapshot storageSnapshot
	if err := json.Unmarshal(result.Object.Value, &snapshot); err != nil {
		return storageSnapshot{}, commandError("invalid_storage_result", "runtime", fmt.Sprintf("decode storage result: %v", err), ExitCheckFailed, []string{"cdp storage list --json"})
	}
	return snapshot, nil
}

func runWebStorageOperation(ctx context.Context, session *cdp.PageSession, op string, backend webStorageBackend, key, value string) (webStorageOperationResult, error) {
	result, err := session.Evaluate(ctx, webStorageOperationExpression(op, backend.JSName, key, value), false)
	if err != nil {
		return webStorageOperationResult{}, storageCommandFailed(op+" storage", session.TargetID, err)
	}
	if result.Exception != nil {
		return webStorageOperationResult{}, commandError("javascript_exception", "runtime", fmt.Sprintf("storage javascript exception: %s", result.Exception.Text), ExitCheckFailed, []string{"cdp storage list --json"})
	}
	var opResult webStorageOperationResult
	if err := json.Unmarshal(result.Object.Value, &opResult); err != nil {
		return webStorageOperationResult{}, commandError("invalid_storage_result", "runtime", fmt.Sprintf("decode storage operation result: %v", err), ExitCheckFailed, []string{"cdp storage get localStorage feature --json"})
	}
	opResult.Backend = backend.Output
	return opResult, nil
}

func getStorageCookies(ctx context.Context, session *cdp.PageSession, rawURL string) ([]map[string]any, error) {
	var result struct {
		Cookies []map[string]any `json:"cookies"`
	}
	params := map[string]any{}
	if strings.TrimSpace(rawURL) != "" {
		params["urls"] = []string{rawURL}
	}
	if err := execSessionJSON(ctx, session, "Network.getCookies", params, &result); err != nil {
		return nil, err
	}
	if result.Cookies == nil {
		return []map[string]any{}, nil
	}
	return result.Cookies, nil
}

func getStorageQuota(ctx context.Context, session *cdp.PageSession, origin string) (map[string]any, error) {
	var quota map[string]any
	if err := execSessionJSON(ctx, session, "Storage.getUsageAndQuota", map[string]any{"origin": origin}, &quota); err != nil {
		return nil, err
	}
	return quota, nil
}

func execSessionJSON(ctx context.Context, session *cdp.PageSession, method string, params any, out any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	raw, err := session.Exec(ctx, method, b)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	return nil
}

func storageCommandURL(ctx context.Context, session *cdp.PageSession, target cdp.TargetInfo, rawURL string) (string, error) {
	if strings.TrimSpace(rawURL) != "" {
		return rawURL, nil
	}
	info, err := collectStoragePageInfo(ctx, session)
	if err == nil && info.URL != "" {
		return info.URL, nil
	}
	if strings.TrimSpace(target.URL) != "" {
		return target.URL, nil
	}
	return "", commandError("usage", "usage", "--url is required when the selected page URL is unavailable", ExitUsage, []string{"cdp storage cookies list --url https://example.com --json"})
}

func collectStoragePageInfo(ctx context.Context, session *cdp.PageSession) (storageSnapshot, error) {
	result, err := session.Evaluate(ctx, storagePageInfoExpression(), false)
	if err != nil {
		return storageSnapshot{}, err
	}
	var info storageSnapshot
	if result.Exception != nil {
		return storageSnapshot{}, fmt.Errorf("javascript exception: %s", result.Exception.Text)
	}
	if err := json.Unmarshal(result.Object.Value, &info); err != nil {
		return storageSnapshot{}, err
	}
	return info, nil
}

func storageSnapshotExpression() string {
	return `(() => {
  "__cdp_cli_storage_snapshot__";
  const bytes = (value) => new TextEncoder().encode(String(value ?? "")).length;
  const readArea = (name) => {
    try {
      const store = window[name];
      const entries = [];
      for (let i = 0; i < store.length; i++) {
        const key = store.key(i);
        const value = store.getItem(key);
        entries.push({key, value, bytes: bytes(value)});
      }
      entries.sort((a, b) => a.key.localeCompare(b.key));
      return {count: entries.length, keys: entries.map((entry) => entry.key), entries};
    } catch (error) {
      return {count: 0, keys: [], entries: [], error: String(error && error.message || error)};
    }
  };
  return {
    url: location.href,
    origin: location.origin,
    local_storage: readArea("localStorage"),
    session_storage: readArea("sessionStorage")
  };
})()`
}

func storagePageInfoExpression() string {
	return `(() => {
  "__cdp_cli_storage_page_info__";
  return {url: location.href, origin: location.origin};
})()`
}

func webStorageOperationExpression(op, area, key, value string) string {
	return fmt.Sprintf(`(() => {
  "__cdp_cli_storage_%s__";
  const store = window[%s];
  const key = %s;
  const value = %s;
  const bytes = (input) => new TextEncoder().encode(String(input ?? "")).length;
  if (%q === "get") {
    const current = store.getItem(key);
    return {url: location.href, origin: location.origin, backend: %s, key, found: current !== null, value: current ?? "", bytes: current === null ? 0 : bytes(current)};
  }
  if (%q === "set") {
    const previous = store.getItem(key);
    store.setItem(key, value);
    const current = store.getItem(key);
    return {url: location.href, origin: location.origin, backend: %s, key, found: true, value: current ?? "", previous: previous ?? "", bytes: bytes(current)};
  }
  if (%q === "delete") {
    const previous = store.getItem(key);
    store.removeItem(key);
    return {url: location.href, origin: location.origin, backend: %s, key, found: previous !== null, previous: previous ?? ""};
  }
  if (%q === "clear") {
    const cleared = store.length;
    store.clear();
    return {url: location.href, origin: location.origin, backend: %s, cleared};
  }
  throw new Error("unsupported storage operation");
})()`, op, jsStringLiteral(area), jsStringLiteral(key), jsStringLiteral(value), op, jsStringLiteral(area), op, jsStringLiteral(area), op, jsStringLiteral(area), op, jsStringLiteral(area))
}

func runIndexedDBOperation(ctx context.Context, session *cdp.PageSession, expression string) (indexedDBOperationResult, error) {
	result, err := session.Evaluate(ctx, expression, true)
	if err != nil {
		return indexedDBOperationResult{}, storageCommandFailed("inspect indexeddb", session.TargetID, err)
	}
	if result.Exception != nil {
		return indexedDBOperationResult{}, commandError("javascript_exception", "runtime", fmt.Sprintf("indexeddb javascript exception: %s", result.Exception.Text), ExitCheckFailed, []string{"cdp storage indexeddb list --json"})
	}
	var opResult indexedDBOperationResult
	if err := json.Unmarshal(result.Object.Value, &opResult); err != nil {
		return indexedDBOperationResult{}, commandError("invalid_storage_result", "runtime", fmt.Sprintf("decode indexeddb result: %v", err), ExitCheckFailed, []string{"cdp storage indexeddb list --json"})
	}
	return opResult, nil
}

func indexedDBListExpression() string {
	return `(async () => {
  "__cdp_cli_indexeddb_list__";
  if (typeof indexedDB === "undefined") {
    throw new Error("IndexedDB is not available in this page context");
  }
  if (typeof indexedDB.databases !== "function") {
    throw new Error("indexedDB.databases is not available in this browser");
  }
  const requestPromise = (request) => new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("IndexedDB request failed"));
  });
  const transactionDone = (transaction) => new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(transaction.error || new Error("IndexedDB transaction failed"));
    transaction.onabort = () => reject(transaction.error || new Error("IndexedDB transaction aborted"));
  });
  const openDB = (name) => new Promise((resolve, reject) => {
    const request = indexedDB.open(name);
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("IndexedDB open failed"));
    request.onblocked = () => reject(new Error("IndexedDB open blocked"));
  });
  const storeInfo = async (db, storeName) => {
    const transaction = db.transaction(storeName, "readonly");
    const done = transactionDone(transaction);
    const store = transaction.objectStore(storeName);
    const indexes = Array.from(store.indexNames).map((name) => {
      const index = store.index(name);
      return {name: index.name, key_path: index.keyPath, unique: index.unique, multi_entry: index.multiEntry};
    }).sort((a, b) => a.name.localeCompare(b.name));
    const count = await requestPromise(store.count());
    await done;
    return {name: store.name, key_path: store.keyPath, auto_increment: store.autoIncrement, count, indexes};
  };
  const databaseInfos = (await indexedDB.databases())
    .filter((info) => info && info.name)
    .sort((a, b) => String(a.name).localeCompare(String(b.name)));
  const databases = [];
  for (const info of databaseInfos) {
    const row = {name: info.name, version: info.version || 0, stores: []};
    let db;
    try {
      db = await openDB(info.name);
      const storeNames = Array.from(db.objectStoreNames).sort((a, b) => a.localeCompare(b));
      for (const storeName of storeNames) {
        try {
          row.stores.push(await storeInfo(db, storeName));
        } catch (error) {
          row.stores.push({name: storeName, count: 0, error: String(error && error.message || error)});
        }
      }
    } catch (error) {
      row.error = String(error && error.message || error);
    } finally {
      if (db) {
        db.close();
      }
    }
    databases.push(row);
  }
  return {url: location.href, origin: location.origin, operation: "list", available: true, count: databases.length, databases};
})()`
}

func indexedDBGetExpression(database, store, key string, keyJSON bool) string {
	return fmt.Sprintf(`(async () => {
  "__cdp_cli_indexeddb_get__";
  %s
  const databaseName = %s;
  const storeName = %s;
  const key = %s;
  const db = await openDB(databaseName);
  try {
    ensureStore(db, storeName);
    const transaction = db.transaction(storeName, "readonly");
    const done = transactionDone(transaction);
    const value = await requestPromise(transaction.objectStore(storeName).get(key));
    await done;
    const found = value !== undefined;
    return {url: location.href, origin: location.origin, operation: "get", available: true, found, database: databaseName, store: storeName, key, key_source: %s, value: found ? value : null};
  } finally {
    db.close();
  }
})()`, indexedDBOperationHelpers(), jsStringLiteral(database), jsStringLiteral(store), indexedDBKeyExpression(key, keyJSON), jsStringLiteral(indexedDBKeySource(keyJSON)))
}

func indexedDBPutExpression(database, store, key, value string, keyJSON bool) string {
	return fmt.Sprintf(`(async () => {
  "__cdp_cli_indexeddb_put__";
  %s
  const databaseName = %s;
  const storeName = %s;
  const key = %s;
  const value = parseValue(%s);
  const db = await openDB(databaseName);
  try {
    ensureStore(db, storeName);
    const transaction = db.transaction(storeName, "readwrite");
    const done = transactionDone(transaction);
    const objectStore = transaction.objectStore(storeName);
    const previousRequest = objectStore.get(key);
    const putRequest = objectStore.keyPath ? objectStore.put(value) : objectStore.put(value, key);
    const previous = await requestPromise(previousRequest);
    const savedKey = await requestPromise(putRequest);
    await done;
    const existed = previous !== undefined;
    return {url: location.href, origin: location.origin, operation: "put", available: true, found: true, database: databaseName, store: storeName, key: savedKey, key_source: %s, value, previous: existed ? previous : null, created: !existed, updated: existed};
  } finally {
    db.close();
  }
})()`, indexedDBOperationHelpers(), jsStringLiteral(database), jsStringLiteral(store), indexedDBKeyExpression(key, keyJSON), jsStringLiteral(value), jsStringLiteral(indexedDBKeySource(keyJSON)))
}

func indexedDBDeleteExpression(database, store, key string, keyJSON bool) string {
	return fmt.Sprintf(`(async () => {
  "__cdp_cli_indexeddb_delete__";
  %s
  const databaseName = %s;
  const storeName = %s;
  const key = %s;
  const db = await openDB(databaseName);
  try {
    ensureStore(db, storeName);
    const transaction = db.transaction(storeName, "readwrite");
    const done = transactionDone(transaction);
    const objectStore = transaction.objectStore(storeName);
    const previousRequest = objectStore.get(key);
    const deleteRequest = objectStore.delete(key);
    const previous = await requestPromise(previousRequest);
    await requestPromise(deleteRequest);
    await done;
    const found = previous !== undefined;
    return {url: location.href, origin: location.origin, operation: "delete", available: true, found, deleted: found, database: databaseName, store: storeName, key, key_source: %s, previous: found ? previous : null};
  } finally {
    db.close();
  }
})()`, indexedDBOperationHelpers(), jsStringLiteral(database), jsStringLiteral(store), indexedDBKeyExpression(key, keyJSON), jsStringLiteral(indexedDBKeySource(keyJSON)))
}

type indexedDBDumpOptions struct {
	Limit      int
	Offset     int
	PageSize   int
	Cursor     string
	Direction  string
	KeysOnly   bool
	ValuesOnly bool
}

func indexedDBDumpExpression(database, store string, opts indexedDBDumpOptions) string {
	return fmt.Sprintf(`(async () => {
  "__cdp_cli_indexeddb_dump__";
  %s
  const databaseName = %s;
  const storeName = %s;
  const limit = %d;
  const offset = %d;
  const pageSize = %d;
  const cursorToken = %s;
  const direction = %s;
  const keysOnly = %t;
  const valuesOnly = %t;
  const encodeCursor = (key) => btoa(unescape(encodeURIComponent(JSON.stringify({key}))));
  const decodeCursor = (token) => {
    if (!token) {
      return null;
    }
    try {
      return JSON.parse(decodeURIComponent(escape(atob(token)))).key;
    } catch (error) {
      throw new Error("Invalid IndexedDB cursor token");
    }
  };
  const db = await openDB(databaseName);
  try {
    ensureStore(db, storeName);
    const transaction = db.transaction(storeName, "readonly");
    const done = transactionDone(transaction);
    const objectStore = transaction.objectStore(storeName);
    const startKey = decodeCursor(cursorToken);
    const range = startKey === null ? null : (direction.startsWith("prev") ? IDBKeyRange.upperBound(startKey, true) : IDBKeyRange.lowerBound(startKey, true));
    const request = objectStore.openCursor(range, direction);
    const records = [];
    let skipped = 0;
    let lastKey = null;
    let hasMore = false;
    await new Promise((resolve, reject) => {
      request.onerror = () => reject(request.error || new Error("IndexedDB cursor failed"));
      request.onsuccess = (event) => {
        const cursor = event.target.result;
        if (!cursor) {
          resolve();
          return;
        }
        if (!cursorToken && skipped < offset) {
          skipped++;
          cursor.continue();
          return;
        }
        if (records.length >= limit) {
          hasMore = true;
          resolve();
          return;
        }
        const row = {};
        if (!valuesOnly) {
          row.key = cursor.key;
        }
        if (!keysOnly) {
          row.value = cursor.value;
        }
        records.push(row);
        lastKey = cursor.key;
        cursor.continue();
      };
    });
    await done;
    return {
      url: location.href,
      origin: location.origin,
      operation: "dump",
      available: true,
      found: true,
      database: databaseName,
      store: storeName,
      count: records.length,
      limit,
      offset: cursorToken ? 0 : offset,
      page_size: pageSize,
      cursor: cursorToken,
      next_cursor: hasMore && lastKey !== null ? encodeCursor(lastKey) : "",
      has_more: hasMore,
      direction,
      records
    };
  } finally {
    db.close();
  }
})()`, indexedDBOperationHelpers(), jsStringLiteral(database), jsStringLiteral(store), opts.Limit, opts.Offset, opts.PageSize, jsStringLiteral(opts.Cursor), jsStringLiteral(opts.Direction), opts.KeysOnly, opts.ValuesOnly)
}

func indexedDBClearExpression(database, store string) string {
	return fmt.Sprintf(`(async () => {
  "__cdp_cli_indexeddb_clear__";
  %s
  const databaseName = %s;
  const storeName = %s;
  const db = await openDB(databaseName);
  try {
    ensureStore(db, storeName);
    const transaction = db.transaction(storeName, "readwrite");
    const done = transactionDone(transaction);
    const objectStore = transaction.objectStore(storeName);
    const countRequest = objectStore.count();
    const clearRequest = objectStore.clear();
    const count = await requestPromise(countRequest);
    await requestPromise(clearRequest);
    await done;
    return {url: location.href, origin: location.origin, operation: "clear", available: true, found: count > 0, database: databaseName, store: storeName, cleared: count, count: 0};
  } finally {
    db.close();
  }
})()`, indexedDBOperationHelpers(), jsStringLiteral(database), jsStringLiteral(store))
}

func indexedDBOperationHelpers() string {
	return `if (typeof indexedDB === "undefined") {
    throw new Error("IndexedDB is not available in this page context");
  }
  const requestPromise = (request) => new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("IndexedDB request failed"));
  });
  const transactionDone = (transaction) => new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(transaction.error || new Error("IndexedDB transaction failed"));
    transaction.onabort = () => reject(transaction.error || new Error("IndexedDB transaction aborted"));
  });
  const openDB = (name) => new Promise((resolve, reject) => {
    const request = indexedDB.open(name);
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("IndexedDB open failed"));
    request.onblocked = () => reject(new Error("IndexedDB open blocked"));
  });
  const ensureStore = (db, storeName) => {
    if (!db.objectStoreNames.contains(storeName)) {
      throw new Error("IndexedDB object store not found: " + storeName);
    }
  };
  const parseValue = (text) => {
    try {
      return JSON.parse(text);
    } catch (error) {
      return text;
    }
  };`
}

func indexedDBKeyExpression(key string, keyJSON bool) string {
	if keyJSON {
		return fmt.Sprintf("JSON.parse(%s)", jsStringLiteral(key))
	}
	return jsStringLiteral(key)
}

func indexedDBKeySource(keyJSON bool) string {
	if keyJSON {
		return "json"
	}
	return "string"
}

func runCacheStorageOperation(ctx context.Context, session *cdp.PageSession, expression string) (cacheStorageOperationResult, error) {
	result, err := session.Evaluate(ctx, expression, true)
	if err != nil {
		return cacheStorageOperationResult{}, storageCommandFailed("inspect cache storage", session.TargetID, err)
	}
	if result.Exception != nil {
		return cacheStorageOperationResult{}, commandError("javascript_exception", "runtime", fmt.Sprintf("cache storage javascript exception: %s", result.Exception.Text), ExitCheckFailed, []string{"cdp storage cache list --json"})
	}
	var opResult cacheStorageOperationResult
	if err := json.Unmarshal(result.Object.Value, &opResult); err != nil {
		return cacheStorageOperationResult{}, commandError("invalid_storage_result", "runtime", fmt.Sprintf("decode cache storage result: %v", err), ExitCheckFailed, []string{"cdp storage cache list --json"})
	}
	return opResult, nil
}

func cacheStorageListExpression(cacheName, requestURLContains string) string {
	return fmt.Sprintf(`(async () => {
  "__cdp_cli_cache_storage_list__";
  if (typeof caches === "undefined") {
    throw new Error("CacheStorage is not available in this page context");
  }
  const requestedCache = %s;
  const requestURLContains = %s;
  const responseMeta = (response) => response ? ({
    status: response.status,
    status_text: response.statusText,
    type: response.type,
    content_type: response.headers.get("content-type") || ""
  }) : null;
  const allNames = (await caches.keys()).sort((a, b) => a.localeCompare(b));
  const names = requestedCache ? allNames.filter((name) => name === requestedCache) : allNames;
  const cacheRows = [];
  let requestCount = 0;
  for (const name of names) {
    const cache = await caches.open(name);
    const requests = await cache.keys();
    const rows = [];
    for (const request of requests) {
      if (requestURLContains && !request.url.includes(requestURLContains)) {
        continue;
      }
      const row = {url: request.url, method: request.method};
      try {
        const response = await cache.match(request);
        if (response) {
          row.response = responseMeta(response);
        }
      } catch (error) {
        row.error = String(error && error.message || error);
      }
      rows.push(row);
    }
    rows.sort((a, b) => a.url.localeCompare(b.url));
    requestCount += rows.length;
    cacheRows.push({name, count: rows.length, requests: rows});
  }
  return {
    url: location.href,
    origin: location.origin,
    operation: "list",
    available: true,
    found: requestedCache ? allNames.includes(requestedCache) : true,
    cache: requestedCache,
    cache_names: allNames,
    request_count: requestCount,
    caches: cacheRows
  };
})()`, jsStringLiteral(cacheName), jsStringLiteral(requestURLContains))
}

func cacheStorageGetExpression(cacheName, requestURL string, maxBodyBytes int) string {
	return fmt.Sprintf(`(async () => {
  "__cdp_cli_cache_storage_get__";
  if (typeof caches === "undefined") {
    throw new Error("CacheStorage is not available in this page context");
  }
  const cacheName = %s;
  const request = new Request(%s);
  const maxBodyBytes = %d;
  const responseMeta = (response) => response ? ({
    status: response.status,
    status_text: response.statusText,
    type: response.type,
    content_type: response.headers.get("content-type") || ""
  }) : null;
  const truncate = (text) => {
    const encoded = new TextEncoder().encode(text);
    if (encoded.length <= maxBodyBytes) {
      return {text, bytes: encoded.length, omitted: false, max_bytes: maxBodyBytes};
    }
    return {
      text: new TextDecoder().decode(encoded.slice(0, maxBodyBytes)),
      bytes: encoded.length,
      omitted: true,
      max_bytes: maxBodyBytes
    };
  };
  const allNames = await caches.keys();
  if (!allNames.includes(cacheName)) {
    return {url: location.href, origin: location.origin, operation: "get", available: true, found: false, cache: cacheName, request_url: request.url};
  }
  const cache = await caches.open(cacheName);
  const response = await cache.match(request);
  if (!response) {
    return {url: location.href, origin: location.origin, operation: "get", available: true, found: false, cache: cacheName, request_url: request.url};
  }
  const text = await response.clone().text();
  return {
    url: location.href,
    origin: location.origin,
    operation: "get",
    available: true,
    found: true,
    cache: cacheName,
    request_url: request.url,
    response: responseMeta(response),
    body: truncate(text)
  };
})()`, jsStringLiteral(cacheName), jsStringLiteral(requestURL), maxBodyBytes)
}

func cacheStoragePutExpression(cacheName, requestURL, body, contentType string, status int) string {
	return fmt.Sprintf(`(async () => {
  "__cdp_cli_cache_storage_put__";
  if (typeof caches === "undefined") {
    throw new Error("CacheStorage is not available in this page context");
  }
  const cacheName = %s;
  const request = new Request(%s);
  const body = %s;
  const contentType = %s;
  const headers = {};
  if (contentType) {
    headers["Content-Type"] = contentType;
  }
  const responseMeta = (response) => response ? ({
    status: response.status,
    status_text: response.statusText,
    type: response.type,
    content_type: response.headers.get("content-type") || ""
  }) : null;
  const cache = await caches.open(cacheName);
  const previous = await cache.match(request);
  await cache.put(request, new Response(body, {status: %d, headers}));
  const response = await cache.match(request);
  const cacheNames = (await caches.keys()).sort((a, b) => a.localeCompare(b));
  return {
    url: location.href,
    origin: location.origin,
    operation: "put",
    available: true,
    found: true,
    cache: cacheName,
    cache_names: cacheNames,
    request_url: request.url,
    created: !previous,
    updated: !!previous,
    response: responseMeta(response),
    body: {bytes: new TextEncoder().encode(body).length}
  };
})()`, jsStringLiteral(cacheName), jsStringLiteral(requestURL), jsStringLiteral(body), jsStringLiteral(contentType), status)
}

func cacheStorageDeleteExpression(cacheName, requestURL string) string {
	return fmt.Sprintf(`(async () => {
  "__cdp_cli_cache_storage_delete__";
  if (typeof caches === "undefined") {
    throw new Error("CacheStorage is not available in this page context");
  }
  const cacheName = %s;
  const request = new Request(%s);
  const allNames = await caches.keys();
  if (!allNames.includes(cacheName)) {
    return {url: location.href, origin: location.origin, operation: "delete", available: true, found: false, deleted: false, cache: cacheName, request_url: request.url};
  }
  const cache = await caches.open(cacheName);
  const deleted = await cache.delete(request);
  return {url: location.href, origin: location.origin, operation: "delete", available: true, found: deleted, deleted, cache: cacheName, request_url: request.url};
})()`, jsStringLiteral(cacheName), jsStringLiteral(requestURL))
}

func cacheStorageClearExpression(cacheName string, all bool) string {
	return fmt.Sprintf(`(async () => {
  "__cdp_cli_cache_storage_clear__";
  if (typeof caches === "undefined") {
    throw new Error("CacheStorage is not available in this page context");
  }
  const cacheName = %s;
  const clearAll = %t;
  const names = (await caches.keys()).sort((a, b) => a.localeCompare(b));
  const targetNames = clearAll ? names : names.filter((name) => name === cacheName);
  const cleared = [];
  for (const name of targetNames) {
    if (await caches.delete(name)) {
      cleared.push(name);
    }
  }
  return {
    url: location.href,
    origin: location.origin,
    operation: "clear",
    available: true,
    found: cleared.length > 0,
    cache: cacheName,
    cleared
  };
})()`, jsStringLiteral(cacheName), all)
}

func runServiceWorkerOperation(ctx context.Context, session *cdp.PageSession, expression string) (serviceWorkerOperationResult, error) {
	result, err := session.Evaluate(ctx, expression, true)
	if err != nil {
		return serviceWorkerOperationResult{}, storageCommandFailed("inspect service workers", session.TargetID, err)
	}
	if result.Exception != nil {
		return serviceWorkerOperationResult{}, commandError("javascript_exception", "runtime", fmt.Sprintf("service worker javascript exception: %s", result.Exception.Text), ExitCheckFailed, []string{"cdp storage service-workers list --json"})
	}
	var opResult serviceWorkerOperationResult
	if err := json.Unmarshal(result.Object.Value, &opResult); err != nil {
		return serviceWorkerOperationResult{}, commandError("invalid_storage_result", "runtime", fmt.Sprintf("decode service worker result: %v", err), ExitCheckFailed, []string{"cdp storage service-workers list --json"})
	}
	return opResult, nil
}

func serviceWorkerListExpression() string {
	return `(async () => {
  "__cdp_cli_service_workers_list__";
  if (!("serviceWorker" in navigator)) {
    throw new Error("service workers are not available in this page context");
  }
  const workerInfo = (worker) => worker ? {
    script_url: worker.scriptURL || "",
    state: worker.state || ""
  } : null;
  const registrationInfo = (registration) => ({
    scope_url: registration.scope || "",
    update_via_cache: registration.updateViaCache || "",
    active: workerInfo(registration.active),
    waiting: workerInfo(registration.waiting),
    installing: workerInfo(registration.installing)
  });
  const registrations = (await navigator.serviceWorker.getRegistrations())
    .map(registrationInfo)
    .sort((a, b) => a.scope_url.localeCompare(b.scope_url));
  return {
    url: location.href,
    origin: location.origin,
    operation: "list",
    available: true,
    count: registrations.length,
    registrations
  };
})()`
}

func serviceWorkerUnregisterExpression(scope string, all bool) string {
	return fmt.Sprintf(`(async () => {
  "__cdp_cli_service_workers_unregister__";
  if (!("serviceWorker" in navigator)) {
    throw new Error("service workers are not available in this page context");
  }
  const requestedScope = %s;
  const unregisterAll = %t;
  const normalize = (value) => String(value || "").replace(/\/+$/, "");
  const workerInfo = (worker) => worker ? {
    script_url: worker.scriptURL || "",
    state: worker.state || ""
  } : null;
  const registrationInfo = (registration, result) => ({
    scope_url: registration.scope || "",
    update_via_cache: registration.updateViaCache || "",
    active: workerInfo(registration.active),
    waiting: workerInfo(registration.waiting),
    installing: workerInfo(registration.installing),
    result
  });
  const registrations = await navigator.serviceWorker.getRegistrations();
  const selected = unregisterAll
    ? registrations
    : registrations.filter((registration) => registration.scope === requestedScope || normalize(registration.scope) === normalize(requestedScope));
  const unregistered = [];
  for (const registration of selected) {
    const result = await registration.unregister();
    unregistered.push(registrationInfo(registration, result));
  }
  unregistered.sort((a, b) => a.scope_url.localeCompare(b.scope_url));
  return {
    url: location.href,
    origin: location.origin,
    operation: "unregister",
    available: true,
    found: selected.length > 0,
    count: unregistered.length,
    scope: requestedScope,
    unregistered
  };
})()`, jsStringLiteral(scope), all)
}

func jsStringLiteral(value string) string {
	b, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(b)
}

func readStorageValueInput(input string) (string, string, error) {
	if strings.HasPrefix(input, "@") {
		path := strings.TrimPrefix(input, "@")
		if strings.TrimSpace(path) == "" {
			return "", "", commandError("usage", "usage", "@file value input requires a path", ExitUsage, []string{"cdp storage set localStorage key @tmp/value.json --json"})
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", "", commandError("usage", "usage", fmt.Sprintf("read value file: %v", err), ExitUsage, []string{"cdp storage set localStorage key @tmp/value.json --json"})
		}
		return string(b), "file", nil
	}
	return input, "inline", nil
}

func originForURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func applyStorageRedaction(snapshot *storageSnapshot, redact string) {
	if redact == "" || redact == "none" {
		return
	}
	redactStorageArea(&snapshot.LocalStorage, redact)
	redactStorageArea(&snapshot.SessionStorage, redact)
	redactStorageCookies(snapshot.Cookies, redact)
	redactCacheStorage(snapshot.CacheStorage, redact)
	redactServiceWorkers(snapshot.ServiceWorkers, redact)
}

func redactStorageArea(area *storageAreaSnapshot, redact string) {
	for i := range area.Entries {
		if redact == "safe" || sensitiveName(area.Entries[i].Key) {
			area.Entries[i].Value = "<redacted>"
			continue
		}
		area.Entries[i].Value = redactBodyText(area.Entries[i].Value, redact)
	}
}

func redactStorageCookies(cookies []map[string]any, redact string) {
	for _, cookie := range cookies {
		value, _ := cookie["value"].(string)
		if redact == "safe" || sensitiveHeaderValue(value) {
			cookie["value"] = "<redacted>"
		} else if value != "" {
			cookie["value"] = redactBodyText(value, redact)
		}
	}
}

func redactCacheStorage(caches []cacheStorageCache, redact string) {
	for i := range caches {
		for j := range caches[i].Requests {
			caches[i].Requests[j].URL = redactURL(caches[i].Requests[j].URL, redact)
		}
	}
}

func redactServiceWorkers(registrations []serviceWorkerRegistration, redact string) {
	for i := range registrations {
		registrations[i].ScopeURL = redactURL(registrations[i].ScopeURL, redact)
		if registrations[i].Active != nil {
			registrations[i].Active.ScriptURL = redactURL(registrations[i].Active.ScriptURL, redact)
		}
		if registrations[i].Waiting != nil {
			registrations[i].Waiting.ScriptURL = redactURL(registrations[i].Waiting.ScriptURL, redact)
		}
		if registrations[i].Installing != nil {
			registrations[i].Installing.ScriptURL = redactURL(registrations[i].Installing.ScriptURL, redact)
		}
	}
}

func cookieNames(cookies []map[string]any) []string {
	names := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if name, ok := cookie["name"].(string); ok && name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func readStorageSnapshotFile(path string) (storageSnapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return storageSnapshot{}, err
	}
	var envelope struct {
		Snapshot storageSnapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(b, &envelope); err == nil && storageSnapshotHasData(envelope.Snapshot) {
		return envelope.Snapshot, nil
	}
	var snapshot storageSnapshot
	if err := json.Unmarshal(b, &snapshot); err != nil {
		return storageSnapshot{}, err
	}
	if !storageSnapshotHasData(snapshot) {
		return storageSnapshot{}, fmt.Errorf("file does not contain a storage snapshot")
	}
	return snapshot, nil
}

func storageSnapshotHasData(snapshot storageSnapshot) bool {
	return snapshot.URL != "" || snapshot.Origin != "" || len(snapshot.LocalStorage.Entries) > 0 || len(snapshot.SessionStorage.Entries) > 0 || len(snapshot.Cookies) > 0 || len(snapshot.IndexedDB) > 0 || len(snapshot.CacheStorage) > 0 || len(snapshot.ServiceWorkers) > 0 || snapshot.Dump != nil
}

func diffStorageSnapshots(left, right storageSnapshot) storageDiffReport {
	local := diffStringMaps(storageEntryValues(left.LocalStorage), storageEntryValues(right.LocalStorage))
	session := diffStringMaps(storageEntryValues(left.SessionStorage), storageEntryValues(right.SessionStorage))
	cookies := diffStringMaps(cookieValues(left.Cookies), cookieValues(right.Cookies))
	indexedDB := diffStringMaps(indexedDBValues(left.IndexedDB), indexedDBValues(right.IndexedDB))
	cache := diffStringMaps(cacheStorageValues(left.CacheStorage), cacheStorageValues(right.CacheStorage))
	serviceWorkers := diffStringMaps(serviceWorkerValues(left.ServiceWorkers), serviceWorkerValues(right.ServiceWorkers))
	summary := map[string]int{
		"added":   len(local.Added) + len(session.Added) + len(cookies.Added) + len(indexedDB.Added) + len(cache.Added) + len(serviceWorkers.Added),
		"removed": len(local.Removed) + len(session.Removed) + len(cookies.Removed) + len(indexedDB.Removed) + len(cache.Removed) + len(serviceWorkers.Removed),
		"changed": len(local.Changed) + len(session.Changed) + len(cookies.Changed) + len(indexedDB.Changed) + len(cache.Changed) + len(serviceWorkers.Changed),
	}
	return storageDiffReport{LocalStorage: local, SessionStorage: session, Cookies: cookies, IndexedDB: indexedDB, CacheStorage: cache, ServiceWorkers: serviceWorkers, Summary: summary}
}

func storageEntryValues(area storageAreaSnapshot) map[string]string {
	values := map[string]string{}
	for _, entry := range area.Entries {
		values[entry.Key] = entry.Value
	}
	return values
}

func cookieValues(cookies []map[string]any) map[string]string {
	values := map[string]string{}
	for _, cookie := range cookies {
		key := cookieIdentity(cookie)
		if key == "" {
			continue
		}
		b, _ := json.Marshal(cookie)
		values[key] = string(b)
	}
	return values
}

func indexedDBValues(databases []indexedDBDatabase) map[string]string {
	values := map[string]string{}
	for _, database := range databases {
		for _, store := range database.Stores {
			key := database.Name + "|" + store.Name
			b, _ := json.Marshal(store)
			values[key] = string(b)
		}
	}
	return values
}

func cacheStorageValues(caches []cacheStorageCache) map[string]string {
	values := map[string]string{}
	for _, cache := range caches {
		for _, request := range cache.Requests {
			key := cache.Name + "|" + request.URL
			b, _ := json.Marshal(request.Response)
			values[key] = string(b)
		}
	}
	return values
}

func serviceWorkerValues(registrations []serviceWorkerRegistration) map[string]string {
	values := map[string]string{}
	for _, registration := range registrations {
		if registration.ScopeURL == "" {
			continue
		}
		b, _ := json.Marshal(registration)
		values[registration.ScopeURL] = string(b)
	}
	return values
}

func cookieIdentity(cookie map[string]any) string {
	name, _ := cookie["name"].(string)
	domain, _ := cookie["domain"].(string)
	path, _ := cookie["path"].(string)
	if name == "" {
		return ""
	}
	return name + "|" + domain + "|" + path
}

func diffStringMaps(left, right map[string]string) storageAreaDiff {
	diff := storageAreaDiff{Added: []storageDiffItem{}, Removed: []storageDiffItem{}, Changed: []storageDiffItem{}}
	keys := map[string]bool{}
	for key := range left {
		keys[key] = true
	}
	for key := range right {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		leftValue, leftOK := left[key]
		rightValue, rightOK := right[key]
		switch {
		case !leftOK && rightOK:
			diff.Added = append(diff.Added, storageDiffItem{Key: key, After: rightValue})
		case leftOK && !rightOK:
			diff.Removed = append(diff.Removed, storageDiffItem{Key: key, Before: leftValue})
		case leftOK && rightOK && leftValue != rightValue:
			diff.Changed = append(diff.Changed, storageDiffItem{Key: key, Before: leftValue, After: rightValue})
		}
	}
	return diff
}

func storageDiffHasChanges(diff storageDiffReport) bool {
	return diff.Summary["added"] > 0 || diff.Summary["removed"] > 0 || diff.Summary["changed"] > 0
}

func storageCommandFailed(action, targetID string, err error) error {
	return commandError(
		"connection_failed",
		"connection",
		fmt.Sprintf("%s target %s: %v", action, targetID, err),
		ExitConnection,
		[]string{"cdp pages --json", "cdp doctor --json"},
	)
}
