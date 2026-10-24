# Homebrew distribution

Homebrew packages are maintained in
[`zmide/homebrew-tap`](https://github.com/zmide/homebrew-tap). Binaries and
`SHA256SUMS` are published in this application's GitHub Releases.

```sh
brew install zmide/tap/oci-mirror-proxy
```

See the [installation guide](../apps/docs/content/docs/installation.mdx#homebrew)
for configuration, services, upgrades, and removal.

## Release automation

After publishing a stable release, the release workflow sends an
`update-package` repository dispatch to `zmide/homebrew-tap`, containing the
package name and release tag. It does not commit files to this repository.

Configure the `HOMEBREW_TAP_TOKEN` Actions secret here with a fine-grained personal
access token scoped only to `zmide/homebrew-tap`, with `Contents: Read and write`
permission. A GitHub App installation token with equivalent access can also be
used if the workflow is adapted to generate it.

The tap validates the package and latest stable release, generates the formula
from release checksums, installs and tests it on macOS and Linux, and commits
the result to its own default branch. Formula generators and Homebrew tests
live in the tap repository.

A successful notification only confirms that GitHub accepted the event. Monitor
the tap's Actions for installation and publication results. To retry or bootstrap
an existing release, run its `Update package` workflow manually with package
`oci-mirror-proxy` and the stable release tag. The receiving workflow must already
exist on the tap's default branch.
