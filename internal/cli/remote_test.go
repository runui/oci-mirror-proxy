package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/admin"
	"github.com/runui/oci-mirror-proxy/internal/app"
	"github.com/runui/oci-mirror-proxy/internal/config"
)

func catalogJSON(endpoint string) string {
	return `{"version":1,"regions":{"cn":"China","apac":"Asia Pacific"},"mirrors":[{"name":"Example","regions":["cn","apac"],"endpoints":[{"registry":"docker.io","url":"` + endpoint + `"}]}]}`
}

func TestRemoteLifecycleBackupDedupAndDryRun(t *testing.T) {
	main := writeConfig(t, config.Default())
	socket := startTestAPI(t, main)
	_ = socket
	originalMain, _ := os.ReadFile(main)
	var mu sync.Mutex
	body := catalogJSON("https://mirror.example")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mu.Lock(); defer mu.Unlock(); io.WriteString(w, body) }))
	defer server.Close()
	invoke := func(extra ...string) (int, string, string) {
		args := []string{"remote", "update", "--config", main, "--admin-socket", socket, "--url", server.URL, "--no-reload"}
		return run(t, append(args, extra...)...)
	}
	if code, _, e := invoke("--region", "cn,apac", "--dry-run"); code != 0 {
		t.Fatal(e)
	}
	if _, err := os.Stat(config.DropInDir(main)); !os.IsNotExist(err) {
		t.Fatal("dry-run created directory")
	}
	if _, err := os.Stat(main + ".lock"); !os.IsNotExist(err) {
		t.Fatal("dry-run created lock")
	}
	if code, _, e := invoke("--region", "cn,apac"); code != 0 {
		t.Fatal(e)
	}
	path, _ := remotePath(main, server.URL)
	first, _ := os.ReadFile(path)
	if bytes.Contains(first, []byte("id:")) {
		t.Fatal("generated upstream ID")
	}
	c, err := config.Load(main)
	if err != nil || len(c.Registries["docker.io"].Upstreams) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	if code, out, e := run(t, "remote", "list", "--config", main, "--admin-socket", socket); code != 0 || !strings.Contains(out, server.URL) {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if code, _, e := invoke(); code != 0 {
		t.Fatal(e)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatal("unchanged update created backup")
	}
	mu.Lock()
	body = catalogJSON("https://second.example")
	mu.Unlock()
	if code, _, e := invoke(); code != 0 {
		t.Fatal(e)
	}
	backup, _ := os.ReadFile(path + ".bak")
	if !bytes.Equal(backup, first) {
		t.Fatal("backup is not previous exact result")
	}
	second, _ := os.ReadFile(path)
	mu.Lock()
	body = `{"version":1,"version":2}`
	mu.Unlock()
	if code, _, _ := invoke(); code == 0 {
		t.Fatal("accepted duplicate keys")
	}
	after, _ := os.ReadFile(path)
	backup, _ = os.ReadFile(path + ".bak")
	if !bytes.Equal(after, second) || !bytes.Equal(backup, first) {
		t.Fatal("failed download changed current or backup")
	}
	if code, _, e := run(t, "remote", "remove", "--config", main, "--admin-socket", socket, "--url", server.URL, "--no-reload"); code != 0 {
		t.Fatal(e)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("remove left source")
	}
	backup, _ = os.ReadFile(path + ".bak")
	if !bytes.Equal(backup, second) {
		t.Fatal("remove did not preserve last result")
	}
	currentMain, _ := os.ReadFile(main)
	if !bytes.Equal(currentMain, originalMain) {
		t.Fatal("remote modified main config")
	}
}

func TestRemoteSourcesShareURLsAndRemoveIndependently(t *testing.T) {
	main := writeConfig(t, config.Default())
	socket := startTestAPI(t, main)
	_ = socket
	catalog, err := decodeCatalog([]byte(catalogJSON("https://mirror.example")))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://example.com/a.json", "https://example.com/b.json"} {
		if _, err := editRemoteClient(admin.NewClient(socket), u, []string{"cn"}, catalog, "update", false, false, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	c, err := config.Load(main)
	if err != nil || len(c.Registries["docker.io"].Upstreams) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := editRemoteClient(admin.NewClient(socket), "https://example.com/a.json", nil, remoteCatalog{}, "remove", false, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	c, err = config.Load(main)
	if err != nil || len(c.Registries["docker.io"].Upstreams) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestRemoteAllRegions(t *testing.T) {
	main := writeConfig(t, config.Default())
	socket := startTestAPI(t, main)
	_ = socket
	var catalog remoteCatalog
	catalog.Version = 1
	catalog.Regions = map[string]string{"cn": "China", "eu": "Europe", "global": "Global"}
	for _, region := range []string{"cn", "eu", "global"} {
		catalog.Mirrors = append(catalog.Mirrors, catalogMirror{
			Name: region, Regions: []string{region},
			Endpoints: []catalogEndpoint{{Registry: "docker.io", URL: "https://" + region + ".example"}},
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(catalog); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	args := []string{"remote", "update", "--config", main, "--admin-socket", socket, "--url", server.URL, "--no-reload"}
	check := func(want int, regions string) {
		t.Helper()
		cfg, err := config.Load(main)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(cfg.Registries["docker.io"].Upstreams); got != want {
			t.Fatalf("expected %d upstreams, got %d", want, got)
		}
		files, err := scanRemoteClient(admin.NewClient(socket))
		if err != nil || len(files) != 1 || strings.Join(files[0].meta.Regions, ",") != regions {
			t.Fatalf("unexpected saved regions: %+v (%v)", files, err)
		}
	}
	for _, tc := range []struct {
		name    string
		flags   []string
		count   int
		regions string
	}{
		{name: "first update without region", count: 3, regions: "cn,eu,global"},
		{name: "explicit region filters sites", flags: []string{"--region", "cn"}, count: 1, regions: "cn"},
		{name: "omitted region clears previous filter", count: 3, regions: "cn,eu,global"},
		{name: "multiple regions", flags: []string{"--region", "cn,eu"}, count: 2, regions: "cn,eu"},
		{name: "explicit empty region selects all", flags: []string{"--region", ""}, count: 3, regions: "cn,eu,global"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, _, errOut := run(t, append(args, tc.flags...)...); code != 0 {
				t.Fatal(errOut)
			}
			check(tc.count, tc.regions)
		})
	}
}

func TestRemoteBackupFailureDoesNotReplaceCurrent(t *testing.T) {
	main := writeConfig(t, config.Default())
	socket := startTestAPI(t, main)
	client := admin.NewClient(socket)
	before := []byte("previous")
	f, err := client.WriteFragment("source.yaml", "", before)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(config.DropInDir(main), "source.yaml.bak"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := client.WriteFragment(f.Name, f.Revision, []byte("next")); err == nil {
		t.Fatal("ignored backup failure")
	}
	after, err := client.ReadFragment(f.Name)
	if err != nil || !bytes.Equal(after.Data, before) {
		t.Fatal("changed file after failed backup")
	}
}

func TestRemoteStrictCatalogAndURLIdentity(t *testing.T) {
	valid := catalogJSON("https://mirror.example")
	for _, body := range []string{valid + ` {}`, strings.Replace(valid, `"version":1`, `"version":2`, 1), strings.Replace(valid, `"name":"Example"`, `"name":"Example","auth":"basic"`, 1), strings.Replace(valid, `"cn","apac"`, `"missing"`, 1), strings.Replace(valid, `https://mirror.example`, `https://mirror.example?secret=x`, 1)} {
		if _, err := decodeCatalog([]byte(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	a, _ := remotePath("config.yaml", "https://EXAMPLE.com:443/a?b=2&a=1")
	b, _ := remotePath("config.yaml", "https://example.com/a?b=2&a=1")
	c, _ := remotePath("config.yaml", "https://example.com/a?a=1&b=2")
	if a != b || a == c {
		t.Fatalf("identity: %s %s %s", a, b, c)
	}
	if strings.Contains(displayRemoteURL("https://example.com?token=secret"), "secret") {
		t.Fatal("secret leaked")
	}
}

func TestRemoteRepairsMalformedTargetAndKeepsMetadata(t *testing.T) {
	main := writeConfig(t, config.Default())
	socket := startTestAPI(t, main)
	_ = socket
	u := "https://example.com/catalog.json"
	catalog, _ := decodeCatalog([]byte(catalogJSON("https://mirror.example")))
	if _, err := editRemoteClient(admin.NewClient(socket), u, []string{"cn"}, catalog, "update", false, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	path, _ := remotePath(main, u)
	before, _ := os.ReadFile(path)
	lines := strings.SplitN(string(before), "\n\n", 2)
	if err := os.WriteFile(path, []byte(lines[0]+"\n\nregistries: [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := editRemoteClient(admin.NewClient(socket), u, nil, catalog, "update", false, false, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteHeaderRemainsDiscoverableAfterGenericYAMLEdit(t *testing.T) {
	main := writeConfig(t, config.Default())
	socket := startTestAPI(t, main)
	_ = socket
	u := "https://example.com/catalog.json?token=private"
	catalog, err := decodeCatalog([]byte(catalogJSON("https://mirror.example")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := editRemoteClient(admin.NewClient(socket), u, []string{"cn"}, catalog, "update", false, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	b, err := config.LoadBundle(main)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.EditUpstream("remove", "docker.io", "https://mirror.example", config.Upstream{}, false); err != nil {
		t.Fatal(err)
	}
	if err := b.Persist(); err != nil {
		t.Fatal(err)
	}
	files, err := scanRemoteClient(admin.NewClient(socket))
	if err != nil || len(files) != 1 || files[0].meta.URL != u || strings.Join(files[0].meta.Regions, ",") != "cn" {
		t.Fatalf("generic YAML write lost managed identity: %+v %v", files, err)
	}
	if _, err := editRemoteClient(admin.NewClient(socket), u, nil, catalog, "update", false, false, io.Discard); err != nil {
		t.Fatalf("could not update edited source: %v", err)
	}
}

func TestRepositoryCatalogValid(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "subscriptions", "mirrors.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeCatalog(data); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(filepath.Join("..", "..", "subscriptions", "schema.json"))
	if err != nil || !json.Valid(schema) {
		t.Fatalf("invalid schema: %v", err)
	}
}

func FuzzRemoteJSON(f *testing.F) {
	f.Add(catalogJSON("https://mirror.example"))
	f.Add(`{"version":1,"version":2}`)
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > 65536 {
			return
		}
		_, _ = decodeCatalog([]byte(body))
	})
}

func TestRemoteAllPartialFailureAndEmptyProtection(t *testing.T) {
	main := writeConfig(t, config.Default())
	socket := startTestAPI(t, main)
	_ = socket
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			http.Error(w, "broken", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, catalogJSON("https://new.example"))
	}))
	defer server.Close()
	catalog, _ := decodeCatalog([]byte(catalogJSON("https://old.example")))
	for _, suffix := range []string{"/good", "/bad"} {
		if _, err := editRemoteClient(admin.NewClient(socket), server.URL+suffix, []string{"cn"}, catalog, "update", false, false, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	badPath, _ := remotePath(main, server.URL+"/bad")
	before, _ := os.ReadFile(badPath)
	if code, _, _ := run(t, "remote", "update", "--all", "--config", main, "--admin-socket", socket, "--no-reload"); code != 1 {
		t.Fatalf("partial failure exit=%d", code)
	}
	goodPath, _ := remotePath(main, server.URL+"/good")
	good, _ := os.ReadFile(goodPath)
	bad, _ := os.ReadFile(badPath)
	if !bytes.Contains(good, []byte("https://new.example")) || !bytes.Equal(before, bad) {
		t.Fatal("partial update did not isolate sources")
	}
	empty := remoteCatalog{Version: 1, Regions: map[string]string{"cn": "China"}, Mirrors: []catalogMirror{}}
	if _, err := editRemoteClient(admin.NewClient(socket), server.URL+"/good", nil, empty, "update", false, false, io.Discard); err == nil {
		t.Fatal("accepted empty selection")
	}
	if _, err := editRemoteClient(admin.NewClient(socket), server.URL+"/good", nil, empty, "update", false, true, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestFetchCatalogCancellationSizeAndRedirectLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/large":
			io.WriteString(w, strings.Repeat(" ", maxCatalogSize+1))
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	for _, suffix := range []string{"/large", "/loop"} {
		if _, err := fetchCatalog(t.Context(), server.URL+suffix); err == nil {
			t.Fatal("accepted " + suffix)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := fetchCatalog(ctx, server.URL+"/slow"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestRemoteReloadAndURLUpstreamCommands(t *testing.T) {
	main := writeConfig(t, config.Default())
	socket := startTestAPI(t, main)
	_ = socket
	svc, err := app.New(t.Context(), app.Options{ConfigPath: main})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	// Keep the Unix path below macOS's 104-byte limit, independent of the test
	// name (t.TempDir includes it and can produce an overlong socket path).
	dir, err := os.MkdirTemp("", "omp")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(dir)
	sock := filepath.Join(dir, "a.sock")
	ln, err := admin.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := admin.NewHTTPServer(svc)
	go srv.Serve(ln)
	defer srv.Close()
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, catalogJSON("https://mirror.example/hub"))
	}))
	defer catalog.Close()
	for _, args := range [][]string{
		{"remote", "update", "--config", main, "--admin-socket", socket, "--url", catalog.URL, "--region", "cn", "--admin-socket", sock},
		{"upstream", "disable", "--config", main, "--admin-socket", socket, "--url", "https://mirror.example/hub", "--admin-socket", sock},
		{"remote", "update", "--config", main, "--admin-socket", socket, "--url", catalog.URL, "--admin-socket", sock},
	} {
		if code, _, e := run(t, args...); code != 0 {
			t.Fatalf("%v: %s", args, e)
		}
	}
	ups, err := svc.ListUpstreams("docker.io")
	if err != nil || len(ups) != 1 || !ups[0].Disabled {
		t.Fatalf("disable not retained: %+v %v", ups, err)
	}
	if code, _, e := run(t, "remote", "remove", "--config", main, "--admin-socket", socket, "--url", catalog.URL, "--admin-socket", sock); code != 0 {
		t.Fatal(e)
	}
	ups, err = svc.ListUpstreams("docker.io")
	if err != nil || len(ups) != 0 {
		t.Fatalf("orphan override: %+v %v", ups, err)
	}
	if code, _, e := run(t, "upstream", "add", "--config", main, "--admin-socket", socket, "--url", "https://manual.example/hub", "--admin-socket", sock); code != 0 {
		t.Fatal(e)
	}
	if code, _, e := run(t, "upstream", "remove", "--config", main, "--admin-socket", socket, "--url", "https://manual.example/hub", "--admin-socket", sock); code != 0 {
		t.Fatal(e)
	}
}

func TestRemoteReloadFailureReportsCommittedFileAndRetryReloads(t *testing.T) {
	main := writeConfig(t, config.Default())
	socket := startTestAPI(t, main)
	_ = socket
	var reject atomic.Bool
	reject.Store(true)
	var reloads atomic.Int32
	dir, err := os.MkdirTemp("", "omp")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(dir)
	sock := filepath.Join(dir, "a.sock")
	ln, err := admin.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := app.New(t.Context(), app.Options{ConfigPath: main})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	handler := admin.NewHTTPServer(svc).Handler
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/reload" {
			handler.ServeHTTP(w, r)
			return
		}
		reloads.Add(1)
		if reject.Load() {
			http.Error(w, `{"error":"rejected"}`, 500)
			return
		}
		w.WriteHeader(204)
	})}
	go server.Serve(ln)
	defer server.Close()
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, catalogJSON("https://mirror.example")) }))
	defer catalog.Close()
	args := []string{"remote", "update", "--config", main, "--admin-socket", socket, "--url", catalog.URL, "--region", "cn", "--admin-socket", sock}
	if code, _, e := run(t, args...); code != 1 || !strings.Contains(e, "files saved") {
		t.Fatalf("%d %s", code, e)
	}
	path, _ := remotePath(main, catalog.URL)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("failed reload lost committed file")
	}
	reject.Store(false)
	if code, _, e := run(t, args...); code != 0 {
		t.Fatal(e)
	}
	if reloads.Load() != 2 {
		t.Fatal("unchanged retry did not reload")
	}
}
