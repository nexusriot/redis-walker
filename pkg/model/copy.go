package model

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// ErrExists is returned when a copy would overwrite an existing key.
var ErrExists = errors.New("destination already exists")

// CopyKey copies one key to another model, which may live in a different
// database or on a different server. Types and TTLs are preserved.
func (m *Model) CopyKey(ctx context.Context, srcKey string, dst *Model, dstKey string, replace bool) error {
	ctx, cancel := m.ctx(ctx)
	defer cancel()
	return m.copyKey(ctx, srcKey, dst, dstKey, replace)
}

func (m *Model) copyKey(ctx context.Context, srcKey string, dst *Model, dstKey string, replace bool) error {
	srcKey = strings.TrimSuffix(srcKey, "/")
	dstKey = strings.TrimSuffix(dstKey, "/")
	if srcKey == "" || dstKey == "" {
		return errors.New("cannot copy the root")
	}
	if dst == nil {
		return errors.New("no destination")
	}
	if m.SameServer(dst) && m.db == dst.db && srcKey == dstKey {
		return errors.New("source and destination are the same key")
	}

	if !replace {
		n, err := dst.rdb.Exists(ctx, dstKey).Result()
		if err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: %s", ErrExists, PathOf(dstKey))
		}
	}

	if m.SameServer(dst) {
		args := []interface{}{"copy", srcKey, dstKey}
		if dst.db != m.db {
			args = append(args, "db", dst.db)
		}
		if replace {
			args = append(args, "replace")
		}
		moved, err := m.rdb.Do(ctx, args...).Int64()
		if err == nil {
			if moved == 0 {
				return fmt.Errorf("%w: %s", ErrExists, PathOf(dstKey))
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	return m.dumpRestore(ctx, srcKey, dst, dstKey, replace)
}

func (m *Model) dumpRestore(ctx context.Context, srcKey string, dst *Model, dstKey string, replace bool) error {
	payload, err := m.rdb.Dump(ctx, srcKey).Result()
	if errors.Is(err, redis.Nil) {
		return fmt.Errorf("%w: %s", ErrNotFound, PathOf(srcKey))
	}
	if err == nil {
		ttl, terr := m.rdb.PTTL(ctx, srcKey).Result()
		if terr != nil || ttl < 0 {
			ttl = 0
		}
		restore := dst.rdb.Restore
		if replace {
			restore = dst.rdb.RestoreReplace
		}
		if err := restore(ctx, dstKey, ttl, payload).Err(); err == nil {
			return nil
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	typ, err := m.rdb.Type(ctx, srcKey).Result()
	if err != nil {
		return err
	}
	if typ == "none" {
		return fmt.Errorf("%w: %s", ErrNotFound, PathOf(srcKey))
	}
	if typ != TypeString {
		return fmt.Errorf("%w: cannot copy %s across servers", ErrWrongType, typ)
	}
	val, err := m.rdb.Get(ctx, srcKey).Result()
	if err != nil {
		return err
	}
	ttl, err := m.rdb.PTTL(ctx, srcKey).Result()
	if err != nil || ttl < 0 {
		ttl = 0
	}
	return dst.rdb.Set(ctx, dstKey, val, ttl).Err()
}

// CopyDir copies a whole subtree to another model.
func (m *Model) CopyDir(ctx context.Context, srcDirKey string, dst *Model, dstDirKey string, replace bool, onProgress Progress) error {
	ctx, cancel := m.ctx(ctx)
	defer cancel()

	srcPfx := PrefixOf(srcDirKey)
	dstPfx := PrefixOf(dstDirKey)
	if srcPfx == "" || dstPfx == "" {
		return errors.New("cannot copy the root")
	}
	if m.SameServer(dst) && m.db == dst.db && strings.HasPrefix(dstPfx, srcPfx) {
		return fmt.Errorf("cannot copy %s into itself", PathOf(srcDirKey))
	}

	keys, _, err := m.scan(ctx, srcPfx, 0)
	if err != nil {
		return err
	}
	keys = m.withOwnKey(ctx, srcPfx, keys)
	if len(keys) == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, PathOf(srcDirKey))
	}

	for i, src := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := strings.TrimSuffix(dstPfx, "/")
		if rest := strings.TrimPrefix(src, srcPfx); rest != src {
			target = dstPfx + rest
		}
		if err := m.copyKey(ctx, src, dst, target, replace); err != nil {
			return err
		}
		if onProgress != nil {
			onProgress(i + 1)
		}
	}
	return nil
}

// MoveKey copies a key to another model and removes the source.
func (m *Model) MoveKey(ctx context.Context, srcKey string, dst *Model, dstKey string, replace bool) error {
	if m.SameServer(dst) && m.db == dst.db {
		ctx, cancel := m.ctx(ctx)
		defer cancel()
		return m.renameKey(ctx, srcKey, dstKey, replace)
	}
	if err := m.CopyKey(ctx, srcKey, dst, dstKey, replace); err != nil {
		return err
	}
	return m.Del(ctx, srcKey)
}

func (m *Model) renameKey(ctx context.Context, srcKey, dstKey string, replace bool) error {
	srcKey = strings.TrimSuffix(srcKey, "/")
	dstKey = strings.TrimSuffix(dstKey, "/")
	if srcKey == "" || dstKey == "" {
		return errors.New("cannot move the root")
	}
	if srcKey == dstKey {
		return nil
	}
	if !replace {
		ok, err := m.rdb.RenameNX(ctx, srcKey, dstKey).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return fmt.Errorf("%w: %s", ErrNotFound, PathOf(srcKey))
			}
			return err
		}
		if !ok {
			return fmt.Errorf("%w: %s", ErrExists, PathOf(dstKey))
		}
		return nil
	}
	if err := m.rdb.Rename(ctx, srcKey, dstKey).Err(); err != nil {
		if errors.Is(err, redis.Nil) {
			return fmt.Errorf("%w: %s", ErrNotFound, PathOf(srcKey))
		}
		return err
	}
	return nil
}

// MoveDir copies a subtree to another model and removes the source.
func (m *Model) MoveDir(ctx context.Context, srcDirKey string, dst *Model, dstDirKey string, replace bool, onProgress Progress) error {
	if err := m.CopyDir(ctx, srcDirKey, dst, dstDirKey, replace, onProgress); err != nil {
		return err
	}
	return m.DelDir(ctx, srcDirKey)
}
