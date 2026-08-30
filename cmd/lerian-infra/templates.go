package main

// Acquiring and keeping the templates.
//
// The binary and the Terraform templates ship from ONE tag, and the operator-facing
// half of that decision lives here: what is offered, what is refused, and what is
// said out loud when the two halves do not match.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/LerianStudio/lerian-infra-cli/pkg/infra"
)

// devVersion is what a binary built without the release ldflag reports. It is not a
// tag, so there is nothing to pin to, and every path that assumes a tag has to say
// so instead of pretending.
const devVersion = "dev"

// validateTemplateFlags rejects combinations that cannot both be honoured.
//
// Each of these is an error rather than a precedence rule. An operator who typed
// --clone --no-clone does not know which they want, and choosing for them buries the
// confusion in a run that then does something they did not ask for.
func (o initOptions) validateTemplateFlags() error {
	if o.clone && o.noClone {
		return errors.New("--clone and --no-clone cannot both be given\n" +
			"Pass whichever describes what should happen when no checkout is found.")
	}
	if o.repo != "" && o.clone {
		return errors.New("--repo and --clone cannot both be given\n" +
			"--repo says where the templates already are; --clone asks for them to be\n" +
			"downloaded. Pick the one that is true.")
	}
	if o.sync && o.repo != "" {
		return errors.New("--sync cannot be used with --repo\n" +
			"A checkout you pointed at is yours: this command does not move it. Sync\n" +
			"only applies to the managed checkout at ~/lerian/lerian-terraform-foundation.")
	}
	return nil
}

// acquireTemplates offers, and on acceptance performs, the clone.
//
// It runs only when nothing was found anywhere — never to replace a checkout the
// operator named.
func acquireTemplates(
	ctx context.Context,
	opts initOptions,
	ask *prompter,
	out io.Writer,
) (infra.Layout, error) {
	theme := newStyle(out)

	if opts.noClone {
		return infra.Layout{}, errors.New("no checkout found, and --no-clone was given\n" +
			"Point at one with --repo or $LERIAN_TF_REPO.")
	}

	git, err := infra.NewGitCLI()
	if err != nil {
		return infra.Layout{}, err
	}
	path, err := infra.ManagedCheckoutPath(opts.templatesDir)
	if err != nil {
		return infra.Layout{}, err
	}

	// The ref is declared by the build, not derived from its version: the CLI and
	// the templates release separately, so the pairing has to be written down.
	ref := infra.TemplatesRef

	fmt.Fprintf(out, "\n%s\n\n", theme.bold("==> Templates"))
	fmt.Fprint(out, wrapIndent(
		"This binary is lerian-infra "+version+" and drives lerian-terraform-foundation "+
			"at "+ref+": the chart mapping compiled in here and the HCL at that tag are "+
			"two halves of one contract. So the clone is pinned to that tag, never to a "+
			"branch.", "  ", 76)+"\n\n")
	if version == devVersion {
		// A local build still knows which templates it wants; what nobody can vouch
		// for is whether its source matches what that tag was tested against. Worth a
		// note, not an alert — nothing here is a consequence the operator is accepting.
		fmt.Fprintf(out, "  %s\n\n", theme.dim(
			"This is a local build (version \"dev\"). The templates ref comes from its "+
				"source, which may be ahead of or behind any release."))
	}

	fmt.Fprintf(out, "    git clone %s\n      %s\n      %s\n\n",
		refLabel(ref), infra.TemplatesRepoURL(), path)

	// --auto-approve deliberately does NOT authorise this.
	//
	// It means "skip the confirmation before writing the files I asked for", and CI
	// passes it as a matter of routine. Letting it also authorise a clone would make
	// every CI run capable of downloading a repository nobody asked it to fetch —
	// the precise accident the explicit flags exist to prevent. Acquiring templates
	// is its own decision, so it takes its own flag.
	if !opts.clone {
		if !ask.interactive {
			return infra.Layout{}, errors.New(
				"no checkout found, and cloning needs a decision\n" +
					"Pass --clone to create the managed checkout, --no-clone to fail instead,\n" +
					"or point at an existing one with --repo. --auto-approve does not imply\n" +
					"--clone: a CI run must never download a repository by accident.")
		}
		if err := ask.confirm(out, "Clone the templates there now?"); err != nil {
			return infra.Layout{}, err
		}
	}

	// The clone is the one step here that takes seconds with nothing to show, which
	// is exactly what the spinner exists for.
	var mu sync.Mutex
	spin := newSpinner(&mu, out, "cloning "+refLabelShort(ref))
	err = infra.CloneTemplates(ctx, git, path, ref)
	spin.Stop()
	if err != nil {
		return infra.Layout{}, err
	}

	fmt.Fprintf(out, "  ok  %s\n", path)
	return infra.NewLayout(path)
}

// runSync moves the managed checkout onto the templates ref this binary declares,
// and does nothing else.
func runSync(ctx context.Context, opts initOptions, out io.Writer) error {
	theme := newStyle(out)

	git, err := infra.NewGitCLI()
	if err != nil {
		return err
	}
	path, err := infra.ManagedCheckoutPath(opts.templatesDir)
	if err != nil {
		return err
	}
	if !infra.IsCheckout(path) {
		return fmt.Errorf("no managed checkout at %s\n"+
			"Nothing to sync. Create it with 'lerian-infra init --env <env> --clone'.", path)
	}

	target := infra.TemplatesRef
	before := infra.InspectCheckout(ctx, git, path, true)
	fmt.Fprintf(out, "\n%s\n\n", theme.bold("==> Sync"))
	fmt.Fprintf(out, "  binary    lerian-infra %s\n", version)
	fmt.Fprintf(out, "  checkout  %s\n", path)
	fmt.Fprintf(out, "  from      %s\n", refOrUntagged(before.Ref))
	fmt.Fprintf(out, "  to        %s\n\n", target)

	if before.AtVersion(target) {
		fmt.Fprintf(out, "  already there — nothing to do\n\n")
		return nil
	}
	if err := infra.SyncTemplates(ctx, git, path, target); err != nil {
		return err
	}

	fmt.Fprintf(out, "  ok  now at %s\n", target)
	// Answered before it is asked: "did I just lose my tfvars?" is the first thing an
	// operator wonders here, and answering after the fact is worth less.
	fmt.Fprint(out, theme.dim(wrapIndent(
		"Your environments.conf and every envs/*.tfvars are untouched — they are "+
			"gitignored, and a checkout does not move untracked or ignored files.",
		"  ", 76))+"\n\n")
	return nil
}

// warnVersionMismatch says when the templates in use are not the ones this binary
// declares it was built against.
//
// It warns and never blocks. The operator may have a reason, and stopping them in the
// middle of a destroy would be worse than the risk being reported. The message says
// what the mismatch actually causes, because "versions differ" alone does not tell
// anyone whether to care.
//
// A local build is not exempt: it declares a templates ref like any other, and a
// developer running against the wrong tag benefits from hearing it just as much.
func warnVersionMismatch(ctx context.Context, out io.Writer, layout infra.Layout, source checkoutSource) {
	git, err := infra.NewGitCLI()
	if err != nil {
		return
	}
	state := infra.InspectCheckout(ctx, git, layout.Root, source == sourceManaged)
	if state.AtVersion(infra.TemplatesRef) {
		return
	}

	theme := newStyle(out)
	fmt.Fprintf(out, "\n  %s\n", theme.bold("Templates are not at the ref this binary declares."))
	fmt.Fprintf(out, "    this binary   lerian-infra %s, built for templates %s\n", version, infra.TemplatesRef)
	fmt.Fprintf(out, "    checkout at   %s\n\n", refOrUntagged(state.Ref))
	fmt.Fprint(out, theme.dim(wrapIndent(
		"The chart mapping in this binary was written against the HCL at "+infra.TemplatesRef+
			". Running it against another tag is how the same product comes out one shape "+
			"in shared mode and another in dedicated.", "    ", 76))+"\n")

	if source == sourceManaged {
		fmt.Fprintf(out, "\n    lerian-infra init --env <env> --sync\n\n")
		return
	}
	// A checkout the operator pointed at is theirs. Blocking it would break exactly
	// the workflow of someone developing a template.
	fmt.Fprint(out, theme.dim(wrapIndent(
		"This checkout came from "+string(source)+", so it is not managed and nothing "+
			"was changed.", "    ", 76))+"\n\n")
}

// templatesRepoForTest exposes the resolved clone source, so the mirror override is
// covered without reaching for the network.
func templatesRepoForTest() string { return infra.TemplatesRepoURL() }

func refLabel(ref string) string {
	if ref == "" {
		return "(default branch)"
	}
	return "--branch " + ref
}

func refLabelShort(ref string) string {
	if ref == "" {
		return "main"
	}
	return ref
}

func refOrUntagged(ref string) string {
	if ref == "" {
		return "untagged"
	}
	return ref
}
