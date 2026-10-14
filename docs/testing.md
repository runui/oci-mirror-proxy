# Testing

## Layers

| Layer | Where | What it proves |
|---|---|---|
| Unit | every package `*_test.go` | Each component in isolation: the breaker state machine under an injected clock, strategy outcomes with scripted candidates, token parsing and caching, path parsing, TOML and JSON rendering, the atomic apply/revert contract. |
| Fuzz | `internal/ociapi` (`FuzzParse`) | Path parsing never yields a canonical path that escapes `/v2/`. The corpus under `testdata/fuzz` is committed. |
| Protocol and end to end | `internal/app/app_test.go` | The real frontend, routing and upstream code, over real HTTP, against the in-memory fake registry. Covers failover timing, breaker suppression, racing, resumption across upstreams, token auth, live admin changes, persistence, and reload rollback. |
| Integration with dockerd | Not automated here | See the manual steps below. |

Run everything with `make check`. It runs formatting, vet, tests with the race
detector, and the dependency policy check. `make fuzz` runs the parser fuzzer for longer.

## The fake registry

`internal/testutil/fakeregistry` is an in-memory Registry v2 server. It can:

- hang: accept connections and never answer (simulates a silent upstream);
- answer with a fixed status to every request (simulates a failing upstream);
- truncate whole-blob responses after N bytes, while promising the full length;
- reject `Range` requests with 500;
- require a bearer token and publish a Bearer challenge.

The package is test support only. It is also built as a standalone process,
`internal/testutil/fakeregistry/cmd/fakeregistry`, which serves one sample image
(`library/hello:latest`) for manual exercises.

## Manual smoke test (real binary, no dockerd)

This is what was run to verify the binary end to end:

```sh
make build
D=$(mktemp -d)
dist/oci-mirror-proxy init --config $D/config.yaml
go run ./internal/testutil/fakeregistry/cmd/fakeregistry > $D/fake.out &   # note the url= line
dist/oci-mirror-proxy serve --config $D/config.yaml --admin-socket $D/admin.sock --log-level debug &
dist/oci-mirror-proxy upstream add --admin-socket $D/admin.sock --id fake --url "$(sed -n 's/^url=//p' $D/fake.out)"
curl -i http://127.0.0.1:24817/v2/
curl -i http://127.0.0.1:24817/v2/library/hello/manifests/latest
curl -i -H 'Range: bytes=0-11' "http://127.0.0.1:24817/v2/library/hello/blobs/$(sed -n 's/^blob=//p' $D/fake.out)"
dist/oci-mirror-proxy status --admin-socket $D/admin.sock
```

Verified in this run:

- `/v2/` answers 200 with the registry API version header.
- Manifest GET and HEAD return 200 with the `Docker-Content-Digest` header.
- A range request returns 206 with the correct `Content-Range`.
- A full blob has exactly its advertised size.
- A missing image returns 404 `MANIFEST_UNKNOWN`, and the log classifies it as a content miss at info level.
- A dead upstream placed first failed three times, its breaker opened, and it was then
  skipped entirely (its request count stopped at 3) while the healthy upstream served every request.
- A hand edit to the config file followed by SIGHUP was applied, and an unchanged
  upstream kept its counters.
- A syntactically broken config on SIGHUP was rejected, and the proxy kept serving.
- SIGTERM stopped the proxy cleanly and removed the admin socket.
- `dockerd apply` and `revert` were checked for the containerd store (create, refuse
  an unmanaged file, `--force` with backup, restore) and for the legacy store
  (merge that preserves unrelated keys, then a byte-exact restore).
- `doctor` reported each unreachable component with a specific reason and exited 1.

## What is not verified

- **A real dockerd pulling through the proxy.** The sandbox used for development had
  no running Docker daemon, and Docker Hub was not reachable. The containerd `hosts.toml`
  and legacy `registry-mirrors` outputs are verified against the moby source for
  their semantics, but not against a live dockerd. Before relying on the proxy, run the
  flow in [docs/operations.md](operations.md#setting-up-dockerd) on a Linux host with
  dockerd, and then `docker pull` an image.
- **Rootless dockerd.** The rootless pid file location is an assumption, and loopback
  reachability depends on the network driver. See the operations guide.
- **Long-running soak.** Breaker and token behavior over hours, and memory use under
  sustained load, are not measured.

## Tooling notes

- The `staticcheck` binary installed on the development machine was built with Go
  1.24, which cannot analyze a Go 1.26 module. Build a current staticcheck with the
  project toolchain before relying on `make staticcheck`. The `check` target does
  not include it.
- `golangci-lint` is not installed. The dependency rule is enforced by `make deps-check`
  instead, using `go list`, which needs no extra tool.
