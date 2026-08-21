package transcriptionapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/providerpolicy"
)

func TestHealthAndModelsOmitDisabledProvidersUnlessExplicitlyRequested(t *testing.T) {
	chatGPT := &fakeProvider{id: ProviderChatGPT, result: Result{Text: "unused"}}
	m365 := &fakeProvider{id: ProviderM365, result: Result{Text: "unused"}}
	policy, err := providerpolicy.New([]string{"chatgpt-web"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewEphemeralStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		Registry:        NewRegistryWithPolicy(policy, chatGPT, m365),
		Store:           store,
		DefaultProvider: ProviderM365,
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	for _, path := range []string{"/healthz", "/v1/models"} {
		t.Run(path, func(t *testing.T) {
			ordinary := getProviderIDs(t, httpServer.URL+path)
			if len(ordinary) != 1 || ordinary[0] != string(ProviderM365) {
				t.Fatalf("ordinary %s providers = %v", path, ordinary)
			}

			diagnostic := getProviderIDs(t, httpServer.URL+path+"?include_disabled=true")
			if len(diagnostic) != 2 || diagnostic[0] != string(ProviderChatGPT) || diagnostic[1] != string(ProviderM365) {
				t.Fatalf("diagnostic %s providers = %v", path, diagnostic)
			}
		})
	}
}

func getProviderIDs(t *testing.T, url string) []string {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", url, response.StatusCode)
	}
	var document struct {
		Providers []struct {
			Provider ProviderID `json:"provider"`
		} `json:"providers"`
		Data []struct {
			Provider ProviderID `json:"provider"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if len(document.Providers) > 0 {
		ids := make([]string, 0, len(document.Providers))
		for _, provider := range document.Providers {
			ids = append(ids, string(provider.Provider))
		}
		return ids
	}
	ids := make([]string, 0, len(document.Data))
	for _, provider := range document.Data {
		ids = append(ids, string(provider.Provider))
	}
	return ids
}
