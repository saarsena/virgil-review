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
// `.map` is intentionally NOT a bare suffix — that would catch any file
// ending in ".map" (e.g. a real Map.go, sourcemap.go). Scope to the
// JS/CSS sourcemap forms only.
var defaultSuffixes = []string{
	".pb.go",
	"_pb2.py",
	"_pb2_grpc.py",
	".min.js",
	".min.css",
	".js.map",
	".css.map",
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
// either path contains shell-special characters or non-ASCII bytes —
// git uses C-string quoting with backslash escapes (\", \\, \t, \NNN).
// Returns "" if neither shape matches.
func pathFromHeader(section string) string {
	end := strings.IndexByte(section, '\n')
	if end < 0 {
		end = len(section)
	}
	first := section[:end]

	if strings.HasPrefix(first, `"`) {
		_, after, ok := parseQuotedPath(first)
		if !ok {
			return ""
		}
		rest := strings.TrimLeft(first[after:], " ")
		if !strings.HasPrefix(rest, `"`) {
			return ""
		}
		bPath, _, ok := parseQuotedPath(rest)
		if !ok {
			return ""
		}
		return strings.TrimPrefix(bPath, "b/")
	}

	bIdx := strings.Index(first, " b/")
	if bIdx < 0 {
		return ""
	}
	return first[bIdx+3:]
}

// parseQuotedPath parses a git C-quoted path starting with '"'. Returns
// the unescaped contents (still including the leading "a/" or "b/"), the
// index in s immediately after the closing quote, and ok=true on success.
// Recognized escapes: \" \\ \a \b \t \n \v \f \r and 3-digit octal \NNN.
// Unknown escapes are passed through verbatim.
func parseQuotedPath(s string) (string, int, bool) {
	if len(s) < 2 || s[0] != '"' {
		return "", 0, false
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 1; i < len(s); {
		c := s[i]
		if c == '"' {
			return b.String(), i + 1, true
		}
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			i++
			continue
		}
		switch n := s[i+1]; n {
		case '"', '\\':
			b.WriteByte(n)
			i += 2
		case 'a':
			b.WriteByte('\a')
			i += 2
		case 'b':
			b.WriteByte('\b')
			i += 2
		case 't':
			b.WriteByte('\t')
			i += 2
		case 'n':
			b.WriteByte('\n')
			i += 2
		case 'v':
			b.WriteByte('\v')
			i += 2
		case 'f':
			b.WriteByte('\f')
			i += 2
		case 'r':
			b.WriteByte('\r')
			i += 2
		default:
			if i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
				v := (int(s[i+1]-'0') << 6) | (int(s[i+2]-'0') << 3) | int(s[i+3]-'0')
				b.WriteByte(byte(v))
				i += 4
			} else {
				b.WriteByte(c)
				i++
			}
		}
	}
	return "", 0, false
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

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
