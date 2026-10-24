# Homebrew distribution

The project repository is also a Homebrew tap. It installs the prebuilt GitHub
release for macOS and Linux on ARM64 and x86-64, with no Go build dependency.
User-facing commands, service setup, and removal are documented in the
[installation guide](../apps/docs/content/docs/installation.mdx#homebrew).

## Release updates

After the GitHub release workflow publishes the latest stable release, its
`homebrew` job regenerates `Formula/oci-mirror-proxy.rb` from that release's
`SHA256SUMS` and commits only the formula to the repository's default branch.
Prereleases and releases that are not the latest stable version are skipped.
No separate tap repository or personal access token is required.

The job uses the repository's `GITHUB_TOKEN` with `contents: write`. Repository
rules must allow GitHub Actions to push the formula update to the default branch.
If a branch protection rule blocks the push, update the formula through the normal
review process instead. To regenerate it manually from the repository root:

```sh
tag=v1.0.18
mkdir -p dist/homebrew
gh release download "$tag" --repo runui/oci-mirror-proxy \
  --pattern SHA256SUMS --dir dist/homebrew --clobber
node scripts/update-homebrew.mjs "$tag" dist/homebrew/SHA256SUMS Formula/oci-mirror-proxy.rb
node --test scripts/update-homebrew.test.mjs
```

Update the tag to the desired stable version. The generator rejects missing or
duplicate checksums, invalid checksum entries, and non-stable version tags.
Edit the generator, rather than the generated Ruby file, when changing the formula's
installation, caveats, service, or test definitions.

## Validation

`.github/workflows/homebrew.yml` installs the formula from the checked-out commit
on macOS and Linux, runs `brew test`, and checks the service metadata. It does not
start the proxy or modify Docker settings. Formula generation tests also run in
the release workflow before committing an update.

To test a formula locally, use a tap containing the proposed commit, then run:

```sh
brew install runui/oci-mirror-proxy/oci-mirror-proxy
brew test runui/oci-mirror-proxy/oci-mirror-proxy
brew services info runui/oci-mirror-proxy/oci-mirror-proxy
```

Commits made with `GITHUB_TOKEN` do not trigger another push workflow, so generated
release updates run the generator tests directly; installation CI runs on ordinary
pushes, pull requests, or manual workflow dispatches.
