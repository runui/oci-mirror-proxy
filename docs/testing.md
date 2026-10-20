# Testing

[English](testing.md) | [简体中文](zh-CN/testing.md)

## Running checks

Run `make check` for formatting checks, vet, and tests, including race detection.
Use `make fuzz` for path-parser fuzzing, or run configuration and catalog fuzzing
separately:

```sh
go test ./internal/config -run '^$' -fuzz FuzzDocumentMerge -fuzztime 30s
go test ./internal/cli -run '^$' -fuzz FuzzRemoteJSON -fuzztime 30s
```

For static analysis, install a `staticcheck` version compatible with the project's
Go toolchain and run `make staticcheck`.

## Manual smoke test (real binary, no dockerd)

The standalone fake registry serves `library/hello:latest` for local checks
without Docker. Start it and wait for the `url=` and `blob=` lines in
`$D/fake.out` before running the client commands:

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

## Checking dockerd integration

On a Linux host with dockerd, follow [setting up dockerd](operations.md#setting-up-dockerd),
then run `docker pull hello-world`. For rootless networking, see
[rootless dockerd](operations.md#rootless-dockerd).
