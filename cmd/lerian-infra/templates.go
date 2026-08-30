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
	"strings"
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
	if o.sync && (o.clone || o.noClone) {
		return errors.New("--sync cannot be combined with --clone or --no-clone\n" +
			"Sync moves an existing managed checkout; it never creates one, so a clone\n" +
			"decision has nothing to apply to.")
	}
	if o.noClone && o.templatesRef != "" {
		return errors.New("--templates-ref and --no-clone cannot both be given\n" +
			"--no-clone says nothing may be downloaded, so there is no clone for a tag\n" +
			"to apply to.")
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

	// WHETHER to clone is settled before WHAT to clone. A run with neither flag is
	// missing both answers, and the decision to download a repository at all is the
	// one that has to be made first — telling a CI job to pick a tag for a clone it
	// never authorised would be answering the second question first.
	//
	// --auto-approve deliberately does NOT authorise this. It means "skip the
	// confirmation before writing the files I asked for", and CI passes it as a
	// matter of routine. Letting it also authorise a clone would make every CI run
	// capable of downloading a repository nobody asked it to fetch — the precise
	// accident the explicit flags exist to prevent.
	if !opts.clone && !ask.interactive {
		message := "no checkout found, and cloning needs a decision\n" +
			"Pass --clone to create the managed checkout, --no-clone to fail instead,\n" +
			"or point at an existing one with --repo. --auto-approve does not imply\n" +
			"--clone: a CI run must never download a repository by accident."
		if opts.templatesRef == "" {
			// Both answers are missing, and a CI job that fixes one at a time pays
			// for a full run per flag. Say both now.
			message += "\n\n--clone also needs --templates-ref <tag>: which templates release\n" +
				"to run is yours to choose, and this binary declares no default."
		}
		return infra.Layout{}, errors.New(message)
	}

	// The tag is the operator's decision and has no default. A binary that picked
	// one would be pinning the templates to its own release date, which is what
	// --templates-ref exists to stop.
	ref := opts.templatesRef
	if ref == "" {
		return infra.Layout{}, missingRefError(ctx, git, "--clone")
	}
	if infra.RefBelowMin(ref) {
		return infra.Layout{}, fmt.Errorf(
			"templates %s is older than this binary understands\n"+
				"The chart mapping compiled in here was written against %s and later.\n"+
				"Pass a tag at or above it, or use a CLI release from that era.",
			ref, infra.TemplatesMinRef)
	}

	fmt.Fprintf(out, "\n%s\n\n", theme.bold("==> Templates"))
	fmt.Fprint(out, wrapIndent(
		"This is lerian-infra "+version+", cloning lerian-terraform-foundation at "+ref+
			" — the tag you asked for. The clone is pinned to it, never to a branch: the "+
			"chart mapping compiled in here and the HCL in the templates are two halves "+
			"of one contract, and a branch moves under it.", "  ", 76)+"\n\n")
	fmt.Fprintf(out, "  %s\n\n", theme.dim(
		"This binary understands "+infra.TemplatesMinRef+" and later."))

	fmt.Fprintf(out, "    git clone %s\n      %s\n      %s\n\n",
		refLabel(ref), infra.TemplatesRepoURL(), path)

	// An operator at a terminal still confirms: they named a tag, not a download.
	if !opts.clone {
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

	target := opts.templatesRef
	if target == "" {
		return missingRefError(ctx, git, "--sync")
	}
	if infra.RefBelowMin(target) {
		return fmt.Errorf("templates %s is older than this binary understands\n"+
			"The chart mapping compiled in here was written against %s and later.",
			target, infra.TemplatesMinRef)
	}
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

// warnTemplatesBelowMin says when the checkout in use is older than the oldest
// templates this binary understands.
//
// It fires on that alone. There is no ref this build "should" be at any more — the
// tag is the operator's — so a checkout at v1.9.0 under a binary tested at v1.5.0
// is a normal, supported combination and saying anything about it would be noise.
// Below the floor is different: the HCL there is a shape the chart mapping compiled
// in here was never written for.
//
// It warns and never blocks. The operator may have a reason, and stopping them in the
// middle of a destroy would be worse than the risk being reported.
func warnTemplatesBelowMin(ctx context.Context, out io.Writer, layout infra.Layout, source checkoutSource) {
	// No git means no way to read the checkout's tag, so there is nothing to compare
	// and the warning is silently skipped. That is deliberate: a checkout handed over
	// with --repo is usable without git, and refusing to run over a missing optional
	// tool would be worse than not knowing the tag.
	git, err := infra.NewGitCLI()
	if err != nil {
		return
	}
	state := infra.InspectCheckout(ctx, git, layout.Root, source == sourceManaged)
	if !infra.RefBelowMin(state.Ref) {
		return
	}

	theme := newStyle(out)
	fmt.Fprintf(out, "\n  %s\n", theme.bold("These templates are older than this binary understands."))
	fmt.Fprintf(out, "    this binary   lerian-infra %s, templates %s and later\n", version, infra.TemplatesMinRef)
	fmt.Fprintf(out, "    checkout at   %s\n\n", refOrUntagged(state.Ref))
	fmt.Fprint(out, theme.dim(wrapIndent(
		"The chart mapping in this binary was written against the HCL at "+infra.TemplatesMinRef+
			" and later. Running it against older HCL is how the same product comes out "+
			"one shape in shared mode and another in dedicated.", "    ", 76))+"\n")

	if source == sourceManaged {
		fmt.Fprintf(out, "\n    lerian-infra init --env <env> --sync --templates-ref %s\n\n",
			infra.TemplatesMinRef)
		return
	}
	// A checkout the operator pointed at is theirs. Blocking it would break exactly
	// the workflow of someone developing a template.
	fmt.Fprint(out, theme.dim(wrapIndent(
		"This checkout came from "+string(source)+", so it is not managed and nothing "+
			"was changed.", "    ", 76))+"\n\n")
}

// missingRefError asks for the tag, and answers the question the ask provokes:
// which tags are there? Listing them costs one ls-remote and turns a refusal into
// a menu. When the network is unavailable the refusal still stands — a default
// would be the pin this design removed — but it says so instead of pretending the
// repository has no tags.
func missingRefError(ctx context.Context, git infra.Git, flagName string) error {
	var message strings.Builder
	fmt.Fprintf(&message, "%s needs a templates tag\n\n", flagName)

	tags, err := git.RemoteTags(ctx, infra.TemplatesRepoURL())
	switch {
	case err != nil:
		fmt.Fprintf(&message, "  the available tags could not be listed: %v\n\n", err)

	// The suggestion has to be a tag this binary would ACCEPT. Naming the newest
	// tag outright is wrong whenever the floor is ahead of what the templates have
	// published — the operator copies the line and gets refused by the very tool
	// that printed it.
	case infra.LatestRef(usableRefs(tags)) != "":
		latest := infra.LatestRef(usableRefs(tags))
		fmt.Fprintf(&message, "  latest    %s\n", latest)
		if others := recentRefs(tags, latest); others != "" {
			fmt.Fprintf(&message, "  also      %s\n", others)
		}
		fmt.Fprintf(&message, "  minimum   %s (the oldest this binary understands)\n\n",
			infra.TemplatesMinRef)
		fmt.Fprintf(&message, "  lerian-infra init --env <env> %s --templates-ref %s\n",
			flagName, latest)
		return errors.New(message.String())

	case infra.LatestRef(tags) != "":
		fmt.Fprintf(&message, "  latest    %s — older than this binary reads\n",
			infra.LatestRef(tags))
		fmt.Fprintf(&message, "  minimum   %s\n\n", infra.TemplatesMinRef)
		fmt.Fprint(&message, wrapIndent(
			"No published tag meets that floor yet. This binary is ahead of the "+
				"templates: use a CLI release built for the tags that exist, or wait "+
				"for "+infra.TemplatesMinRef+".", "  ", 76)+"\n")
		return errors.New(message.String())
	}

	fmt.Fprintf(&message, "  minimum   %s (the oldest this binary understands)\n\n",
		infra.TemplatesMinRef)
	fmt.Fprintf(&message, "  lerian-infra init --env <env> %s --templates-ref <tag>\n\n", flagName)
	fmt.Fprint(&message, "  Tags: https://github.com/LerianStudio/lerian-terraform-foundation/tags\n")
	return errors.New(message.String())
}

// usableRefs keeps the tags this binary would accept: at or above the floor, and no
// prereleases — those exist for the people who already know they want them, and are
// never the right thing to suggest to someone who just asked what to pass.
func usableRefs(tags []string) []string {
	var usable []string
	for _, tag := range tags {
		if strings.Contains(tag, "-") || infra.RefBelowMin(tag) {
			continue
		}
		usable = append(usable, tag)
	}
	return usable
}

// recentRefs names the few usable tags under the latest, so the operator can pick an
// older one without leaving the terminal.
func recentRefs(tags []string, latest string) string {
	var shown []string
	for _, tag := range usableRefs(tags) {
		if tag == latest {
			continue
		}
		shown = append(shown, tag)
		if len(shown) == 3 {
			break
		}
	}
	return strings.Join(shown, "  ")
}

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
