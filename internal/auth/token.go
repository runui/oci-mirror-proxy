package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/runui/oci-mirror-proxy/internal/secret"
)

const (
	// defaultTokenTTL applies when the token service omits expires_in.
	defaultTokenTTL = 60 * time.Second
	// expiryMargin renews a token slightly before the service would reject it.
	expiryMargin = 10 * time.Second
	// maxCachedTokens bounds the cache. Expired entries are pruned when it fills.
	maxCachedTokens = 1024
	// maxTokenResponse bounds the token service response we are willing to read.
	maxTokenResponse = 1 << 20
)

// tokenAuth implements the registry token flow. An upstream answers an
// unauthenticated request with a Bearer challenge that names a token realm.
// The proxy requests a token from that realm, caches it per scope, and reuses it
// until shortly before expiry.
type tokenAuth struct {
	client   *http.Client
	username string
	password secret.Secret // zero value when the upstream is accessed anonymously
	now      func() time.Time

	mu      sync.Mutex
	realm   string // realm learned from the most recent challenge
	service string
	tokens  map[string]cachedToken // keyed by scope
}

type cachedToken struct {
	value   string
	expires time.Time
}

func newTokenAuth(client *http.Client, username string, password secret.Secret, now func() time.Time) *tokenAuth {
	return &tokenAuth{
		client:   client,
		username: username,
		password: password,
		now:      now,
		tokens:   make(map[string]cachedToken),
	}
}

// Authorize attaches a cached token for repo. If no token is cached but an
// earlier challenge revealed the realm, it fetches one first, which avoids a
// 401 round trip for every new repository.
func (t *tokenAuth) Authorize(ctx context.Context, req *http.Request, repo string) error {
	scope := pullScope(repo)
	if scope == "" {
		return nil
	}
	if tok, ok := t.cached(scope); ok {
		req.Header.Set("Authorization", "Bearer "+tok)
		return nil
	}
	realm, service := t.knownRealm()
	if realm == "" {
		// Nothing learned yet: send anonymously. A 401 will start the challenge flow.
		return nil
	}
	tok, err := t.fetch(ctx, realm, service, scope)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

// Challenge handles a 401. It fetches a token for the scope named in the
// challenge and reports that the request can be retried.
func (t *tokenAuth) Challenge(ctx context.Context, resp *http.Response, repo string) (bool, error) {
	params, ok := bearerChallenge(resp.Header.Values("WWW-Authenticate"))
	if !ok {
		return false, nil
	}
	realm := params["realm"]
	if realm == "" {
		return false, errors.New("bearer challenge has no realm")
	}
	scope := params["scope"]
	if scope == "" {
		scope = pullScope(repo)
	}
	t.remember(realm, params["service"])
	if _, err := t.fetch(ctx, realm, params["service"], scope); err != nil {
		return false, err
	}
	return true, nil
}

// fetch requests a token from the realm and caches it for scope.
func (t *tokenAuth) fetch(ctx context.Context, realm, service, scope string) (string, error) {
	u, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("token realm %q: %w", realm, err)
	}
	q := u.Query()
	if service != "" {
		q.Set("service", service)
	}
	q.Set("scope", scope)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if t.username != "" {
		req.SetBasicAuth(t.username, t.password.Reveal())
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token service answered %s", resp.Status)
	}

	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxTokenResponse)).Decode(&body); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	tok := body.Token
	if tok == "" {
		tok = body.AccessToken
	}
	if tok == "" {
		return "", errors.New("token service returned no token")
	}
	ttl := time.Duration(body.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = defaultTokenTTL
	}
	t.store(scope, tok, ttl)
	return tok, nil
}

func (t *tokenAuth) cached(scope string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c, ok := t.tokens[scope]
	if !ok || !t.now().Before(c.expires) {
		return "", false
	}
	return c.value, true
}

func (t *tokenAuth) store(scope, value string, ttl time.Duration) {
	// Renew early, but never so early that a short-lived token is useless.
	margin := min(expiryMargin, ttl/2)
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.tokens) >= maxCachedTokens {
		now := t.now()
		for k, c := range t.tokens {
			if !now.Before(c.expires) {
				delete(t.tokens, k)
			}
		}
	}
	t.tokens[scope] = cachedToken{value: value, expires: t.now().Add(ttl - margin)}
}

func (t *tokenAuth) remember(realm, service string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.realm, t.service = realm, service
}

func (t *tokenAuth) knownRealm() (string, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.realm, t.service
}

// pullScope builds the scope string for read access to repo.
func pullScope(repo string) string {
	if repo == "" {
		return ""
	}
	return "repository:" + repo + ":pull"
}
