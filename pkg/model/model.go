package model

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

const (
	// defaultScanCount is the COUNT hint passed to SCAN.
	defaultScanCount = 1000
	// defaultMaxKeys caps a single listing so that a huge keyspace cannot
	// exhaust memory; the listing is reported as truncated instead.
	defaultMaxKeys = 100000
	// defaultPreviewBytes is how much of a string value is loaded for the
	// list view. The full value is only read when a key is opened.
	defaultPreviewBytes = 8192
	// delBatchSize bounds the number of keys sent to a single DEL so that a
	// recursive delete cannot block the server for seconds.
	delBatchSize = 500
)

// TypeString is the Redis type name of plain string values, the only type
// redis-walker can display and edit.
const TypeString = "string"

// ErrNotFound is returned when a key (or directory prefix) does not exist.
var ErrNotFound = errors.New("not found")

// ErrWrongType is returned when an operation would replace a non-string value.
var ErrWrongType = errors.New("key holds a non-string value")

// Node is a single entry of a directory listing.
type Node struct {
	// Name is the virtual path shown in the UI.
	Name string
	// Key is the real Redis key (for a directory: the key prefix without the
	// trailing slash). All mutations must use this, never Name.
	Key string
	// IsDir reports whether this entry has children.
	IsDir bool
	// Type is the Redis type of a leaf ("string", "hash", ...); empty for
	// directories that have no key of their own.
	Type string
	// Value holds the string value, possibly truncated to the preview size.
	Value string
	// Size is the full length of the value in bytes.
	Size int64
	// Truncated reports whether Value is shorter than Size.
	Truncated bool
	// TTL is the remaining time to live, or -1 when the key never expires.
	TTL time.Duration
}

// Listing is the result of Ls.
type Listing struct {
	Nodes []*Node
	// Truncated is set when the scan hit the key limit and the listing is
	// therefore incomplete.
	Truncated bool
}

// Options configures a Model.
type Options struct {
	Host            string
	Port            string
	DB              int
	Username        string
	Password        string
	ExcludePrefixes []string

	// DialTimeout bounds the initial connection check.
	DialTimeout time.Duration
	// OpTimeout bounds a single user-visible operation.
	OpTimeout time.Duration
	// MaxKeys caps the number of keys returned by one scan.
	MaxKeys int
	// PreviewBytes caps how much of a value is loaded for a listing.
	PreviewBytes int
}

func (o *Options) withDefaults() {
	if o.Host == "" {
		o.Host = "127.0.0.1"
	}
	if o.Port == "" {
		o.Port = "6379"
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = 3 * time.Second
	}
	if o.OpTimeout <= 0 {
		o.OpTimeout = 10 * time.Second
	}
	if o.MaxKeys <= 0 {
		o.MaxKeys = defaultMaxKeys
	}
	if o.PreviewBytes <= 0 {
		o.PreviewBytes = defaultPreviewBytes
	}
}

// Model is the Redis-backed data source of the browser.
type Model struct {
	rdb          redis.UniversalClient
	exclude      []string
	opTimeout    time.Duration
	maxKeys      int
	previewBytes int
}

// New connects to Redis and returns a Model. The connection is validated with
// a PING so that authentication problems surface immediately.
func New(opts Options) (*Model, error) {
	opts.withDefaults()

	rdb := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%s", opts.Host, opts.Port),
		DB:           opts.DB,
		Username:     opts.Username, // optional ACL user
		Password:     opts.Password, // optional password
		DialTimeout:  opts.DialTimeout,
		ReadTimeout:  opts.OpTimeout,
		WriteTimeout: opts.OpTimeout,
	})

	ctx, cancel := context.WithTimeout(context.Background(), opts.DialTimeout)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	return NewWithClient(rdb, opts), nil
}

// NewWithClient wraps an already connected client. It is mainly useful for
// tests and for embedding redis-walker in another program.
func NewWithClient(rdb redis.UniversalClient, opts Options) *Model {
	opts.withDefaults()

	normEx := make([]string, 0, len(opts.ExcludePrefixes))
	for _, p := range opts.ExcludePrefixes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Exclude rules are written as display paths ("/pcp:") but must also
		// match keys stored without a leading slash.
		normEx = append(normEx, PathOf(p))
	}

	return &Model{
		rdb:          rdb,
		exclude:      normEx,
		opTimeout:    opts.OpTimeout,
		maxKeys:      opts.MaxKeys,
		previewBytes: opts.PreviewBytes,
	}
}

// Close releases the underlying connection pool.
func (m *Model) Close() error { return m.rdb.Close() }

func (m *Model) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), m.opTimeout)
}

func (m *Model) shouldExclude(key string) bool {
	if len(m.exclude) == 0 {
		return false
	}
	p := PathOf(key)
	for _, ex := range m.exclude {
		if strings.HasPrefix(p, ex) {
			return true
		}
	}
	return false
}

// scan returns every key under prefix. The MATCH pattern is escaped, and every
// returned key is verified against the prefix, so glob meta characters inside a
// key name can never widen the result set.
func (m *Model) scan(ctx context.Context, prefix string, limit int) (keys []string, truncated bool, err error) {
	var cursor uint64
	match := globEscape(prefix) + "*"
	seen := make(map[string]struct{})
	for {
		batch, next, err := m.rdb.Scan(ctx, cursor, match, defaultScanCount).Result()
		if err != nil {
			return nil, false, err
		}
		for _, k := range batch {
			if !strings.HasPrefix(k, prefix) {
				// Defence in depth: a server-side glob can never be trusted.
				continue
			}
			if m.shouldExclude(k) {
				continue
			}
			if _, dup := seen[k]; dup {
				// SCAN may return a key more than once.
				continue
			}
			seen[k] = struct{}{}
			keys = append(keys, k)
			if limit > 0 && len(keys) >= limit {
				return keys, true, nil
			}
		}
		if next == 0 {
			return keys, false, nil
		}
		cursor = next
	}
}

// Ls lists the direct children of a directory prefix ("" is the root).
func (m *Model) Ls(prefix string) (*Listing, error) {
	ctx, cancel := m.ctx()
	defer cancel()

	prefix = PrefixOf(prefix)
	start := time.Now()

	keys, truncated, err := m.scan(ctx, prefix, m.maxKeys)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"op": "ls", "pfx": prefix}).Error("redis ls failed")
		return nil, err
	}

	type childInfo struct {
		key     string
		segment string
		isDir   bool
		isLeaf  bool
	}
	children := map[string]*childInfo{}
	for _, key := range keys {
		childKey, segment, isDir, ok := splitChild(prefix, key)
		if !ok {
			continue
		}
		if segment == dirMarker && !isDir {
			// The placeholder written by MkDir is an implementation detail.
			continue
		}
		ci := children[childKey]
		if ci == nil {
			ci = &childInfo{key: childKey, segment: segment}
			children[childKey] = ci
		}
		if isDir {
			ci.isDir = true
		} else {
			ci.isLeaf = true
		}
	}

	leaves := make([]string, 0, len(children))
	for _, ci := range children {
		if ci.isLeaf {
			leaves = append(leaves, ci.key)
		}
	}
	sort.Strings(leaves)
	meta, err := m.loadMeta(ctx, leaves)
	if err != nil {
		return nil, err
	}

	ordered := make([]string, 0, len(children))
	for k := range children {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)

	listing := &Listing{Truncated: truncated}
	for _, ck := range ordered {
		ci := children[ck]
		if ci.isDir {
			listing.Nodes = append(listing.Nodes, &Node{
				Name:  PathOf(ck),
				Key:   ck,
				IsDir: true,
				TTL:   -1,
			})
		}
		if ci.isLeaf {
			n := &Node{Name: PathOf(ck), Key: ck, TTL: -1}
			if md, ok := meta[ck]; ok {
				n.Type, n.Value, n.Size, n.Truncated, n.TTL = md.typ, md.value, md.size, md.truncated, md.ttl
			}
			listing.Nodes = append(listing.Nodes, n)
		}
	}

	log.WithFields(log.Fields{
		"op":        "ls",
		"pfx":       prefix,
		"keys":      len(keys),
		"count":     len(listing.Nodes),
		"truncated": truncated,
		"duration":  time.Since(start),
	}).Debug("redis ls done")

	return listing, nil
}

type keyMeta struct {
	typ       string
	value     string
	size      int64
	truncated bool
	ttl       time.Duration
}

// loadMeta reads type, size, preview and TTL of the given keys using two
// pipelines instead of one round trip per key.
func (m *Model) loadMeta(ctx context.Context, keys []string) (map[string]keyMeta, error) {
	out := make(map[string]keyMeta, len(keys))
	if len(keys) == 0 {
		return out, nil
	}

	pipe := m.rdb.Pipeline()
	typeCmds := make([]*redis.StatusCmd, len(keys))
	ttlCmds := make([]*redis.DurationCmd, len(keys))
	for i, k := range keys {
		typeCmds[i] = pipe.Type(ctx, k)
		ttlCmds[i] = pipe.TTL(ctx, k)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read key metadata: %w", err)
	}

	strKeys := make([]string, 0, len(keys))
	for i, k := range keys {
		typ, err := typeCmds[i].Result()
		if err != nil {
			continue
		}
		if typ == "none" {
			// Deleted between SCAN and TYPE.
			continue
		}
		ttl, err := ttlCmds[i].Result()
		if err != nil {
			ttl = -1
		}
		out[k] = keyMeta{typ: typ, ttl: ttl}
		if typ == TypeString {
			strKeys = append(strKeys, k)
		}
	}
	if len(strKeys) == 0 {
		return out, nil
	}

	pipe = m.rdb.Pipeline()
	lenCmds := make([]*redis.IntCmd, len(strKeys))
	valCmds := make([]*redis.StringCmd, len(strKeys))
	for i, k := range strKeys {
		lenCmds[i] = pipe.StrLen(ctx, k)
		valCmds[i] = pipe.GetRange(ctx, k, 0, int64(m.previewBytes-1))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read key values: %w", err)
	}
	for i, k := range strKeys {
		md := out[k]
		if size, err := lenCmds[i].Result(); err == nil {
			md.size = size
		}
		if val, err := valCmds[i].Result(); err == nil {
			md.value = val
		}
		md.truncated = md.size > int64(len(md.value))
		out[k] = md
	}
	return out, nil
}

// Get returns a single node, reading the full (untruncated) value for strings.
// A key that does not exist but has children is reported as a directory.
func (m *Model) Get(key string) (*Node, error) {
	ctx, cancel := m.ctx()
	defer cancel()
	return m.get(ctx, key)
}

func (m *Model) get(ctx context.Context, key string) (*Node, error) {
	if key == "" || key == "/" {
		return &Node{Name: "/", Key: "", IsDir: true, TTL: -1}, nil
	}
	key = strings.TrimSuffix(key, "/")

	typ, err := m.rdb.Type(ctx, key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	if err == nil && typ != "none" {
		n := &Node{Name: PathOf(key), Key: key, Type: typ, TTL: -1}
		if ttl, err := m.rdb.TTL(ctx, key).Result(); err == nil {
			n.TTL = ttl
		}
		if typ == TypeString {
			val, err := m.rdb.Get(ctx, key).Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return nil, err
			}
			n.Value = val
			n.Size = int64(len(val))
		}
		return n, nil
	}

	// No value of its own: it may still be a directory.
	kids, _, err := m.scan(ctx, PrefixOf(key), 1)
	if err != nil {
		return nil, err
	}
	if len(kids) > 0 {
		return &Node{Name: PathOf(key), Key: key, IsDir: true, TTL: -1}, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, PathOf(key))
}

// Resolve looks a user-typed path up in the keyspace. Because the leading "/"
// of a path is virtual, both "a/b" and "/a/b" are tried.
func (m *Model) Resolve(path string) (*Node, error) {
	ctx, cancel := m.ctx()
	defer cancel()

	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return &Node{Name: "/", Key: "", IsDir: true, TTL: -1}, nil
	}
	candidates := []string{path}
	if trimmed := strings.TrimPrefix(path, "/"); trimmed != path && trimmed != "" {
		candidates = append(candidates, trimmed)
	} else if !strings.HasPrefix(path, "/") {
		candidates = append(candidates, "/"+path)
	}

	var firstErr error
	for _, c := range candidates {
		n, err := m.get(ctx, c)
		if err == nil {
			return n, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

// Set writes a string value, keeping any TTL the key already has. It refuses to
// overwrite a key that holds a non-string value.
func (m *Model) Set(key, value string) error {
	ctx, cancel := m.ctx()
	defer cancel()

	key = strings.TrimSuffix(key, "/")
	if key == "" {
		return errors.New("cannot set a value on the root")
	}
	typ, err := m.rdb.Type(ctx, key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	if err == nil && typ != "none" && typ != TypeString {
		return fmt.Errorf("%w: %s is a %s", ErrWrongType, PathOf(key), typ)
	}

	start := time.Now()
	// KeepTTL: editing a value must not silently make a volatile key permanent.
	if err := m.rdb.Set(ctx, key, value, redis.KeepTTL).Err(); err != nil {
		log.WithError(err).WithFields(log.Fields{"op": "set", "key": key}).Error("redis set failed")
		return err
	}
	log.WithFields(log.Fields{
		"op": "set", "key": key, "size": len(value), "duration": time.Since(start),
	}).Debug("redis set ok")
	return nil
}

// MkDir makes an empty directory visible by writing a placeholder leaf.
func (m *Model) MkDir(dirKey string) error {
	ctx, cancel := m.ctx()
	defer cancel()

	dirKey = strings.TrimSuffix(dirKey, "/")
	if dirKey == "" {
		return errors.New("cannot create the root directory")
	}
	existing, _, err := m.scan(ctx, PrefixOf(dirKey), 1)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		// Something already lives under this prefix: the directory exists.
		return nil
	}
	return m.rdb.Set(ctx, dirKey+"/"+dirMarker, "", 0).Err()
}

// Del removes a single key of any type.
func (m *Model) Del(key string) error {
	ctx, cancel := m.ctx()
	defer cancel()

	key = strings.TrimSuffix(key, "/")
	if key == "" {
		return errors.New("cannot delete the root")
	}
	return m.rdb.Del(ctx, key).Err()
}

// DelDir recursively removes every key under a directory prefix.
func (m *Model) DelDir(dirKey string) error {
	ctx, cancel := m.ctx()
	defer cancel()

	prefix := PrefixOf(dirKey)
	if prefix == "" {
		return errors.New("refusing to delete the whole keyspace")
	}
	keys, _, err := m.scan(ctx, prefix, 0)
	if err != nil {
		return err
	}
	// A directory may also exist as a key of its own ("a" plus "a/b").
	if n, err := m.rdb.Exists(ctx, strings.TrimSuffix(prefix, "/")).Result(); err == nil && n > 0 {
		keys = append(keys, strings.TrimSuffix(prefix, "/"))
	}
	return m.delBatched(ctx, keys)
}

func (m *Model) delBatched(ctx context.Context, keys []string) error {
	for i := 0; i < len(keys); i += delBatchSize {
		end := i + delBatchSize
		if end > len(keys) {
			end = len(keys)
		}
		if err := m.rdb.Del(ctx, keys[i:end]...).Err(); err != nil {
			return err
		}
	}
	return nil
}

// RenameDir moves every key under oldDirKey to newDirKey. RENAME is used so
// that the type, the TTL and the exact value of each key survive the move.
func (m *Model) RenameDir(oldDirKey, newDirKey string) error {
	ctx, cancel := m.ctx()
	defer cancel()

	oldPfx := PrefixOf(oldDirKey)
	newPfx := PrefixOf(newDirKey)
	if oldPfx == "" || newPfx == "" {
		return errors.New("cannot rename the root")
	}
	if oldPfx == newPfx {
		return nil
	}
	if strings.HasPrefix(newPfx, oldPfx) {
		return fmt.Errorf("cannot move %s into itself", PathOf(oldDirKey))
	}

	srcKeys, _, err := m.scan(ctx, oldPfx, 0)
	if err != nil {
		return err
	}
	if len(srcKeys) == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, PathOf(oldDirKey))
	}
	dstKeys, _, err := m.scan(ctx, newPfx, 1)
	if err != nil {
		return err
	}
	if len(dstKeys) > 0 {
		return fmt.Errorf("target already exists: %s", PathOf(newDirKey))
	}

	for _, oldKey := range srcKeys {
		newKey := newPfx + strings.TrimPrefix(oldKey, oldPfx)
		if err := m.rdb.Rename(ctx, oldKey, newKey).Err(); err != nil {
			if errors.Is(err, redis.Nil) {
				// Vanished mid-rename; nothing to move.
				continue
			}
			return fmt.Errorf("rename %s -> %s: %w", PathOf(oldKey), PathOf(newKey), err)
		}
	}
	return nil
}
