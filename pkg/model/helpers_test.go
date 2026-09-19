package model

import (
	"context"
	"strconv"
	"testing"
	"time"
)

var bg = context.Background()

func itoa(i int) string { return strconv.Itoa(i) }

func newTestCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}
