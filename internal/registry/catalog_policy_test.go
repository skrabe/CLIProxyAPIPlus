package registry

import (
	"context"
	"testing"
	"time"
)

func TestCatalogPolicyLocalOverridesAndHome(t *testing.T) {
	for _, tc := range []struct {
		source      string
		local, home bool
		want        string
	}{
		{"", false, false, ""}, {"", true, false, embeddedCatalogSource},
		{"/tmp/custom.json", true, false, "/tmp/custom.json"},
		{"https://example.com/models.json", true, false, "https://example.com/models.json"},
		{"https://example.com/models.json", false, true, "disabled"},
	} {
		if got := effectiveCatalogSources(CatalogSources{Catalog: tc.source}, tc.local, tc.home).Catalog; got != tc.want {
			t.Fatalf("source %q: got %q want %q", tc.source, got, tc.want)
		}
	}
}

func TestCatalogPolicyHotReload(t *testing.T) {
	oldGeneral := generalCatalogUpdater
	catalogRuntime.Lock()
	oldCtx, oldLocal := catalogRuntime.ctx, catalogRuntime.local
	catalogRuntime.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		generalCatalogUpdater = oldGeneral
		catalogRuntime.Lock()
		catalogRuntime.ctx, catalogRuntime.local = oldCtx, oldLocal
		catalogRuntime.Unlock()
	}()
	published := make(chan string, 8)
	generalCatalogUpdater = &catalogUpdater{
		fetch:   func(ctx context.Context, source string) ([]byte, error) { return []byte(source), nil },
		publish: func(data []byte) ([]string, error) { published <- string(data); return nil, nil },
	}
	awaitSource := func(want string) {
		t.Helper()
		select {
		case got := <-published:
			if got != want {
				t.Fatalf("got %q want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("catalog source did not update")
		}
	}
	SetLocalModelCatalogs(true)
	StartModelCatalogUpdaters(ctx, CatalogSources{Catalog: "https://example.com/first"}, false)
	awaitSource("https://example.com/first")
	UpdateModelCatalogSources(CatalogSources{}, false)
	awaitSource(embeddedCatalogSource)
	UpdateModelCatalogSources(CatalogSources{Catalog: "https://example.com/ignored"}, true)
	generalCatalogUpdater.mu.Lock()
	source := generalCatalogUpdater.source
	generation := generalCatalogUpdater.generation
	generalCatalogUpdater.mu.Unlock()
	if source != "disabled" {
		t.Fatal("home enabled general catalog")
	}
	cancel()
	UpdateModelCatalogSources(CatalogSources{Catalog: "https://example.com/stopped"}, false)
	generalCatalogUpdater.mu.Lock()
	after := generalCatalogUpdater.generation
	generalCatalogUpdater.mu.Unlock()
	if generation != after {
		t.Fatal("reload after shutdown restarted readers")
	}
}
