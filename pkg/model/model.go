package model

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

type Model struct {
	rdb     *redis.Client
	exclude []string
}

type Node struct {
	Name  string
	IsDir bool
	Value string
}

// NewModel creates a new Redis-backed model.
func NewModel(host, port string, db int, excludePrefixes []string) (*Model, error) {
	addr := fmt.Sprintf("%s:%s", host, port)
	rdb := redis.NewClient(&redis.Options{
		Addr: addr,
		DB:   db,
		// TODO: extend with Password & Username
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Ping to validate connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	// Normalize exclude prefixes
	normEx := make([]string, 0, len(excludePrefixes))
	for _, p := range excludePrefixes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		normEx = append(normEx, normPath(p))
	}

	return &Model{
		rdb:     rdb,
		exclude: normEx,
	}, nil
}

// Public API (same as etcd model, minus protocols)

func (m *Model) Ls(directory string) ([]*Node, error)  { return m.ls(directory) }
func (m *Model) Get(key string) (*Node, error)         { return m.get(key) }
func (m *Model) Set(key, value string) error           { return m.set(key, value) }
func (m *Model) MkDir(directory string) error          { return m.mkdir(directory) }
func (m *Model) Del(key string) error                  { return m.del(key) }
func (m *Model) DelDir(key string) error               { return m.deldir(key) }
func (m *Model) RenameDir(oldDir, newDir string) error { return m.renameDir(oldDir, newDir) }

const dirMarker = ".dir"

func normPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if p != "/" {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// withTrail returns prefix for listing. Root "/" => empty prefix (all keys).
func withTrail(p string) string {
	p = normPath(p)
	if p == "/" {
		return ""
	}
	return strings.TrimSuffix(p, "/") + "/"
}

func parentOf(p string) string {
	p = normPath(p)
	if p == "/" {
		return "/"
	}
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

func baseOf(p string) string {
	p = normPath(p)
	if p == "/" {
		return "/"
	}
	i := strings.LastIndex(p, "/")
	if i < 0 || i == len(p)-1 {
		return p
	}
	return p[i+1:]
}

func (m *Model) shouldExclude(key string) bool {
	if len(m.exclude) == 0 {
		return false
	}
	k := normPath(key)
	for _, p := range m.exclude {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

func (m *Model) scanKeysWithPrefix(ctx context.Context, prefix string) ([]string, error) {
	var (
		cursor uint64
		all    []string
		match  = prefix + "*"
	)
	for {
		keys, next, err := m.rdb.Scan(ctx, cursor, match, 1000).Result()
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if m.shouldExclude(k) {
				log.WithFields(log.Fields{
					"op":   "scan",
					"key":  k,
					"pfx":  prefix,
					"info": "excluded by prefix",
				}).Debug("redis scan skipped key")
				continue
			}
			all = append(all, k)
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return all, nil
}

func (m *Model) ls(directory string) ([]*Node, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	prefix := withTrail(directory)
	start := time.Now()
	var keys []string
	var err error

	if prefix == "" {
		// root listing: match everything
		keys, err = m.scanKeysWithPrefix(ctx, "")
	} else {
		keys, err = m.scanKeysWithPrefix(ctx, prefix)
	}
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"op":   "ls",
			"dir":  directory,
			"pfx":  prefix,
			"kind": "redis",
		}).Error("redis ls failed")
		return nil, err
	}

	type childInfo struct {
		isDir     bool
		hasFile   bool
		fileKey   string
		fileValue string
	}
	children := map[string]*childInfo{}

	for _, key := range keys {
		if key == "" {
			continue
		}
		rest := key
		if prefix != "" {
			rest = strings.TrimPrefix(key, prefix)
		}
		rest = strings.TrimLeft(rest, "/")
		if rest == "" {
			continue
		}
		parts := strings.SplitN(rest, "/", 2)
		child := parts[0]
		if child == "" || child == dirMarker {
			continue
		}
		ci := children[child]
		if ci == nil {
			ci = &childInfo{}
			children[child] = ci
		}
		if len(parts) == 2 {
			ci.isDir = true
		} else {
			ci.hasFile = true
			ci.fileKey = key
		}
	}

	// Fetch file values (only for string keys).
	for name, ci := range children {
		if !ci.hasFile || ci.fileKey == "" {
			continue
		}

		val, err := m.rdb.Get(ctx, ci.fileKey).Result()
		if err != nil && err != redis.Nil {
			// If this is a WRONGTYPE error, it means the key is not a string
			// (hash/list/set/zset/stream). We don't want to fail the whole
			// listing; just skip loading the value.
			if strings.Contains(err.Error(), "WRONGTYPE") {
				log.WithError(err).WithFields(log.Fields{
					"op":  "ls-file",
					"key": ci.fileKey,
				}).Debug("non-string Redis value; skipping value load")
				continue
			}
			// real error -> bubble up
			return nil, fmt.Errorf("get %s: %w", ci.fileKey, err)
		}

		ci.fileValue = val
		log.WithFields(log.Fields{
			"op":   "ls-file",
			"key":  ci.fileKey,
			"name": name,
		}).Debug("redis ls loaded value")
	}

	names := make([]string, 0, len(children))
	for k := range children {
		names = append(names, k)
	}
	sort.Strings(names)

	root := normPath(directory)
	if root == "/" {
		root = ""
	}
	var nodes []*Node
	for _, name := range names {
		ci := children[name]
		full := root + "/" + name
		if ci.isDir {
			nodes = append(nodes, &Node{
				Name:  normPath(full),
				IsDir: true,
			})
		}
		if ci.hasFile {
			nodes = append(nodes, &Node{
				Name:  normPath(full),
				IsDir: false,
				Value: ci.fileValue,
			})
		}
	}

	log.WithFields(log.Fields{
		"op":       "ls",
		"dir":      directory,
		"pfx":      prefix,
		"count":    len(nodes),
		"duration": time.Since(start),
	}).Debug("redis ls done")

	return nodes, nil
}

func (m *Model) set(key, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	k := normPath(key)
	if k == "/" {
		return fmt.Errorf("cannot set value on root")
	}
	start := time.Now()
	if err := m.rdb.Set(ctx, k, value, 0).Err(); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"op":  "set",
			"key": k,
		}).Error("redis set failed")
		return err
	}
	log.WithFields(log.Fields{
		"op":       "set",
		"key":      k,
		"size":     len(value),
		"duration": time.Since(start),
	}).Debug("redis set ok")
	return nil
}

func (m *Model) mkdir(directory string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir := normPath(directory)
	if dir == "/" {
		return nil
	}
	dir = strings.TrimSuffix(dir, "/")
	markerKey := dir + "/" + dirMarker
	pfx := withTrail(dir)

	respKeys, err := m.scanKeysWithPrefix(ctx, pfx)
	if err != nil {
		return err
	}
	if len(respKeys) > 0 {
		// something already exists under this prefix, that's enough
		return nil
	}
	if err := m.rdb.Set(ctx, markerKey, "", 0).Err(); err != nil {
		return err
	}
	return nil
}

func (m *Model) del(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	k := normPath(key)
	if k == "/" {
		return fmt.Errorf("cannot delete root")
	}
	if _, err := m.rdb.Del(ctx, k).Result(); err != nil {
		return err
	}
	return nil
}

func (m *Model) deldir(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pfx := withTrail(key)
	keys, err := m.scanKeysWithPrefix(ctx, pfx)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	if _, err := m.rdb.Del(ctx, keys...).Result(); err != nil {
		return err
	}
	return nil
}

func (m *Model) renameDir(oldDir, newDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	oldPfx := withTrail(oldDir)
	newPfx := withTrail(newDir)
	if oldPfx == newPfx {
		return nil
	}

	srcKeys, err := m.scanKeysWithPrefix(ctx, oldPfx)
	if err != nil {
		return err
	}
	if len(srcKeys) == 0 {
		return fmt.Errorf("source does not exist: %s", oldDir)
	}

	// Check that target prefix is free
	dstKeys, err := m.scanKeysWithPrefix(ctx, newPfx)
	if err != nil {
		return err
	}
	if len(dstKeys) > 0 {
		return fmt.Errorf("target already exists: %s", newDir)
	}

	// Copy all keys
	for _, oldKey := range srcKeys {
		newKey := strings.Replace(oldKey, oldPfx, newPfx, 1)
		val, err := m.rdb.Get(ctx, oldKey).Result()
		if err != nil && err != redis.Nil {
			return fmt.Errorf("copy %s -> %s get failed: %w", oldKey, newKey, err)
		}
		if err := m.rdb.Set(ctx, newKey, val, 0).Err(); err != nil {
			return fmt.Errorf("copy %s -> %s set failed: %w", oldKey, newKey, err)
		}
	}
	// delete old prefix
	if _, err := m.rdb.Del(ctx, srcKeys...).Result(); err != nil {
		return err
	}
	return nil
}

func (m *Model) get(key string) (*Node, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	k := normPath(key)
	if k == "/" {
		// treat as dir
		return &Node{
			Name:  "/",
			IsDir: true,
			Value: "",
		}, nil
	}

	val, err := m.rdb.Get(ctx, k).Result()
	if err == nil {
		return &Node{
			Name:  k,
			IsDir: false,
			Value: val,
		}, nil
	}
	if err != nil && err != redis.Nil {
		// Same WRONGTYPE handling: non-string value.
		if strings.Contains(err.Error(), "WRONGTYPE") {
			// Treat as a non-string leaf; we don't have a value preview.
			log.WithError(err).WithFields(log.Fields{
				"op":  "get",
				"key": k,
			}).Debug("non-string Redis value in get; returning placeholder node")
			return &Node{
				Name:  k,
				IsDir: false,
				Value: "",
			}, nil
		}
		return nil, err
	}

	// If no direct value, see if it behaves like a directory
	pfx := withTrail(k)
	keys, err := m.scanKeysWithPrefix(ctx, pfx)
	if err != nil {
		return nil, err
	}
	if len(keys) > 0 {
		return &Node{
			Name:  k,
			IsDir: true,
			Value: "",
		}, nil
	}

	return nil, fmt.Errorf("not found: %s", k)
}
