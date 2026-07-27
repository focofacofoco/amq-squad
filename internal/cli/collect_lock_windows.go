//go:build windows

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func lockCollectJournalContext(ctx context.Context, root string) (func(), error) {
	if err := os.MkdirAll(root, collectJournalDirectoryPerm); err != nil {
		return nil, fmt.Errorf("ensure collect journal lock dir: %w", err)
	}
	path := filepath.Join(root, ".lock")
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, collectJournalFilePerm)
		if err == nil {
			return func() {
				_ = f.Close()
				_ = os.Remove(path)
			}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("open collect journal lock: %w", err)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
