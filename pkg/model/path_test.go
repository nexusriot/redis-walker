package model

import "testing"

func TestPathOf(t *testing.T) {
	cases := map[string]string{
		"":        "/",
		"a":       "/a",
		"a/b":     "/a/b",
		"/a/b":    "/a/b",
		"user:1":  "/user:1",
		"/user:1": "/user:1",
	}
	for in, want := range cases {
		if got := PathOf(in); got != want {
			t.Errorf("PathOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrefixOf(t *testing.T) {
	cases := map[string]string{
		"":      "",
		"/":     "",
		"a":     "a/",
		"a/":    "a/",
		"/a":    "/a/",
		"a/b":   "a/b/",
		"/a/b/": "/a/b/",
	}
	for in, want := range cases {
		if got := PrefixOf(in); got != want {
			t.Errorf("PrefixOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParentPrefix(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"a/":     "",
		"/a/":    "",
		"a/b/":   "a/",
		"/a/b/":  "/a/",
		"a/b/c/": "a/b/",
	}
	for in, want := range cases {
		if got := ParentPrefix(in); got != want {
			t.Errorf("ParentPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBaseOf(t *testing.T) {
	cases := map[string]string{
		"a":       "a",
		"a/b":     "b",
		"/a/b":    "b",
		"/a/b/":   "b",
		"user:1":  "user:1",
		"/user:1": "user:1",
	}
	for in, want := range cases {
		if got := BaseOf(in); got != want {
			t.Errorf("BaseOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDisplayDir(t *testing.T) {
	cases := map[string]string{
		"":      "/",
		"a/":    "/a",
		"/a/":   "/a",
		"a/b/":  "/a/b",
		"/a/b/": "/a/b",
	}
	for in, want := range cases {
		if got := DisplayDir(in); got != want {
			t.Errorf("DisplayDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChildKey(t *testing.T) {
	cases := []struct{ prefix, name, want string }{
		{"", "a", "a"},
		{"a/", "b", "a/b"},
		{"/a/", "b", "/a/b"},
		{"a/", "b/", "a/b"},
	}
	for _, c := range cases {
		if got := ChildKey(c.prefix, c.name); got != c.want {
			t.Errorf("ChildKey(%q,%q) = %q, want %q", c.prefix, c.name, got, c.want)
		}
	}
}

func TestGlobEscape(t *testing.T) {
	cases := map[string]string{
		"plain":  "plain",
		"a*b":    `a\*b`,
		"a?b":    `a\?b`,
		"a[1]":   `a\[1\]`,
		`a\b`:    `a\\b`,
		"a/b:c-": "a/b:c-",
	}
	for in, want := range cases {
		if got := globEscape(in); got != want {
			t.Errorf("globEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitChild(t *testing.T) {
	cases := []struct {
		name            string
		prefix, key     string
		wantChild       string
		wantSeg         string
		wantDir, wantOK bool
	}{
		{"root leaf", "", "a", "a", "a", false, true},
		{"root dir", "", "a/b", "a", "a", true, true},
		{"root slash dir", "", "/a/b", "/a", "a", true, true},
		{"root slash leaf", "", "/a", "/a", "a", false, true},
		{"nested leaf", "a/", "a/b", "a/b", "b", false, true},
		{"nested dir", "a/", "a/b/c", "a/b", "b", true, true},
		{"prefix itself", "a/", "a/", "", "", false, false},
		{"empty segment", "a/", "a//b", "", "", false, false},
		{"not under prefix", "a/", "b/c", "", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			child, seg, isDir, ok := splitChild(c.prefix, c.key)
			if ok != c.wantOK || child != c.wantChild || seg != c.wantSeg || isDir != c.wantDir {
				t.Errorf("splitChild(%q,%q) = (%q,%q,%v,%v), want (%q,%q,%v,%v)",
					c.prefix, c.key, child, seg, isDir, ok,
					c.wantChild, c.wantSeg, c.wantDir, c.wantOK)
			}
		})
	}
}

// The root must keep "a/b" and "/a/b" apart: they are two different Redis keys
// that only look alike once the virtual leading slash is added.
func TestSplitChildKeepsLeadingSlashDistinct(t *testing.T) {
	withSlash, _, _, _ := splitChild("", "/a/b")
	without, _, _, _ := splitChild("", "a/b")
	if withSlash == without {
		t.Fatalf("keys /a/b and a/b collapsed into the same child %q", withSlash)
	}
}
