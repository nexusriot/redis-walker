package model

import (
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestCopyKeySameServer(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("src", "value")
	mr.SetTTL("src", time.Minute)

	if err := m.CopyKey(bg, "src", m, "dst", false); err != nil {
		t.Fatal(err)
	}
	if v, _ := mr.Get("dst"); v != "value" {
		t.Fatalf("dst = %q", v)
	}
	if !mr.Exists("src") {
		t.Fatal("the source was removed by a copy")
	}
	if mr.TTL("dst") <= 0 {
		t.Fatal("the TTL was not copied")
	}
}

func TestCopyKeyPreservesNonStringTypes(t *testing.T) {
	m, mr := newTestModel(t)
	mr.HSet("h", "f", "v")

	if err := m.CopyKey(bg, "h", m, "h2", false); err != nil {
		t.Fatal(err)
	}
	if mr.Type("h2") != "hash" {
		t.Fatalf("h2 is a %s", mr.Type("h2"))
	}
	if v := mr.HGet("h2", "f"); v != "v" {
		t.Fatalf("h2.f = %q", v)
	}
}

func TestCopyKeyRefusesToOverwrite(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("src", "a")
	mr.Set("dst", "b")

	if err := m.CopyKey(bg, "src", m, "dst", false); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if v, _ := mr.Get("dst"); v != "b" {
		t.Fatalf("dst was overwritten: %q", v)
	}
	if err := m.CopyKey(bg, "src", m, "dst", true); err != nil {
		t.Fatal(err)
	}
	if v, _ := mr.Get("dst"); v != "a" {
		t.Fatalf("dst = %q after a replacing copy", v)
	}
}

func TestCopyKeyAcrossDatabases(t *testing.T) {
	mr := miniredis.RunT(t)
	src, err := New(Options{Host: mr.Host(), Port: mr.Port(), DB: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })
	dst, err := New(Options{Host: mr.Host(), Port: mr.Port(), DB: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dst.Close() })

	mr.Select(0)
	mr.Set("k", "v")
	if err := src.CopyKey(bg, "k", dst, "k", false); err != nil {
		t.Fatal(err)
	}
	mr.Select(1)
	if v, _ := mr.Get("k"); v != "v" {
		t.Fatalf("db1 k = %q", v)
	}
}

func TestCopyKeyAcrossServers(t *testing.T) {
	srcModel, srcSrv := newTestModel(t)
	dstSrv := miniredis.RunT(t)
	dstModel, err := New(Options{Host: dstSrv.Host(), Port: dstSrv.Port()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dstModel.Close() })

	srcSrv.Set("k", "v")
	if srcModel.SameServer(dstModel) {
		t.Fatal("two separate servers must not be reported as the same")
	}
	if err := srcModel.CopyKey(bg, "k", dstModel, "k", false); err != nil {
		t.Fatal(err)
	}
	if v, _ := dstSrv.Get("k"); v != "v" {
		t.Fatalf("copied value = %q", v)
	}
}

func TestCopyDirSubtree(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("src", "own")
	mr.Set("src/a", "1")
	mr.Set("src/deep/b", "2")

	progress := 0
	if err := m.CopyDir(bg, "src", m, "dst", false, func(done int) { progress = done }); err != nil {
		t.Fatal(err)
	}
	if v, _ := mr.Get("dst/a"); v != "1" {
		t.Fatalf("dst/a = %q", v)
	}
	if v, _ := mr.Get("dst/deep/b"); v != "2" {
		t.Fatalf("dst/deep/b = %q", v)
	}
	if v, _ := mr.Get("dst"); v != "own" {
		t.Fatalf("the folder's own key was not copied: %q", v)
	}
	if !mr.Exists("src/a") {
		t.Fatal("the source subtree was removed")
	}
	if progress != 3 {
		t.Fatalf("progress = %d, want 3", progress)
	}
}

func TestCopyDirRejects(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("a/x", "1")

	if err := m.CopyDir(bg, "a", m, "a/sub", false, nil); err == nil {
		t.Fatal("copying a folder into itself must fail")
	}
	if err := m.CopyDir(bg, "missing", m, "b", false, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if err := m.CopyDir(bg, "", m, "b", false, nil); err == nil {
		t.Fatal("copying the root must fail")
	}
}

func TestMoveKeyOnTheSameServerUsesRename(t *testing.T) {
	m, mr := newTestModel(t)
	mr.HSet("h", "f", "v")

	if err := m.MoveKey(bg, "h", m, "moved", false); err != nil {
		t.Fatal(err)
	}
	if mr.Exists("h") {
		t.Fatal("the source survived the move")
	}
	if mr.Type("moved") != "hash" {
		t.Fatalf("moved is a %s", mr.Type("moved"))
	}
}

func TestMoveKeyRefusesToOverwrite(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("a", "1")
	mr.Set("b", "2")

	if err := m.MoveKey(bg, "a", m, "b", false); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if !mr.Exists("a") {
		t.Fatal("the source was removed although the move failed")
	}
	if err := m.MoveKey(bg, "a", m, "b", true); err != nil {
		t.Fatal(err)
	}
	if v, _ := mr.Get("b"); v != "1" || mr.Exists("a") {
		t.Fatalf("b = %q, a exists = %v", v, mr.Exists("a"))
	}
}

func TestMoveKeyAcrossServers(t *testing.T) {
	srcModel, srcSrv := newTestModel(t)
	dstSrv := miniredis.RunT(t)
	dstModel, err := New(Options{Host: dstSrv.Host(), Port: dstSrv.Port()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dstModel.Close() })

	srcSrv.Set("k", "v")
	if err := srcModel.MoveKey(bg, "k", dstModel, "k", false); err != nil {
		t.Fatal(err)
	}
	if srcSrv.Exists("k") {
		t.Fatal("the source survived a cross-server move")
	}
	if v, _ := dstSrv.Get("k"); v != "v" {
		t.Fatalf("moved value = %q", v)
	}
}

func TestMoveDirRemovesTheSource(t *testing.T) {
	m, mr := newTestModel(t)
	mr.Set("src/a", "1")
	mr.Set("src/b", "2")

	if err := m.MoveDir(bg, "src", m, "dst", false, nil); err != nil {
		t.Fatal(err)
	}
	if mr.Exists("src/a") || mr.Exists("src/b") {
		t.Fatal("the source subtree survived the move")
	}
	if v, _ := mr.Get("dst/b"); v != "2" {
		t.Fatalf("dst/b = %q", v)
	}
}

func TestEndpointHelpers(t *testing.T) {
	m, mr := newTestModel(t)
	if m.DB() != 0 {
		t.Fatalf("DB() = %d", m.DB())
	}
	if m.Endpoint() != mr.Addr()+"/0" {
		t.Fatalf("Endpoint() = %q", m.Endpoint())
	}
	if !m.SameServer(m) {
		t.Fatal("a model must be on the same server as itself")
	}
	if m.SameServer(nil) {
		t.Fatal("nil is not the same server")
	}
}
