//go:build !unix && !windows

package config

import (
	"context"
	"errors"
)

func Lock(context.Context, string) (func(), error) {
	return nil, errors.New("configuration editing requires Unix file locking")
}

func ReadLock(context.Context, string) (func(), error) { return func() {}, nil }
