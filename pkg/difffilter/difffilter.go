// Package difffilter strips per-file sections from a unified diff for
// paths Virgil considers noise: dependency lockfiles, vendored code,
// build output, and other generated content.
//
// Filtering happens after the diff is fetched from GitHub but before the
// reviewer sees it. The goal is to cut token spend and stop the model
// from "reviewing" content it has no useful judgment about (you can't
// usefully critique a 5000-line lockfile diff).
//
// Path matching is conservative on purpose: only well-known basenames,
// directory prefixes, and suffixes. Anything ambiguous is left in.
// Renames are matched against the new (b/) path.
package difffilter

import (
	"path"
	"strings"
)

// defaultLockfiles are exact basenames of dependency-pin files. These
// are noise to a code reviewer — a hash change tells you nothing.
var defaultLockfiles = map[string]bool{
	"go.sum":            true,
	"package-lock.json": true,
	"yarn.lock":         true,
	"pnpm-lock.yaml":    true,
	"bun.lockb":         true,
	"Cargo.lock":        true,
	"Pipfile.lock":      true,
	"poetry.lock":       true,
	"uv.lock":           true,
	"composer.lock":     true,
	"Gemfile.lock":      true,
	"mix.lock":          true,
	"Podfile.lock":      true,
	"flake.lock":        true,
}

// defaultDirPrefixes match files whose path begins with one of these.
// Trailing slash is required so e.g. "vendored.go" doesn't match "vendor/".
var defaultDirPrefixes = []string{
	"vendor/",
	"node_modules/",
	"dist/",
	"build/",
	"target/",
	"__pycache__/",
	".next/",
	".nuxt/",
	".svelte-kit/",
	"out/",
	".godot/",
	".import/",
}

// defaultSuffixes match files whose path ends with one of these.
var defaultSuffixes = []string{
	".pb.go",
	"_pb2.py",
	"_pb2_grpc.py",
	".min.js",
	".min.css",
	".map",
}

// Result is the output of Filter: the cleaned diff and the list of
// dropped paths (for logging).
type Result struct {
	Diff    string
	Dropped []string
}

// Filter returns the input diff with sections for noise files removed.
// If diff is empty or has no recognizable per-file sections, it is
// returned unchanged.
func Filter(diff string) Result {
	const header = "diff --git "
	if !strings.Contains(diff, header) {
		return Result{Diff: diff}
	}

	// Splitting on the header strips it; we re-prepend on each kept
	// section. The first element is everything before the first header
	// (almost always empty for a real diff), preserved as preamble.
	parts := strings.Split(diff, header)
	preamble := parts[0]
	sections := parts[1:]

	var kept []string
	var dropped []string
	for _, sec := range sections {
		p := pathFromHeader(sec)
		if p != "" && shouldFilter(p) {
			dropped = append(dropped, p)
			continue
		}
		// Unparseable sections are kept rather than silently lost.
		kept = append(kept, sec)
	}

	var out strings.Builder
	out.Grow(len(diff))
	out.WriteString(preamble)
	for _, sec := range kept {
		out.WriteString(header)
		out.WriteString(sec)
	}
	return Result{Diff: out.String(), Dropped: dropped}
}

// pathFromHeader returns the new-side path from a "diff --git" section's
// first line. The header (without the "diff --git " prefix) looks like
// "a/<old> b/<new>" for normal cases and "\"a/<old>\" \"b/<new>\"" when
// either path contains shell-special characters. Returns "" if neither
// shape matches.
func pathFromHeader(section string) string {
	end := strings.IndexByte(section, '\n')
	if end < 0 {
		end = len(section)
	}
	first := section[:end]

	if strings.HasPrefix(first, `"a/`) {
		// Find closing quote of "a/...".
		closeA := strings.IndexByte(first[1:], '"')
		if closeA < 0 {
			return ""
		}
		rest := strings.TrimLeft(first[1+closeA+1:], " ")
		if !strings.HasPrefix(rest, `"b/`) {
			return ""
		}
		bPart := rest[3:]
		closeB := strings.LastIndexByte(bPart, '"')
		if closeB < 0 {
			return ""
		}
		return bPart[:closeB]
	}

	bIdx := strings.Index(first, " b/")
	if bIdx < 0 {
		return ""
	}
	return first[bIdx+3:]
}

func shouldFilter(p string) bool {
	if defaultLockfiles[path.Base(p)] {
		return true
	}
	for _, prefix := range defaultDirPrefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	for _, suffix := range defaultSuffixes {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
}
