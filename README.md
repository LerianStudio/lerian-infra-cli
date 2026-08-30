# lerian-infra-cli

**CLI and Go library** for provisioning the infrastructure the Lerian products run on.
It drives the Terraform templates in
[lerian-terraform-foundation](https://github.com/LerianStudio/lerian-terraform-foundation):
discovers what can be deployed, runs the stacks in dependency order, refuses to touch an
AWS account other than the one an environment declares, and hands the resulting
endpoints to the Helm charts.

**Today it covers AWS on Terraform.** GCP, Azure and CloudFormation are where it is
going, not where it is — the repository description states the destination; this file
states what works.

Two things live here:

| | What | Who uses it |
| --- | --- | --- |
| `cmd/lerian-infra` | the binary | operators, CI pipelines |
| `pkg/infra` | the library the binary is a thin face over | the Lerian BYOC wizard, and anything else that needs to drive the templates from Go |

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/LerianStudio/lerian-infra-cli/main/scripts/install.sh | sh
```

The script detects your platform, downloads the matching release, **verifies it against
the published checksums**, and installs into the first writable directory among
`~/.local/bin`, `~/bin` and `/usr/local/bin`. It never calls `sudo`, and installs
nothing if the checksum does not match.

| Variable | Does |
| --- | --- |
| `LERIAN_INFRA_VERSION` | Install a specific tag instead of the latest, e.g. `v1.0.0`. |
| `INSTALL_DIR` | Install somewhere else. |

Prefer to do it by hand, or on Windows? Take the archive from the [releases
page](https://github.com/LerianStudio/lerian-infra-cli/releases) and verify it
yourself:

```bash
tar xzf lerian-infra_<version>_Darwin_arm64.tar.gz
sha256sum -c checksums.txt --ignore-missing
```

Or from source, with Go 1.26:

```bash
go install github.com/LerianStudio/lerian-infra-cli/cmd/lerian-infra@latest
```

A binary built this way reports its version as `dev`.

**Runtime dependencies:** `terraform` >= 1.10.0, `aws`, and `git` on your `PATH`. The
CLI shells out to all three and checks for them before doing anything. `kubectl` is
needed for the step after the cluster exists; the CLI never calls it.

## The templates

The CLI does not embed the Terraform. It drives a checkout of
`lerian-terraform-foundation`, and each version of the CLI declares which tag of that
repository it was built against. The chart mapping compiled into the binary and the
`helm_values` expressions in the HCL are two halves of one contract, so the pairing is
not left to chance:

```bash
lerian-infra init --env dev --clone     # clones the declared tag into ~/lerian/lerian-terraform-foundation
lerian-infra init --env dev --sync      # after upgrading the binary, moves the checkout to match
```

Every command prints which checkout and which tag it is reading, and warns when they
do not match what the binary expects. If you are already inside a checkout of the
templates, nothing to do — the CLI finds it by walking up from the working directory.

**The step-by-step for standing up an environment lives with the templates:**
[lerian-terraform-foundation › AWS](https://github.com/LerianStudio/lerian-terraform-foundation#aws).
It is not repeated here because two copies of a tutorial drift.

## As a library

```go
import "github.com/LerianStudio/lerian-infra-cli/pkg/infra"
```

`pkg/infra` is the whole engine; the binary adds a terminal on top. Nothing in the
package prints — progress reaches the caller through the `infra.Progress` interface,
which is what lets the same run drive a terminal checklist or a polled web UI without
parsing text.

```go
layout, err := infra.NewLayout(repoRoot)
if err != nil {
    return err
}
catalog, err := infra.Discover(layout)                    // products/*/*/main.tf
if err != nil {
    return err
}
stages, err := infra.Resolve(layout, catalog, "midaz")    // dependency-ordered
if err != nil {
    return err
}
runner, err := infra.NewRunner(infra.RunnerOptions{
    Layout: layout, Env: "dev", Backend: backend, Terraform: tf,
    Jobs: 4, Progress: myProgress, RunDir: dir,           // dir must be 0700
})
if err != nil {
    return err
}
results, err := runner.Execute(ctx, stages, infra.ActionApply, myConfirm)
if err != nil {
    return err
}
doc, err := runner.HelmValues(ctx, infra.Units(stages))
```

The account guard runs before `NewRunner` (`LoadEnvConfig`, `LoadBackend`,
`CheckBackend`, `VerifyAccount`) and has no bypass. A consumer of this library is
expected to surface its errors, not route around them.

## Development

```bash
make build    # bin/lerian-infra
make test     # go test ./... -cover
make lint     # gofmt + go vet — the same two checks CI runs, nothing else
```

Commits follow [conventional commits](https://www.conventionalcommits.org/); CI
enforces it and semantic-release cuts the tag. A release publishes binaries for Linux
and macOS on `amd64` and `arm64`, plus Windows `amd64`, with `checksums.txt`.

## Security

Report vulnerabilities per [SECURITY.md](SECURITY.md), not through issues.

## License

Apache 2.0. See [LICENSE](LICENSE).
