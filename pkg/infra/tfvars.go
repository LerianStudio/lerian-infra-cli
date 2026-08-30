package infra

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// placeholderPattern matches the tokens the committed templates leave behind for
// the operator to replace. The second alternative catches a bare <SHOUTING> token
// that the first one's vocabulary does not know about.
var placeholderPattern = regexp.MustCompile(
	`<(PUT-YOUR|YOUR|SEU|seu|CHANGE-ME|CHANGEME|REPLACE)[^>]*>|<[A-Z][A-Z0-9_-]{3,}>`)

// Readiness says whether one root can be planned in this environment.
type Readiness struct {
	Unit Unit
	// Problem is empty when the root is ready, and otherwise one operator-facing
	// line saying what is missing.
	Problem string
	// Remediation is what to do about Problem.
	Remediation string
}

// Ready reports whether the root can run.
func (r Readiness) Ready() bool { return r.Problem == "" }

// VarFile is <root>/envs/<env>.tfvars, the only variables file a stack reads.
func VarFile(unit Unit, env string) string {
	return filepath.Join(unit.Dir, "envs", env+".tfvars")
}

// CheckReadiness verifies that every unit has its envs/<env>.tfvars and that no
// placeholder survives in it.
//
// It returns one entry per unit rather than stopping at the first problem: an
// operator setting up an environment wants the whole list of files to copy, not
// one of them at a time.
//
// Read-only actions do not call this. They never pass -var-file, so the variables
// file is irrelevant to them, and demanding it would block reading the outputs of
// a stack somebody else applied.
func CheckReadiness(units []Unit, env string) []Readiness {
	report := make([]Readiness, 0, len(units))
	for _, unit := range units {
		report = append(report, checkUnitReadiness(unit, env))
	}
	return report
}

func checkUnitReadiness(unit Unit, env string) Readiness {
	path := VarFile(unit, env)
	example := path + "-example"

	if _, err := os.Stat(path); err != nil {
		if _, exampleErr := os.Stat(example); exampleErr == nil {
			return Readiness{
				Unit:    unit,
				Problem: fmt.Sprintf("missing envs/%s.tfvars", env),
				Remediation: fmt.Sprintf("*.tfvars is gitignored and *.tfvars-example is the committed template:\n"+
					"  cp %s \\\n     %s\n  $EDITOR %s", example, path, path),
			}
		}
		return Readiness{
			Unit:    unit,
			Problem: fmt.Sprintf("missing envs/%s.tfvars, and there is no %s.tfvars-example either", env, env),
			Remediation: fmt.Sprintf("This stack may not support the '%s' environment yet. "+
				"See what exists in %s.", env, filepath.Join(unit.Dir, "envs")),
		}
	}

	lines, err := placeholderLines(path)
	if err != nil {
		return Readiness{
			Unit:        unit,
			Problem:     fmt.Sprintf("cannot read envs/%s.tfvars: %v", env, err),
			Remediation: "Check the permissions of the file.",
		}
	}
	if len(lines) > 0 {
		return Readiness{
			Unit: unit,
			Problem: fmt.Sprintf("unresolved placeholder(s) on %d line(s) of envs/%s.tfvars",
				len(lines), env),
			Remediation: "Replace every <...> token with a real value before applying:\n  " +
				strings.Join(lines, "\n  "),
		}
	}
	return Readiness{Unit: unit}
}

// placeholderLines returns the numbered lines that still carry a placeholder.
// Comments are stripped first: the committed templates legitimately write things
// like "<that address>" in prose, and a real tfvars keeps those comments.
func placeholderLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var found []string
	var comments commentScanner
	scanner := bufio.NewScanner(file)
	for number := 1; scanner.Scan(); number++ {
		line := scanner.Text()
		// HCL accepts both # and // for comments, and a comment can trail a value.
		// Matching the raw line reported a placeholder inside a note — "# was
		// <PUT-YOUR-VPC-ID>" — and CheckReadiness then refused to plan a file that
		// was complete.
		code := comments.code(line)
		if strings.TrimSpace(code) == "" {
			continue
		}
		if placeholderPattern.MatchString(code) {
			found = append(found, fmt.Sprintf("%d: %s", number, strings.TrimSpace(line)))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return found, nil
}

// commentScanner strips HCL comments from a file one line at a time.
//
// It is stateful because HCL has block comments: /* ... */ can open on one line and
// close on another, and a placeholder or a region assignment inside one is prose,
// not configuration. A line-at-a-time function cannot know it is inside a block.
//
// Quotes are tracked too, because a # or a // inside a string is not a comment: a
// bucket named "lerian-tfstate-dev#1" must survive intact.
type commentScanner struct {
	inBlock bool
}

// code returns the part of line that is configuration, with every comment removed.
func (c *commentScanner) code(line string) string {
	var out []byte
	inQuotes := false
	for i := 0; i < len(line); i++ {
		if c.inBlock {
			if line[i] == '*' && i+1 < len(line) && line[i+1] == '/' {
				c.inBlock = false
				i++
			}
			continue
		}
		switch {
		case inQuotes && line[i] == '\\' && i+1 < len(line):
			out = append(out, line[i], line[i+1])
			i++
		case line[i] == '"':
			inQuotes = !inQuotes
			out = append(out, line[i])
		case inQuotes:
			out = append(out, line[i])
		case line[i] == '#':
			return string(out)
		case line[i] == '/' && i+1 < len(line) && line[i+1] == '/':
			return string(out)
		case line[i] == '/' && i+1 < len(line) && line[i+1] == '*':
			c.inBlock = true
			i++
		default:
			out = append(out, line[i])
		}
	}
	return string(out)
}

// stripComment removes the comments from a single self-contained line.
func stripComment(line string) string {
	var scanner commentScanner
	return scanner.code(line)
}
