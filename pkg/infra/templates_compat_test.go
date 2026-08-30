package infra

// Compatibility with the templates at TemplatesRef.
//
// Every other test in this package runs against fixtures: a literal that stands in
// for the HCL, a fake Terraform, a bare repository built in a temp dir. None of them
// read a real outputs.tf, which means none of them can notice the templates drifting
// under the binary. This file does. It is skipped unless LERIAN_TEMPLATES_CHECKOUT
// points at a checkout of lerian-terraform-foundation — locally that is optional; in
// CI a job clones TemplatesRef and sets it, so a bump of the constant to a tag the
// code does not understand fails the build instead of the operator's first run.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

const templatesCheckoutEnv = "LERIAN_TEMPLATES_CHECKOUT"

func templatesCheckout(t *testing.T) string {
	t.Helper()
	path := os.Getenv(templatesCheckoutEnv)
	if path == "" {
		t.Skipf("%s not set; this test reads a real checkout of lerian-terraform-foundation", templatesCheckoutEnv)
	}
	if !IsCheckout(path) {
		t.Fatalf("%s=%s is not a checkout: examples/aws/_modules or examples/aws/backend missing", templatesCheckoutEnv, path)
	}
	return path
}

// outputNames returns the names declared by `output "<name>"` blocks in one file.
func outputNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^output\s+"([^"]+)"`).FindAllStringSubmatch(string(content), -1) {
		names[m[1]] = true
	}
	return names
}

// enginesUnderTest is every engine the Go side has an opinion about: the ones with a
// chart mapping and the ones whose secret payload shape is declared.
func enginesUnderTest() []string {
	seen := map[string]bool{}
	for _, engines := range chartMappers {
		for engine := range engines {
			seen[engine] = true
		}
	}
	for engine := range secretPayloadProperty {
		seen[engine] = true
	}
	var out []string
	for engine := range seen {
		out = append(out, engine)
	}
	sort.Strings(out)
	return out
}

// ReadFacts reads a fixed set of output names from whichever root owns the datastore.
// In shared mode that is products/shared-resources/<engine>; in dedicated mode the
// product's own root. Both must declare them, or the values come out empty and the
// chart gets a host of "".
func TestTemplatesDeclareTheOutputsReadFactsReads(t *testing.T) {
	root := templatesCheckout(t)
	required := []string{"endpoint", "port", "secret_arn", "secret_name", "identifier"}
	userSpellings := []string{"username", "master_username", "admin_username"}

	for _, engine := range enginesUnderTest() {
		for _, owner := range []string{
			filepath.Join("products", "shared-resources", engine),
			filepath.Join("products", "midaz", engine),
		} {
			file := filepath.Join(root, "examples", "aws", owner, "outputs.tf")
			if _, err := os.Stat(file); err != nil {
				// midaz does not use every engine (no msk), and that is fine.
				continue
			}
			names := outputNames(t, file)
			for _, want := range required {
				if !names[want] {
					t.Errorf("%s: output %q missing; ReadFacts reads it", owner, want)
				}
			}
			hasUser := false
			for _, spelling := range userSpellings {
				hasUser = hasUser || names[spelling]
			}
			// Valkey authenticates with a token and has no user; every other engine
			// must expose one under a spelling ReadFacts normalises.
			if !hasUser && engine != "valkey" {
				t.Errorf("%s: no username output under any of %v", owner, userSpellings)
			}
			if engine == "valkey" && !names["transit_encryption_enabled"] {
				t.Errorf("%s: transit_encryption_enabled missing; it decides REDIS_TLS", owner)
			}
		}
	}
}

// In dedicated mode helm-values reads the product root's own helm_values output. The
// midaz roots must still declare it, keyed by component, or the dedicated path breaks
// while the shared path (built in Go) keeps working — the exact divergence the
// nesting was introduced to prevent.
func TestMidazRootsDeclareHelmValues(t *testing.T) {
	root := templatesCheckout(t)
	for engine := range chartMappers["midaz"] {
		file := filepath.Join(root, "examples", "aws", "products", "midaz", engine, "outputs.tf")
		names := outputNames(t, file)
		if !names["helm_values"] {
			t.Errorf("products/midaz/%s: no helm_values output", engine)
		}
		content, _ := os.ReadFile(file)
		// The component key is how the HCL and the Go mapper agree on shape.
		if !regexp.MustCompile(`(?m)^\s+ledger\s*=\s*\{`).Match(content) {
			t.Errorf("products/midaz/%s: helm_values is not keyed by component (no ledger = { ... })", engine)
		}
	}
}

// The mode switch readDatastoreMode looks for must exist as a variable on every
// product root that has a chart mapping, or shared mode can never be selected.
func TestMappedProductRootsDeclareTheModeVariable(t *testing.T) {
	root := templatesCheckout(t)
	for product, engines := range chartMappers {
		for engine := range engines {
			file := filepath.Join(root, "examples", "aws", "products", product, engine, "variables.tf")
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("reading %s: %v", file, err)
			}
			if !regexp.MustCompile(`(?m)^variable\s+"mode"`).Match(content) {
				t.Errorf("products/%s/%s: no variable \"mode\"; shared mode cannot be selected", product, engine)
			}
		}
	}
}
