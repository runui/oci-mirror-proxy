package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/config"
	"github.com/runui/oci-mirror-proxy/internal/secret"
)

// tokenServer emulates a registry with token authentication. Requests without
// the issued token receive a Bearer challenge that points at the token realm.
type tokenServer struct {
	*httptest.Server
	mu          sync.Mutex
	issued      string
	ttl         int
	tokenCalls  int
	registry401 int
	basicUser   string
	basicPass   string
	tokenStatus int
}

func newTokenServer(t *testing.T, ttl int) *tokenServer {
	t.Helper()
	ts := &tokenServer{issued: "tok-1", ttl: ttl}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		ts.tokenCalls++
		if ts.basicUser != "" {
			u, p, ok := r.BasicAuth()
			if !ok || u != ts.basicUser || p != ts.basicPass {
				http.Error(w, "bad credentials", http.StatusUnauthorized)
				return
			}
		}
		if ts.tokenStatus != 0 {
			http.Error(w, "unavailable", ts.tokenStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":%q,"expires_in":%d}`, ts.issued, ts.ttl)
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+ts.issued {
			ts.registry401++
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(
				`Bearer realm="%s/token",service="fake-registry"`, ts.URL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	ts.Server = httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func (ts *tokenServer) counts() (tokens, unauthorized int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.tokenCalls, ts.registry401
}

// do sends one request through the authorizer, mirroring the upstream endpoint's
// behaviour: authorize, send, and retry once after a challenge.
func do(t *testing.T, a Authorizer, client *http.Client, base, repo string) int {
	t.Helper()
	ctx := t.Context()
	send := func() *http.Response {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v2/"+repo+"/manifests/latest", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Authorize(ctx, req, repo); err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		return resp
	}
	resp := send()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return resp.StatusCode
	}
	retry, err := a.Challenge(ctx, resp, repo)
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if !retry {
		return resp.StatusCode
	}
	resp2 := send()
	defer resp2.Body.Close()
	return resp2.StatusCode
}

func newTokenAuthForTest(t *testing.T, username string, password secret.Secret, now func() time.Time) *tokenAuth {
	t.Helper()
	return newTokenAuth(http.DefaultClient, username, password, now)
}

func TestTokenFlowRetriesAfterChallenge(t *testing.T) {
	ts := newTokenServer(t, 300)
	a := newTokenAuthForTest(t, "", secret.Secret{}, time.Now)

	if code := do(t, a, ts.Client(), ts.URL, "library/nginx"); code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after the challenge flow", code)
	}
	tokens, unauthorized := ts.counts()
	if tokens != 1 || unauthorized != 1 {
		t.Fatalf("token calls = %d, 401 responses = %d; want 1 and 1", tokens, unauthorized)
	}
}

func TestTokenPrefetchAvoidsSecondChallenge(t *testing.T) {
	ts := newTokenServer(t, 300)
	a := newTokenAuthForTest(t, "", secret.Secret{}, time.Now)

	do(t, a, ts.Client(), ts.URL, "library/nginx")
	// A new repository must not cost another 401: the realm is already known.
	if code := do(t, a, ts.Client(), ts.URL, "library/alpine"); code != http.StatusOK {
		t.Fatalf("second repo status = %d, want 200", code)
	}
	_, unauthorized := ts.counts()
	if unauthorized != 1 {
		t.Fatalf("401 responses = %d, want exactly 1 (prefetch should avoid the second)", unauthorized)
	}
}

func TestTokenIsRenewedAfterExpiry(t *testing.T) {
	ts := newTokenServer(t, 20) // margin is min(10s, ttl/2) = 10s, so usable for 10s
	clock := time.Unix(1_000_000, 0)
	now := func() time.Time { return clock }
	a := newTokenAuthForTest(t, "", secret.Secret{}, now)

	do(t, a, ts.Client(), ts.URL, "library/nginx")

	clock = clock.Add(9 * time.Second)
	if _, ok := a.cached("repository:library/nginx:pull"); !ok {
		t.Fatal("token should still be valid 9s after issue")
	}
	clock = clock.Add(2 * time.Second)
	if _, ok := a.cached("repository:library/nginx:pull"); ok {
		t.Fatal("token should be treated as expired inside the safety margin")
	}
}

func TestTokenBasicCredentialsReachRealm(t *testing.T) {
	ts := newTokenServer(t, 300)
	ts.basicUser, ts.basicPass = "alice", "s3cret"
	a := newTokenAuthForTest(t, "alice", secret.New("s3cret"), time.Now)

	if code := do(t, a, ts.Client(), ts.URL, "private/app"); code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when the realm accepts the credentials", code)
	}
}

func TestTokenServiceFailureIsReported(t *testing.T) {
	ts := newTokenServer(t, 300)
	ts.tokenStatus = http.StatusServiceUnavailable
	a := newTokenAuthForTest(t, "", secret.Secret{}, time.Now)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/v2/x/manifests/latest", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := a.Challenge(t.Context(), resp, "x"); err == nil {
		t.Fatal("Challenge must fail when the token service is unavailable")
	}
}

func TestBasicAuthAttachesHeader(t *testing.T) {
	a, err := New(t.Context(), config.Auth{Type: config.AuthBasic, Username: "bob", Password: "env:PW"}, Options{
		Secrets: mustResolver(t, map[string]string{"PW": "pw-value"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com/v2/", nil)
	if err := a.Authorize(t.Context(), req, ""); err != nil {
		t.Fatal(err)
	}
	user, pass, ok := req.BasicAuth()
	if !ok || user != "bob" || pass != "pw-value" {
		t.Fatalf("basic auth = %q/%q/%v", user, pass, ok)
	}
}

func TestNewReportsMissingSecret(t *testing.T) {
	_, err := New(t.Context(), config.Auth{Type: config.AuthBasic, Username: "bob", Password: "env:MISSING"}, Options{
		Secrets: mustResolver(t, map[string]string{}),
	})
	if err == nil {
		t.Fatal("a missing secret must fail at build time")
	}
}

func TestNewRejectsUnknownType(t *testing.T) {
	_, err := New(t.Context(), config.Auth{Type: "oauth"}, Options{})
	if err == nil {
		t.Fatal("unknown auth type must be rejected")
	}
}

func TestNoneAuthLeavesRequestUntouched(t *testing.T) {
	a, err := New(t.Context(), config.Auth{Type: config.AuthNone}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com/v2/", nil)
	if err := a.Authorize(t.Context(), req, "x"); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatal("none auth must not add an Authorization header")
	}
	retry, err := a.Challenge(context.Background(), &http.Response{Header: http.Header{}}, "x")
	if err != nil || retry {
		t.Fatalf("none auth challenge = %v, %v; want no retry", retry, err)
	}
}

func mustResolver(t *testing.T, env map[string]string) *secret.Resolver {
	t.Helper()
	r, err := secret.NewResolver(secret.EnvProvider{Lookup: func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
