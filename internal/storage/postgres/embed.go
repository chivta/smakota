package postgres

import (
	"fmt"
	"io/fs"
)

// fs_sub strips the migrations/ prefix, which tern expects to be the FS root.
func fs_sub() (fs.FS, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("sub migrations fs: %w", err)
	}
	return sub, nil
}
