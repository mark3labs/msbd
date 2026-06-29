package core

// volume.go — named persistent volumes and their file IO. Volumes are
// independent of sandboxes; mount them at create time via CreateParams.Mounts.

import (
	"context"
	"fmt"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// VolumeParams is the provider-neutral volume create input.
type VolumeParams struct {
	Name     string
	Kind     string // "dir" (default) | "disk"
	SizeMiB  int    // disk-backed size
	QuotaMiB int
	Labels   map[string]string
}

// Volume is the provider-neutral volume shape.
type Volume struct {
	Name          string
	Path          string
	Kind          string
	QuotaMiB      *uint32
	UsedBytes     uint64
	CapacityBytes *uint64
	DiskFormat    *string
	DiskFstype    *string
	Labels        map[string]string
	CreatedAt     time.Time
}

func volumeFromHandle(h *msb.VolumeHandle) *Volume {
	return &Volume{
		Name:          h.Name(),
		Path:          h.Path(),
		Kind:          string(h.Kind()),
		QuotaMiB:      h.QuotaMiB(),
		UsedBytes:     h.UsedBytes(),
		CapacityBytes: h.CapacityBytes(),
		DiskFormat:    h.DiskFormat(),
		DiskFstype:    h.DiskFstype(),
		Labels:        h.Labels(),
		CreatedAt:     h.CreatedAt(),
	}
}

func (s *Service) CreateVolume(ctx context.Context, p VolumeParams) (*Volume, error) {
	var opts []msb.VolumeOption
	switch p.Kind {
	case "disk":
		opts = append(opts, msb.WithVolumeKind(msb.VolumeKindDisk))
	case "dir", "":
		opts = append(opts, msb.WithVolumeKind(msb.VolumeKindDir))
	default:
		return nil, fmt.Errorf("invalid volume kind %q", p.Kind)
	}
	if p.SizeMiB > 0 {
		opts = append(opts, msb.WithVolumeSize(uint32(p.SizeMiB)))
	}
	if p.QuotaMiB > 0 {
		opts = append(opts, msb.WithVolumeQuota(uint32(p.QuotaMiB)))
	}
	if len(p.Labels) > 0 {
		opts = append(opts, msb.WithVolumeLabels(p.Labels))
	}
	if _, err := msb.CreateVolume(ctx, p.Name, opts...); err != nil {
		return nil, fmt.Errorf("create volume %s: %w", p.Name, err)
	}
	return s.GetVolume(ctx, p.Name)
}

func (s *Service) ListVolumes(ctx context.Context) ([]Volume, error) {
	handles, err := msb.ListVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	out := make([]Volume, 0, len(handles))
	for _, h := range handles {
		out = append(out, *volumeFromHandle(h))
	}
	return out, nil
}

func (s *Service) GetVolume(ctx context.Context, name string) (*Volume, error) {
	h, err := msb.GetVolume(ctx, name)
	if err != nil {
		return nil, ErrNotFound
	}
	return volumeFromHandle(h), nil
}

func (s *Service) RemoveVolume(ctx context.Context, name string) error {
	if _, err := msb.GetVolume(ctx, name); err != nil {
		return ErrNotFound
	}
	if err := msb.RemoveVolume(ctx, name); err != nil {
		return fmt.Errorf("remove volume %s: %w", name, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Volume file IO
// ---------------------------------------------------------------------------

func (s *Service) VolumeReadFile(ctx context.Context, name, relPath string) ([]byte, error) {
	h, err := msb.GetVolume(ctx, name)
	if err != nil {
		return nil, ErrNotFound
	}
	b, err := h.FS().Read(relPath)
	if err != nil {
		return nil, fmt.Errorf("volume read %s: %w", relPath, err)
	}
	return b, nil
}

func (s *Service) VolumeWriteFile(ctx context.Context, name, relPath string, content []byte) error {
	h, err := msb.GetVolume(ctx, name)
	if err != nil {
		return ErrNotFound
	}
	if err := h.FS().Write(relPath, content); err != nil {
		return fmt.Errorf("volume write %s: %w", relPath, err)
	}
	return nil
}

func (s *Service) VolumeMkdir(ctx context.Context, name, relPath string) error {
	h, err := msb.GetVolume(ctx, name)
	if err != nil {
		return ErrNotFound
	}
	if err := h.FS().Mkdir(relPath); err != nil {
		return fmt.Errorf("volume mkdir %s: %w", relPath, err)
	}
	return nil
}

func (s *Service) VolumeRemove(ctx context.Context, name, relPath string, recursive bool) error {
	h, err := msb.GetVolume(ctx, name)
	if err != nil {
		return ErrNotFound
	}
	if recursive {
		if err := h.FS().RemoveAll(relPath); err != nil {
			return fmt.Errorf("volume remove %s: %w", relPath, err)
		}
		return nil
	}
	if err := h.FS().Remove(relPath); err != nil {
		return fmt.Errorf("volume remove %s: %w", relPath, err)
	}
	return nil
}

func (s *Service) VolumeExists(ctx context.Context, name, relPath string) (bool, error) {
	h, err := msb.GetVolume(ctx, name)
	if err != nil {
		return false, ErrNotFound
	}
	ok, err := h.FS().Exists(relPath)
	if err != nil {
		return false, fmt.Errorf("volume exists %s: %w", relPath, err)
	}
	return ok, nil
}
