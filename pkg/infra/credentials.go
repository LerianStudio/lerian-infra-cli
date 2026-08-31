package infra

// Credentials are resolved once for a whole run and handed to every Terraform
// process, instead of letting each one resolve the profile for itself.
//
// The reason is a race we caused. With AWS_PROFILE set, every terraform process
// resolves the profile independently, and for an SSO profile that means each one
// may decide the cached token needs refreshing. Four of them starting together —
// which is exactly what a parallel stage does — refresh concurrently and fight
// over the same file:
//
//	failed to replace old cached SSO token file, rename
//	~/.aws/sso/cache/<hash>.json.tmp-1787066430403425000 ->
//	~/.aws/sso/cache/<hash>.json: no such file or directory
//
// One process renames its temporary file into place, the next finds the one it
// wrote already gone. The failure is intermittent, depends on how many units share
// a stage, and reads like a broken credential rather than a collision.
//
// Resolving once removes the race by construction: the children receive concrete
// keys and never touch the SSO cache at all.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Credentials is a resolved, short-lived AWS credential set.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// Environment renders the credentials as environment variables for a child
// process. AWS_PROFILE is deliberately absent from the result: a child that sees
// both would resolve the profile and go back to the SSO cache.
func (c Credentials) Environment() map[string]string {
	if c.AccessKeyID == "" {
		return nil
	}
	env := map[string]string{
		"AWS_ACCESS_KEY_ID":     c.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY": c.SecretAccessKey,
	}
	if c.SessionToken != "" {
		env["AWS_SESSION_TOKEN"] = c.SessionToken
	}
	return env
}

// ErrNoAWSCLI is returned when the AWS CLI is absent.
//
// It is checked early, by name, for the same reason ErrNoGit and
// MinTerraformVersion are: without it the first AWS call fails as
// `exec: "aws": executable file not found in $PATH`, buried inside a credentials
// error, and the operator reads that as a broken profile rather than a missing tool.
var ErrNoAWSCLI = errors.New("infra: the AWS CLI (aws) was not found in PATH")

// RequireAWSCLI verifies the AWS CLI is installed before anything tries to use it.
//
// The CLI is not optional and not replaceable by a Go SDK here: it is the only
// implementation that can complete an SSO refresh, and it is what resolves whichever
// credential mechanism the operator's profile happens to use.
//
// The message names both ways a profile is configured. SSO is common at Lerian and
// not universal — a profile with an access key and secret in ~/.aws/credentials is
// just as valid, and telling somebody who uses one to run `aws configure sso` sends
// them to fix something that is not broken.
func RequireAWSCLI() error {
	if _, err := exec.LookPath("aws"); err != nil {
		return fmt.Errorf("%w\n"+
			"Install the AWS CLI v2, then configure a profile for each account you\n"+
			"deploy into — one per environment, since dev, stg and prd are separate\n"+
			"accounts:\n"+
			"  aws configure sso --profile lerian-dev      # IAM Identity Center\n"+
			"  aws configure --profile lerian-dev          # access key and secret\n"+
			"Either works. Ambient credentials in the environment work too: pass\n"+
			"--profile '' with --account to say which account they reach.\n"+
			"  https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html",
			ErrNoAWSCLI)
	}
	return nil
}

// ResolveCredentials asks the AWS CLI to export the profile's current credentials,
// refreshing the SSO token once if it needs it.
//
// The AWS CLI is used rather than a Go SDK for one reason that matters here: it is
// the only implementation that can complete an SSO refresh, and it is already a
// hard requirement of this repository. An empty profile means ambient credentials,
// which the children inherit as they are — there is nothing to resolve and no cache
// to race over.
func ResolveCredentials(ctx context.Context, profile string) (Credentials, error) {
	if profile == "" {
		return Credentials{}, nil
	}

	command := exec.CommandContext(ctx, "aws", "configure", "export-credentials",
		"--profile", profile, "--format", "env-no-export")
	// stdout and stderr are kept apart, and only stdout is parsed. Merging them had
	// two consequences: a stderr line shaped like KEY=VALUE was read as a credential,
	// and a non-zero exit after the CLI had already printed credentials put
	// AWS_SECRET_ACCESS_KEY into the error text, which the caller then prints.
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return Credentials{}, fmt.Errorf("infra: cannot resolve credentials for profile %q: %w\n%s\n\n"+
			"An expired SSO session is the usual cause:\n  aws sso login --profile %s",
			profile, err, strings.TrimSpace(stderr.String()), profile)
	}

	var credentials Credentials
	for _, line := range strings.Split(string(output), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch key {
		case "AWS_ACCESS_KEY_ID":
			credentials.AccessKeyID = value
		case "AWS_SECRET_ACCESS_KEY":
			credentials.SecretAccessKey = value
		case "AWS_SESSION_TOKEN":
			credentials.SessionToken = value
		}
	}
	if credentials.AccessKeyID == "" {
		return Credentials{}, fmt.Errorf(
			"infra: profile %q produced no credentials\n"+
				"  aws configure export-credentials --profile %s --format env-no-export",
			profile, profile)
	}
	return credentials, nil
}
