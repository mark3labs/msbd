package store

// path.go — where the database lives.
//
// The default sits INSIDE ~/.microsandbox, the directory the SDK runtime
// already owns, because every existing deployment path already persists it:
// the Dockerfile declares VOLUME ["/root/.microsandbox"], docker-compose mounts
// it, and the NixOS module gives the service a state directory. Defaulting
// anywhere else would silently produce a store that evaporates on redeploy.

import (
	"os"
	"path/filepath"
	"strings"
)

// DBFileName is the database file inside the data directory.
const DBFileName = "msbd.db"

// DefaultDir returns the default data directory: $MSBD_DATA_DIR when set,
// otherwise ~/.microsandbox/msbd. If the home directory cannot be determined
// (an empty $HOME under some init systems), it falls back to a relative path so
// msbd still starts rather than failing at boot.
func DefaultDir() string {
	if v := strings.TrimSpace(os.Getenv("MSBD_DATA_DIR")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".microsandbox", "msbd")
	}
	return filepath.Join(home, ".microsandbox", "msbd")
}

// DBPath resolves the database path for a data directory. An empty dir means
// DefaultDir(); a dir that already names a *.db file is used verbatim, so
// --data-dir can point straight at a file when someone wants that.
func DBPath(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = DefaultDir()
	}
	if strings.HasSuffix(dir, ".db") {
		return dir
	}
	return filepath.Join(dir, DBFileName)
}
