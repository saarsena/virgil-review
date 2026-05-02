package difffilter

import (
	"strings"
	"testing"
)

func TestFilter_EmptyAndNoHeaders(t *testing.T) {
	if got := Filter("").Diff; got != "" {
		t.Errorf("empty diff: got %q, want \"\"", got)
	}
	const plain = "hello, no headers here"
	if got := Filter(plain).Diff; got != plain {
		t.Errorf("no headers: got %q, want unchanged", got)
	}
}

func TestFilter_DropsLockfile(t *testing.T) {
	in := joinDiffs(
		section("main.go", "+func Foo() {}\n"),
		section("go.sum", "+abc def\n"),
	)
	r := Filter(in)
	if !strings.Contains(r.Diff, "main.go") {
		t.Error("expected main.go to be kept")
	}
	if strings.Contains(r.Diff, "go.sum") {
		t.Error("expected go.sum to be dropped")
	}
	if len(r.Dropped) != 1 || r.Dropped[0] != "go.sum" {
		t.Errorf("Dropped = %v, want [go.sum]", r.Dropped)
	}
}

func TestFilter_DropsDirPrefix(t *testing.T) {
	in := joinDiffs(
		section("src/foo.js", "+console.log(1)\n"),
		section("node_modules/lodash/index.js", "+x\n"),
		section("vendor/github.com/x/y/z.go", "+y\n"),
	)
	r := Filter(in)
	if strings.Contains(r.Diff, "node_modules/") {
		t.Error("expected node_modules/ to be dropped")
	}
	if strings.Contains(r.Diff, "vendor/") {
		t.Error("expected vendor/ to be dropped")
	}
	if !strings.Contains(r.Diff, "src/foo.js") {
		t.Error("expected src/foo.js to be kept")
	}
	if len(r.Dropped) != 2 {
		t.Errorf("Dropped = %v, want 2 entries", r.Dropped)
	}
}

func TestFilter_DropsSuffix(t *testing.T) {
	in := joinDiffs(
		section("api/api.pb.go", "+x\n"),
		section("static/app.min.js", "+y\n"),
		section("static/app.js", "+z\n"),
	)
	r := Filter(in)
	if strings.Contains(r.Diff, "api.pb.go") {
		t.Error("expected api.pb.go to be dropped")
	}
	if strings.Contains(r.Diff, "app.min.js") {
		t.Error("expected app.min.js to be dropped")
	}
	if !strings.Contains(r.Diff, "app.js") {
		t.Error("expected app.js (non-min) to be kept")
	}
}

func TestFilter_KeepsLookalikes(t *testing.T) {
	// "vendored.go" must NOT match "vendor/" prefix.
	// "package-lock.json.go" must NOT match the basename rule.
	in := joinDiffs(
		section("vendored.go", "+x\n"),
		section("not-package-lock.json.go", "+y\n"),
	)
	r := Filter(in)
	if !strings.Contains(r.Diff, "vendored.go") {
		t.Error("vendored.go was wrongly filtered")
	}
	if !strings.Contains(r.Diff, "not-package-lock.json.go") {
		t.Error("not-package-lock.json.go was wrongly filtered")
	}
	if len(r.Dropped) != 0 {
		t.Errorf("Dropped = %v, want []", r.Dropped)
	}
}

func TestFilter_AllDropped(t *testing.T) {
	in := joinDiffs(
		section("go.sum", "+x\n"),
		section("vendor/foo.go", "+y\n"),
	)
	r := Filter(in)
	if strings.Contains(r.Diff, "diff --git") {
		t.Errorf("expected no surviving diff sections, got %q", r.Diff)
	}
	if len(r.Dropped) != 2 {
		t.Errorf("Dropped = %v, want 2 entries", r.Dropped)
	}
}

func TestFilter_QuotedPath(t *testing.T) {
	// Git quotes paths with spaces/special chars in the diff header.
	// We should still extract the new-side path and apply rules.
	quoted := `diff --git "a/scenes/My Scene.tscn" "b/scenes/My Scene.tscn"
index abc..def 100644
--- "a/scenes/My Scene.tscn"
+++ "b/scenes/My Scene.tscn"
@@ -1 +1 @@
-x
+y
`
	r := Filter(quoted)
	// Not a filtered path → kept as-is.
	if !strings.Contains(r.Diff, "My Scene.tscn") {
		t.Error("expected quoted path to be kept")
	}
	if len(r.Dropped) != 0 {
		t.Errorf("Dropped = %v, want []", r.Dropped)
	}
}

// section produces a single "diff --git" section for path with the given body.
func section(path, body string) string {
	return "diff --git a/" + path + " b/" + path + "\n" +
		"index abc..def 100644\n" +
		"--- a/" + path + "\n" +
		"+++ b/" + path + "\n" +
		"@@ -0,0 +1 @@\n" +
		body
}

func joinDiffs(sections ...string) string {
	return strings.Join(sections, "")
}
