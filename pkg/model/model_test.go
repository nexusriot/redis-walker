package model

import (
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestModel starts an in-memory Redis and returns a Model bound to it.
func newTestModel(t *testing.T, opts ...func(*Options)) (*Model, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	o := Options{Host: mr.Host(), Port: mr.Port()}
	for _, f := range opts {
		f(&o)
	}
	m, err := New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, mr
}

func names(l *Listing) []string {
	out := make([]string, 0, len(l.Nodes))
	for _, n := range l.Nodes {
		if n.IsDir {
			out = append(out, n.Name+"/")
			continue
		}
		out = append(out, n.Name)
	}
	return out
}

func mustLs(t *testing.T, m *Model, prefix string) *Listing {
	t.Helper()
	l, err := m.Ls(prefix)
	if err != nil {
		t.Fatalf("Ls(%q): %v", prefix, err)
	}
	return l
}

func TestNewPingFailure(t *testing.T) {
	// Port 1 is reserved and nothing listens on it.
	_, err := New(Options{Host: "127.0.0.1", Port: "1", DialTimeout: 300 * time.Millisecond})
	if err == nil {
		t.Fatal("expected a connection error")
	}
}

func TestLsRootGroupsChildren(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("/a/b", "1")
	mr.Set("/a/c", "2")
	mr.Set("/top", "3")
	mr.Set("plain:key", "4")

	got := names(mustLs(t, m, ""))
	want := []string{"/a/", "/plain:key", "/top"}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("root listing = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("root listing = %v, want %v", got, want)
		}
	}
}

func TestLsNested(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("/a/b/c", "1")
	mr.Set("/a/leaf", "2")

	l := mustLs(t, m, "/a/")
	if got := names(l); len(got) != 2 || got[0] != "/a/b/" || got[1] != "/a/leaf" {
		t.Fatalf("Ls(/a/) = %v", got)
	}
}

// A key may be both a value and a directory; both entries must be listed and
// must carry the same real Redis key.
func TestLsKeyThatIsAlsoADirectory(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("a", "value")
	mr.Set("a/child", "1")

	l := mustLs(t, m, "")
	if len(l.Nodes) != 2 {
		t.Fatalf("expected a dir and a leaf, got %v", names(l))
	}
	if !l.Nodes[0].IsDir || l.Nodes[1].IsDir {
		t.Fatalf("expected the directory first, got %+v", l.Nodes)
	}
	for _, n := range l.Nodes {
		if n.Key != "a" {
			t.Fatalf("node %+v should keep the real key %q", n, "a")
		}
	}
}

// Regression: keys stored without a leading "/" used to be listed under a
// fabricated "/name" path, so editing them created a *second* key and deleting
// them did nothing.
func TestLsKeepsRealKeyForNonSlashKeys(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("session:42", "payload")

	l := mustLs(t, m, "")
	if len(l.Nodes) != 1 {
		t.Fatalf("listing = %v", names(l))
	}
	n := l.Nodes[0]
	if n.Key != "session:42" {
		t.Fatalf("Key = %q, want %q", n.Key, "session:42")
	}
	if n.Name != "/session:42" {
		t.Fatalf("Name = %q, want %q", n.Name, "/session:42")
	}
	if err := m.Del(n.Key); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if mr.Exists("session:42") {
		t.Fatal("the key was not deleted")
	}
}

func TestLsSkipsDirMarkerButKeepsRealDirNamedDir(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("d/"+dirMarker, "")
	mr.Set("e/"+dirMarker+"/real", "1")

	l := mustLs(t, m, "d/")
	if len(l.Nodes) != 0 {
		t.Fatalf("the .dir marker must stay hidden, got %v", names(l))
	}
	l = mustLs(t, m, "e/")
	if got := names(l); len(got) != 1 || got[0] != "/e/.dir/" {
		t.Fatalf("a real folder named .dir must be visible, got %v", got)
	}
}

// Regression: SCAN MATCH is a glob, so an unescaped "[" in a key name used to
// silently select unrelated keys - including for recursive delete.
func TestGlobCharactersInKeyNames(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("cache[1]/a", "1")
	mr.Set("cache[1]/b", "2")
	mr.Set("cache1/c", "3")
	mr.Set("cacheX/d", "4")

	l := mustLs(t, m, "cache[1]/")
	if got := names(l); len(got) != 2 {
		t.Fatalf("Ls(cache[1]/) = %v, want the 2 keys of that folder", got)
	}
	if err := m.DelDir("cache[1]"); err != nil {
		t.Fatalf("DelDir: %v", err)
	}
	for _, k := range []string{"cache1/c", "cacheX/d"} {
		if !mr.Exists(k) {
			t.Fatalf("DelDir removed the unrelated key %q", k)
		}
	}
	if mr.Exists("cache[1]/a") {
		t.Fatal("DelDir did not remove the target keys")
	}
}

func TestLsExcludePrefixes(t *testing.T) {
	m, mr := newTestModel(t, func(o *Options) {
		o.ExcludePrefixes = []string{"/pcp:", "metrics/"}
	})
	mr.Set("pcp:a", "1")
	mr.Set("/pcp:b", "2")
	mr.Set("metrics/x", "3")
	mr.Set("keep", "4")

	if got := names(mustLs(t, m, "")); len(got) != 1 || got[0] != "/keep" {
		t.Fatalf("listing = %v, want only /keep", got)
	}
}

func TestLsValuePreviewAndTruncation(t *testing.T) {
	m, mr := newTestModel(t, func(o *Options) { o.PreviewBytes = 4 })
	mr.Set("big", "0123456789")
	mr.Set("small", "ab")

	l := mustLs(t, m, "")
	byKey := map[string]*Node{}
	for _, n := range l.Nodes {
		byKey[n.Key] = n
	}
	if got := byKey["big"]; got.Value != "0123" || !got.Truncated || got.Size != 10 {
		t.Fatalf("big preview = %+v", got)
	}
	if got := byKey["small"]; got.Value != "ab" || got.Truncated {
		t.Fatalf("small preview = %+v", got)
	}
}

func TestLsReportsTypeOfNonStringKeys(t *testing.T) {
	m, mr := newTestModel(t)
	mr.HSet("h", "f", "v")
	mr.Set("s", "v")

	byKey := map[string]*Node{}
	for _, n := range mustLs(t, m, "").Nodes {
		byKey[n.Key] = n
	}
	if byKey["h"].Type != "hash" {
		t.Fatalf("hash type = %q", byKey["h"].Type)
	}
	if byKey["s"].Type != TypeString {
		t.Fatalf("string type = %q", byKey["s"].Type)
	}
}

func TestLsReportsTTL(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("k", "v")
	mr.SetTTL("k", time.Minute)
	mr.Set("perm", "v")

	byKey := map[string]*Node{}
	for _, n := range mustLs(t, m, "").Nodes {
		byKey[n.Key] = n
	}
	if byKey["k"].TTL <= 0 {
		t.Fatalf("expected a positive TTL, got %v", byKey["k"].TTL)
	}
	if byKey["perm"].TTL != -1 {
		t.Fatalf("expected no TTL, got %v", byKey["perm"].TTL)
	}
}

func TestLsTruncatedListing(t *testing.T) {
	m, mr := newTestModel(t, func(o *Options) { o.MaxKeys = 3 })
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		mr.Set(k, "1")
	}
	l := mustLs(t, m, "")
	if !l.Truncated {
		t.Fatal("expected the listing to be reported as truncated")
	}
	if len(l.Nodes) > 3 {
		t.Fatalf("expected at most 3 nodes, got %d", len(l.Nodes))
	}
}

func TestGetReturnsFullValue(t *testing.T) {
	m, mr := newTestModel(t, func(o *Options) { o.PreviewBytes = 2 })
	mr.Set("k", "0123456789")

	n, err := m.Get("k")
	if err != nil {
		t.Fatal(err)
	}
	if n.Value != "0123456789" || n.Truncated {
		t.Fatalf("Get returned a truncated value: %+v", n)
	}
}

func TestGetDirectory(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("a/b", "1")

	n, err := m.Get("a")
	if err != nil {
		t.Fatal(err)
	}
	if !n.IsDir || n.Key != "a" {
		t.Fatalf("Get(a) = %+v", n)
	}
}

func TestGetNotFound(t *testing.T) {
	m, _ := newTestModel(t)
	if _, err := m.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGetNonStringDoesNotFail(t *testing.T) {
	m, mr := newTestModel(t)
	mr.HSet("h", "f", "v")
	n, err := m.Get("h")
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "hash" || n.Value != "" {
		t.Fatalf("Get(h) = %+v", n)
	}
}

func TestResolveTriesBothSlashVariants(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("user:1", "v")
	mr.Set("/legacy", "v")

	for _, in := range []string{"user:1", "/user:1"} {
		n, err := m.Resolve(in)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", in, err)
		}
		if n.Key != "user:1" {
			t.Fatalf("Resolve(%q).Key = %q", in, n.Key)
		}
	}
	n, err := m.Resolve("/legacy")
	if err != nil || n.Key != "/legacy" {
		t.Fatalf("Resolve(/legacy) = %+v, %v", n, err)
	}
}

func TestSetKeepsTTL(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("k", "old")
	mr.SetTTL("k", time.Minute)

	if err := m.Set("k", "new"); err != nil {
		t.Fatal(err)
	}
	if got := mr.TTL("k"); got <= 0 {
		t.Fatalf("editing a value dropped the TTL (ttl=%v)", got)
	}
	if v, _ := mr.Get("k"); v != "new" {
		t.Fatalf("value = %q", v)
	}
}

func TestSetRefusesNonStringKeys(t *testing.T) {
	m, mr := newTestModel(t)
	mr.HSet("h", "f", "v")
	if err := m.Set("h", "oops"); !errors.Is(err, ErrWrongType) {
		t.Fatalf("err = %v, want ErrWrongType", err)
	}
	if mr.Type("h") != "hash" {
		t.Fatal("the hash was replaced by a string")
	}
}

func TestSetRejectsRoot(t *testing.T) {
	m, _ := newTestModel(t)
	if err := m.Set("", "v"); err == nil {
		t.Fatal("expected an error for the root")
	}
}

func TestMkDirCreatesMarkerOnlyWhenEmpty(t *testing.T) {
	m, mr := newTestModel(t)
	if err := m.MkDir("new"); err != nil {
		t.Fatal(err)
	}
	if !mr.Exists("new/" + dirMarker) {
		t.Fatal("the marker key was not created")
	}

	mr.Set("used/child", "1")
	if err := m.MkDir("used"); err != nil {
		t.Fatal(err)
	}
	if mr.Exists("used/" + dirMarker) {
		t.Fatal("a marker must not be added to a non-empty folder")
	}
}

func TestDelDirRemovesSubtreeAndOwnKey(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("a", "own")
	mr.Set("a/b", "1")
	mr.Set("a/c/d", "2")
	mr.Set("ab", "keep")

	if err := m.DelDir("a"); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a", "a/b", "a/c/d"} {
		if mr.Exists(k) {
			t.Fatalf("%q survived DelDir", k)
		}
	}
	if !mr.Exists("ab") {
		t.Fatal("DelDir removed a sibling whose name shares the prefix")
	}
}

func TestDelDirRefusesRoot(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("a", "1")
	if err := m.DelDir(""); err == nil {
		t.Fatal("DelDir must refuse to wipe the keyspace")
	}
	if !mr.Exists("a") {
		t.Fatal("keys were deleted")
	}
}

func TestDelDirBatchesLargeDeletes(t *testing.T) {
	m, mr := newTestModel(t)
	for i := 0; i < delBatchSize*2+7; i++ {
		mr.Set("big/"+itoa(i), "v")
	}
	if err := m.DelDir("big"); err != nil {
		t.Fatal(err)
	}
	if keys := mr.Keys(); len(keys) != 0 {
		t.Fatalf("%d keys survived", len(keys))
	}
}

// Regression: renaming used to GET/SET every key, which silently replaced
// non-string values with an empty string and dropped every TTL.
func TestRenameDirPreservesTypesAndTTLs(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("old/s", "v")
	mr.SetTTL("old/s", time.Minute)
	mr.HSet("old/h", "f", "v")
	mr.Set("old/deep/x", "1")

	if err := m.RenameDir("old", "new"); err != nil {
		t.Fatal(err)
	}
	if mr.Exists("old/s") || mr.Exists("old/h") || mr.Exists("old/deep/x") {
		t.Fatal("source keys survived the rename")
	}
	if v, _ := mr.Get("new/s"); v != "v" {
		t.Fatalf("new/s = %q", v)
	}
	if mr.TTL("new/s") <= 0 {
		t.Fatal("the TTL was lost by the rename")
	}
	if mr.Type("new/h") != "hash" {
		t.Fatalf("new/h became a %s", mr.Type("new/h"))
	}
	if v, _ := mr.Get("new/deep/x"); v != "1" {
		t.Fatalf("new/deep/x = %q", v)
	}
}

func TestRenameDirRejects(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("a/x", "1")
	mr.Set("b/y", "1")

	if err := m.RenameDir("missing", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if err := m.RenameDir("a", "b"); err == nil {
		t.Fatal("expected an error when the target exists")
	}
	if err := m.RenameDir("a", "a/sub"); err == nil {
		t.Fatal("expected an error when moving a folder into itself")
	}
	if err := m.RenameDir("", "x"); err == nil {
		t.Fatal("expected an error for the root")
	}
	if err := m.RenameDir("a", "a"); err != nil {
		t.Fatalf("renaming to the same name must be a no-op, got %v", err)
	}
}

func TestScanDeduplicatesAndVerifiesPrefix(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("a/1", "v")
	mr.Set("ab/2", "v")

	keys, truncated, err := m.scan(newTestCtx(t), "a/", 0)
	if err != nil || truncated {
		t.Fatalf("scan: %v truncated=%v", err, truncated)
	}
	if len(keys) != 1 || keys[0] != "a/1" {
		t.Fatalf("scan = %v", keys)
	}
}

func TestModelUsesClientInterface(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	m := NewWithClient(client, Options{})
	mr.Set("k", "v")
	if l := mustLs(t, m, ""); len(l.Nodes) != 1 {
		t.Fatalf("listing = %v", names(l))
	}
}
