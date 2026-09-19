package model

import (
	"context"
	"testing"
	"time"
)

func TestStatsCountsKeysAndTypes(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("app/a", "12345")
	mr.Set("app/b/c", "67890")
	mr.HSet("app/h", "f", "v")
	mr.Set("other", "x")
	mr.SetTTL("app/a", time.Minute)

	st, err := m.Stats(bg, "app", 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Keys != 3 {
		t.Fatalf("Keys = %d, want 3", st.Keys)
	}
	if st.Folders != 1 {
		t.Fatalf("Folders = %d, want 1", st.Folders)
	}
	if st.Types["string"] != 2 || st.Types["hash"] != 1 {
		t.Fatalf("Types = %v", st.Types)
	}
	if st.WithTTL != 1 || st.SoonestTTL <= 0 {
		t.Fatalf("ttl stats = %d / %v", st.WithTTL, st.SoonestTTL)
	}
	if st.Bytes <= 0 {
		t.Fatalf("Bytes = %d", st.Bytes)
	}
}

func TestStatsIncludesTheDirectoryOwnKey(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("app", "own")
	mr.Set("app/a", "1")

	st, err := m.Stats(bg, "app", 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Keys != 2 {
		t.Fatalf("Keys = %d, want the child and the folder key itself", st.Keys)
	}
}

func TestStatsRanksPrefixesAndKeysBySize(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("root/small/a", "x")
	mr.Set("root/big/a", "0123456789012345678901234567890123456789")
	mr.Set("root/big/b", "0123456789012345678901234567890123456789")

	st, err := m.Stats(bg, "root", 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.TopPrefixes) != 2 {
		t.Fatalf("TopPrefixes = %+v", st.TopPrefixes)
	}
	if st.TopPrefixes[0].Key != "root/big" {
		t.Fatalf("the biggest prefix is %q", st.TopPrefixes[0].Key)
	}
	if st.TopPrefixes[0].Keys != 2 || !st.TopPrefixes[0].IsDir {
		t.Fatalf("prefix entry = %+v", st.TopPrefixes[0])
	}
	if len(st.TopKeys) == 0 || st.TopKeys[0].Bytes < st.TopKeys[len(st.TopKeys)-1].Bytes {
		t.Fatalf("TopKeys not sorted: %+v", st.TopKeys)
	}
}

func TestStatsTopNLimits(t *testing.T) {
	m, mr := newTestModel(t)
	for i := 0; i < 20; i++ {
		mr.Set("d/k"+itoa(i), "v")
	}
	st, err := m.Stats(bg, "d", 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.TopKeys) != 3 {
		t.Fatalf("TopKeys = %d, want 3", len(st.TopKeys))
	}
}

func TestStatsReportsProgressAndHonoursCancel(t *testing.T) {
	m, mr := newTestModel(t)
	for i := 0; i < 50; i++ {
		mr.Set("d/k"+itoa(i), "v")
	}
	seen := 0
	if _, err := m.Stats(bg, "d", 5, func(done int) { seen = done }); err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no progress was reported")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Stats(ctx, "d", 5, nil); err == nil {
		t.Fatal("a cancelled context must abort the analysis")
	}
}

func TestLsHonoursCancel(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("a", "1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Ls(ctx, ""); err == nil {
		t.Fatal("a cancelled context must abort the listing")
	}
}

func TestLsReportsProgress(t *testing.T) {
	m, mr := newTestModel(t)
	for i := 0; i < 30; i++ {
		mr.Set("k"+itoa(i), "v")
	}
	got := 0
	if _, err := m.LsWithProgress(bg, "", func(done int) { got = done }); err != nil {
		t.Fatal(err)
	}
	if got == 0 {
		t.Fatal("no progress was reported")
	}
}
