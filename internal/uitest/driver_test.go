package uitest

import "testing"

func TestEntryName(t *testing.T) {
	cases := map[string]string{
		"   plain":                 "plain",
		"📁 folder/":                "folder/",
		"   ahash [blue](hash)[-]": "ahash",
		"📁 cache[1[]/":             "cache[1]/",
		"[red[]name":               "[red]name",
		"[..]":                     "[..]",
		"[yellow]   _private[-]":   "_private",
	}
	for in, want := range cases {
		if got := EntryName(in); got != want {
			t.Errorf("EntryName(%q) = %q, want %q", in, got, want)
		}
	}
}
