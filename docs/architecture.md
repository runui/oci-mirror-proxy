# Architecture

[English](architecture.md) | [简体中文](zh-CN/architecture.md)

## Components

```
cmd/oci-mirror-proxy        entry point; calls internal/cli
internal/cli                commands, flag parsing, output formatting
internal/app                composition root: owns configuration state, pools, route table
internal/frontend           Registry v2 HTTP surface dockerd talks to
internal/route              Dispatcher, middleware chaining, atomically swapped route table
internal/middleware         logging, blob resumption
internal/scheduler          strategies: sequential and hedged; the Candidate contract
internal/upstream           one upstream (endpoint), its breaker and transport; the reconciling pool
internal/auth               per-upstream authorizers: none, basic, token challenge flow
internal/secret             secret references and redaction
internal/config             configuration model, defaults, validation, file I/O
internal/ociapi             registry path parsing; a leaf package
internal/admin              admin API over a unix socket, and its client
internal/daemonconf         dockerd integration: detection, pure rendering, transactional apply
internal/unixhttp           HTTP over unix sockets, shared by admin and daemonconf
internal/fsutil             atomic file writes
internal/cache              reserved extension point for a blob cache (interface only)
```

## Dependency rules

- Leaf packages import no internal package: `ociapi`, `secret`, `scheduler`, `fsutil`,
  `unixhttp`, `daemonconf` (it receives a `Target` value, not the config model).
- Interfaces are defined where they are consumed. For example, `route` depends on a
  `CandidateSource` interface, not on the concrete pool. `frontend` depends on a
  `Routes` interface.
- Only `app` and `cli` see the whole graph.

## Request flow

1. dockerd sends `GET /v2/library/nginx/manifests/latest`.
2. `frontend` parses the path with `ociapi`, which derives the alias (`docker.io`),
   the kind (manifest), the repository and the reference. Anything outside the
   registry grammar is a 404. Only GET and HEAD are accepted.
3. `frontend` looks up the alias in the route table. The lookup is lock-free.
4. The dispatcher chain runs: logging, then blob resumption, then the route.
5. The route takes a snapshot of enabled upstreams from its pool, then calls the
   strategy.
6. The strategy tries upstreams. Each attempt goes through `Endpoint.Attempt`,
   which checks the breaker, sends the request, applies the first-byte and overall
   timeouts, answers a single authentication challenge if one is issued, and
   records the outcome.
7. The first acceptable (2xx) response is returned. Its headers are copied through an
   allowlist, and its body is streamed.

## Failure handling

| Event | Handling |
|---|---|
| Connection refused or silent upstream | Counted as a failure. The next upstream is tried. Bounded by the first-byte timeout. |
| 5xx or 429 | Counted as a failure. |
| 401 with a Bearer challenge | Handled once: a token is fetched and the request is retried. A second 401 is returned and counts as an alive upstream with no content. |
| 404 | Not a health problem. The next upstream is tried. If every attempted upstream answered 404, the proxy answers 404 and dockerd falls back. |
| Breaker open | The attempt is skipped without touching the network. |
| Mid-stream failure of a blob | The download continues from the current offset on another upstream, if that upstream returns a `206` whose `Content-Range` starts at the same offset. Otherwise the original error is surfaced. Up to three resumptions per download. |
| Client disconnect | The request context is cancelled, the strategy stops, and nothing more is written. |
| Every upstream failed, not with 404 | 502 `UNAVAILABLE`. dockerd falls back. |

## Strategies

**Sequential** (default): one upstream at a time, in configured order. The next
one is contacted only after the previous one has failed.

**Hedged** (opt-in): the first upstream starts immediately. Whenever `race.delay`
passes with no answer, another upstream is started, up to `race.max_parallel` in
flight. A failed attempt starts the next upstream immediately. The first
acceptable response wins, and the other attempts are cancelled. Losing responses
are drained and closed in the background, so connections are not leaked.

## Reconciliation

Every change to the upstream set, whether from the admin API, the CLI or a file
reload, goes through `Pool.Reconcile`:

- An upstream whose definition is unchanged keeps its breaker state and counters.
- A changed or new upstream is rebuilt, with its own transport and authorizer.
- Changing the breaker settings rebuilds every upstream, because the breakers
  carry the thresholds.
- A build error leaves the pool unchanged.
- Removed upstreams have their idle connections closed.

Within the service, configuration changes are serialized by a mutex. A change is
applied to the pools, then persisted. If either step fails, the previous
configuration is reapplied. Requests never take that mutex.

## Concurrency model

- The route table is an `atomic.Pointer` to an immutable map. Lookups never block.
- A pool publishes its endpoint slice atomically. Readers take a snapshot, so a
  reconcile never changes a request that is already running.
- Each endpoint has its own `http.Transport`, so TLS settings and connection pools
  are isolated between upstreams.
- Breaker and token-cache state is protected by mutexes. Counters are atomics.

## Security boundaries

- The data plane is unauthenticated and listens on loopback by default.
- The admin API is reachable only through a socket protected by file permissions.
- Upstream credentials are scoped to their upstream. The client's `Authorization`
  is dropped at the frontend. Redirects that cross hosts drop the `Authorization`
  header, because that is standard Go `net/http` behavior.
- Content integrity is end-to-end: dockerd verifies each digest. A faulty upstream
  can cause a failed pull, but it cannot cause an image with wrong content to be accepted.

## Extension points

| Extension | How |
|---|---|
| New authentication scheme | Implement `auth.Authorizer` and add a case to `auth.New`. |
| New secret backend (for example a keychain) | Implement `secret.Provider` and register it in the resolver. The config package needs no change. |
| New strategy | Implement `scheduler.Strategy`, then select it in `app.dispatcher`. |
| New middleware | Implement `route.Middleware` and add it to the chain in `app.New`. |
| Blob cache | Implement `cache.Store` and wrap the route with a middleware that consults it. The data path is unchanged. |
| Another registry alias | Add a `registries` entry. Routing, the hosts.toml output and the daemon mirror logic already handle it. |
