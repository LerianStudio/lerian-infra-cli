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

Download the archive and `checksums.txt` from the same release, then, in the
directory holding both:

```bash
sha256sum -c checksums.txt --ignore-missing        # Linux
shasum -a 256 -c checksums.txt --ignore-missing    # macOS
```

```powershell
# Windows PowerShell: compare the hash against the archive's line in checksums.txt
(Get-FileHash .\lerian-infra_<version>_Windows_x86_64.zip -Algorithm SHA256).Hash.ToLower()
Select-String -Path .\checksums.txt -Pattern 'Windows_x86_64'
```

Then unpack it — `tar xzf lerian-infra_<version>_Darwin_arm64.tar.gz`, or Explorer's
"Extract All" for the Windows `.zip` — and put `lerian-infra` on your `PATH`.

Or from source, with Go 1.26:

```bash
go install github.com/LerianStudio/lerian-infra-cli/cmd/lerian-infra@latest
```

A binary built this way reports its version as `dev`.

## Prerequisites

`terraform` >= 1.10.0, the **AWS CLI v2**, and `git` on your `PATH`. The CLI shells
out to all three and checks for each one before the first call that needs it, by name
— including the AWS CLI's major version, since v1 cannot export a profile's
credentials and would otherwise fail later, looking like a broken profile.
`kubectl` is for the step after the cluster exists; this CLI never calls it.

The AWS CLI has to be **configured**, not just installed — it is what resolves
credentials and answers `sts get-caller-identity`, which every run is checked
against. Configure one profile per account you deploy into: dev, stg and prd are
separate AWS accounts, so that is normally three.

```bash
aws configure sso --profile lerian-dev   # IAM Identity Center
aws configure --profile lerian-dev       # access key and secret
```

Either mechanism works — the CLI reads whatever your profile uses and never assumes
SSO. Credentials already in the environment (CI, IRSA) work too: pass `--profile ''`
— the empty string is how you say "use what is already here", and it is not the same
as leaving the flag out. Omitting it in a terminal lists the profiles to pick from;
omitting it in CI is an error naming the flag, because choosing the identity that
creates infrastructure is not a guess this tool makes.

`init` lists the profiles it finds in `~/.aws` with the account each one currently
resolves to, so a mistyped profile or an expired session is visible before anything
is written. On a machine that cannot reach AWS at all — no AWS CLI, or no credentials —
`--account <id> --region <region>` writes the configuration anyway, and the account
is verified later, at apply time. The region has to be stated there: `~/.aws/config`
is read by the very tool that is missing.

## The templates

The CLI does not embed the Terraform. It drives a checkout of
`lerian-terraform-foundation`, and **which release of it to run is yours to choose** —
the two repositories have separate version lines, and the binary pins neither:

```bash
lerian-infra init --env dev --clone --templates-ref v1.6.0   # into ~/lerian/lerian-terraform-foundation
lerian-infra init --env dev --sync  --templates-ref v1.7.0   # move an existing checkout to another tag
```

`--templates-ref` has no default. Run either command without it and the error lists
the tags that exist, newest first.

What the binary does declare is a floor — the oldest templates its chart mapping
understands, printed by `lerian-infra --version`. Below it the same product comes out
one shape in shared mode and another in dedicated, so below it is refused; above it,
anything you name is yours to run. Every command prints the tag the checkout is on.

Already inside a checkout of the templates? Nothing to do — the CLI finds it by
walking up from the working directory.

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
// The account guard, in the order it has to run. Skipping any of it is how a
// "dev" run reaches a production account: the bucket, the region and the live
// credentials must agree before a single Terraform process starts.
config, err := infra.LoadEnvConfig(layout, "dev")
if err != nil {
    return err
}
backend, err := infra.LoadBackend(layout, "dev")
if err != nil {
    return err
}
if err := infra.CheckBackend(backend, config); err != nil {
    return err
}
if _, err := infra.VerifyAccount(ctx, infra.CLIIdentity{}, config); err != nil {
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

That guard sequence is not optional and has no bypass. `NewRunner` cannot run it for
you — it takes a `Backend` you already loaded — so a consumer that omits it gets a
runner that will happily drive the wrong account. Surface its errors; do not route
around them.

`HelmValues` takes the units of ONE product: values are keyed by that chart's
components and `secret_refs` by engine, so a call spanning two products is refused
rather than merged into a document that fits neither.

## Development

```bash
make build    # bin/lerian-infra
make test     # go test ./... -cover
make lint     # gofmt + go vet. CI adds golangci-lint and gosec through the
              # shared Go analysis workflow, so a green make is necessary, not
              # sufficient.
```

Commits follow [conventional commits](https://www.conventionalcommits.org/): the
shared PR-validation workflow checks the title and semantic-release reads the
commits to cut the tag. A release publishes binaries for Linux
and macOS on `amd64` and `arm64`, plus Windows `amd64`, with `checksums.txt`.

## Security

Report vulnerabilities per [SECURITY.md](SECURITY.md), not through issues.

## License

Apache 2.0. See [LICENSE](LICENSE).
