package model

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
)

const metaBatchSize = 500

// KeySize is a single key with the memory it occupies.
type KeySize struct {
	Key   string
	Type  string
	Bytes int64
}

// PrefixSize aggregates the keys below one child of the analysed directory.
type PrefixSize struct {
	Key   string
	IsDir bool
	Keys  int
	Bytes int64
}

// DirStats is the result of analysing a directory subtree.
type DirStats struct {
	Prefix      string
	Keys        int
	Folders     int
	Bytes       int64
	Types       map[string]int
	WithTTL     int
	SoonestTTL  time.Duration
	TopKeys     []KeySize
	TopPrefixes []PrefixSize
	Truncated   bool
	Estimated   bool
	TakenAt     time.Time
}

// Stats walks a subtree and reports how many keys it holds, how much memory
// they use and how that memory is distributed over the direct children.
func (m *Model) Stats(ctx context.Context, dirKey string, topN int, onProgress Progress) (*DirStats, error) {
	ctx, cancel := m.ctx(ctx)
	defer cancel()

	if topN <= 0 {
		topN = 10
	}
	prefix := PrefixOf(dirKey)

	keys, truncated, err := m.scanProgress(ctx, prefix, m.maxKeys, onProgress)
	if err != nil {
		return nil, err
	}
	keys = m.withOwnKey(ctx, prefix, keys)

	st := &DirStats{
		Prefix:     prefix,
		Types:      map[string]int{},
		SoonestTTL: -1,
		Truncated:  truncated,
		TakenAt:    time.Now(),
	}
	folders := map[string]struct{}{}
	byChild := map[string]*PrefixSize{}
	var childOrder []string

	for i := 0; i < len(keys); i += metaBatchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := i + metaBatchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[i:end]

		pipe := m.rdb.Pipeline()
		typeCmds := make([]*redis.StatusCmd, len(batch))
		ttlCmds := make([]*redis.DurationCmd, len(batch))
		memCmds := make([]*redis.IntCmd, len(batch))
		lenCmds := make([]*redis.IntCmd, len(batch))
		for j, k := range batch {
			typeCmds[j] = pipe.Type(ctx, k)
			ttlCmds[j] = pipe.TTL(ctx, k)
			memCmds[j] = pipe.MemoryUsage(ctx, k)
			lenCmds[j] = pipe.StrLen(ctx, k)
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}

		for j, k := range batch {
			typ, err := typeCmds[j].Result()
			if err != nil || typ == "none" {
				continue
			}
			bytes, err := memCmds[j].Result()
			if err != nil || bytes <= 0 {
				st.Estimated = true
				if n, err := lenCmds[j].Result(); err == nil {
					bytes = n + int64(len(k))
				} else {
					bytes = int64(len(k))
				}
			}

			st.Keys++
			st.Bytes += bytes
			st.Types[typ]++
			if ttl, err := ttlCmds[j].Result(); err == nil && ttl > 0 {
				st.WithTTL++
				if st.SoonestTTL < 0 || ttl < st.SoonestTTL {
					st.SoonestTTL = ttl
				}
			}
			st.TopKeys = append(st.TopKeys, KeySize{Key: k, Type: typ, Bytes: bytes})

			childKey, _, isDir, ok := splitChild(prefix, k)
			if !ok {
				childKey, isDir = k, false
			}
			if isDir {
				folders[childKey] = struct{}{}
			}
			ps := byChild[childKey]
			if ps == nil {
				ps = &PrefixSize{Key: childKey, IsDir: isDir}
				byChild[childKey] = ps
				childOrder = append(childOrder, childKey)
			}
			ps.IsDir = ps.IsDir || isDir
			ps.Keys++
			ps.Bytes += bytes
		}
	}

	st.Folders = len(folders)

	sort.Slice(st.TopKeys, func(i, j int) bool {
		if st.TopKeys[i].Bytes != st.TopKeys[j].Bytes {
			return st.TopKeys[i].Bytes > st.TopKeys[j].Bytes
		}
		return st.TopKeys[i].Key < st.TopKeys[j].Key
	})
	if len(st.TopKeys) > topN {
		st.TopKeys = st.TopKeys[:topN]
	}

	prefixes := make([]PrefixSize, 0, len(childOrder))
	for _, k := range childOrder {
		prefixes = append(prefixes, *byChild[k])
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if prefixes[i].Bytes != prefixes[j].Bytes {
			return prefixes[i].Bytes > prefixes[j].Bytes
		}
		return prefixes[i].Key < prefixes[j].Key
	})
	if len(prefixes) > topN {
		prefixes = prefixes[:topN]
	}
	st.TopPrefixes = prefixes

	return st, nil
}
