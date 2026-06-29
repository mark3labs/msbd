package core

// fs.go — native filesystem operations beyond read/write. Every call routes
// through Registry.resolve so reconnect + transparent resume work uniformly.

import (
	"context"
	"fmt"
	"time"
)

// FileEntry is one directory listing entry.
type FileEntry struct {
	Path string
	Kind string // file | directory | symlink | other
	Size int64
	Mode uint32
}

// FileStat is metadata for one path.
type FileStat struct {
	Path    string
	Size    int64
	Mode    uint32
	ModTime time.Time
	IsDir   bool
}

func (s *Service) ListDir(ctx context.Context, id, path, cwd string) ([]FileEntry, error) {
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return nil, err
	}
	entries, err := sb.FS().List(ctx, resolvePath(path, cwd))
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", path, err)
	}
	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, FileEntry{Path: e.Path, Kind: string(e.Kind), Size: e.Size, Mode: e.Mode})
	}
	return out, nil
}

func (s *Service) Stat(ctx context.Context, id, path, cwd string) (*FileStat, error) {
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return nil, err
	}
	st, err := sb.FS().Stat(ctx, resolvePath(path, cwd))
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	return &FileStat{Path: st.Path, Size: st.Size, Mode: st.Mode, ModTime: st.ModTime, IsDir: st.IsDir}, nil
}

func (s *Service) Exists(ctx context.Context, id, path, cwd string) (bool, error) {
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return false, err
	}
	ok, err := sb.FS().Exists(ctx, resolvePath(path, cwd))
	if err != nil {
		return false, fmt.Errorf("exists %s: %w", path, err)
	}
	return ok, nil
}

func (s *Service) Mkdir(ctx context.Context, id, path, cwd string) error {
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return err
	}
	if err := sb.FS().Mkdir(ctx, resolvePath(path, cwd)); err != nil {
		return fmt.Errorf("mkdir %s: %w", path, err)
	}
	return nil
}

func (s *Service) Remove(ctx context.Context, id, path, cwd string, recursive bool) error {
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return err
	}
	dest := resolvePath(path, cwd)
	if recursive {
		if err := sb.FS().RemoveDir(ctx, dest); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}
	if err := sb.FS().Remove(ctx, dest); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func (s *Service) Copy(ctx context.Context, id, src, dst, cwd string) error {
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return err
	}
	if err := sb.FS().Copy(ctx, resolvePath(src, cwd), resolvePath(dst, cwd)); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	return nil
}

func (s *Service) Rename(ctx context.Context, id, src, dst, cwd string) error {
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return err
	}
	if err := sb.FS().Rename(ctx, resolvePath(src, cwd), resolvePath(dst, cwd)); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", src, dst, err)
	}
	return nil
}

// CopyFromHost copies an allowlisted host path into the sandbox.
func (s *Service) CopyFromHost(ctx context.Context, id, hostPath, guestPath, cwd string) error {
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return err
	}
	if err := sb.FS().CopyFromHost(ctx, hostPath, resolvePath(guestPath, cwd)); err != nil {
		return fmt.Errorf("copy from host %s -> %s: %w", hostPath, guestPath, err)
	}
	return nil
}

// CopyToHost copies a sandbox path to an allowlisted host destination.
func (s *Service) CopyToHost(ctx context.Context, id, guestPath, hostPath, cwd string) error {
	sb, err := s.reg.resolve(ctx, id)
	if err != nil {
		return err
	}
	if err := sb.FS().CopyToHost(ctx, resolvePath(guestPath, cwd), hostPath); err != nil {
		return fmt.Errorf("copy to host %s -> %s: %w", guestPath, hostPath, err)
	}
	return nil
}
