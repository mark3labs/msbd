package core

// snapshot.go — sandbox rootfs snapshots: create, list, inspect, verify,
// remove, export, import, reindex.

import (
	"context"
	"fmt"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Snapshot is the provider-neutral snapshot summary.
type Snapshot struct {
	Digest       string
	Name         *string
	ParentDigest *string
	ImageRef     string
	Format       string
	SizeBytes    *uint64
	Path         string
	CreatedAt    time.Time
}

// SnapshotCreateParams configures snapshot creation from a stopped sandbox.
type SnapshotCreateParams struct {
	SourceSandbox   string
	Name            string
	Path            string
	Labels          map[string]string
	Force           bool
	RecordIntegrity bool
}

// SnapshotVerify is the result of a snapshot integrity check.
type SnapshotVerify struct {
	Digest      string `json:"digest"`
	Path        string `json:"path"`
	UpperKind   string `json:"upper_kind"`
	UpperAlgo   string `json:"upper_algorithm"`
	UpperDigest string `json:"upper_digest"`
}

func snapshotFromHandle(h *msb.SnapshotHandle) Snapshot {
	return Snapshot{
		Digest:       h.Digest(),
		Name:         h.Name(),
		ParentDigest: h.ParentDigest(),
		ImageRef:     h.ImageRef(),
		Format:       h.Format(),
		SizeBytes:    h.SizeBytes(),
		Path:         h.Path(),
		CreatedAt:    h.CreatedAt(),
	}
}

func (s *Service) CreateSnapshot(ctx context.Context, p SnapshotCreateParams) (*Snapshot, error) {
	art, err := msb.Snapshot.Create(ctx, p.SourceSandbox, msb.SnapshotCreateOptions{
		Name:            p.Name,
		Path:            p.Path,
		Labels:          p.Labels,
		Force:           p.Force,
		RecordIntegrity: p.RecordIntegrity,
	})
	if err != nil {
		return nil, fmt.Errorf("create snapshot: %w", err)
	}
	parent := art.Parent()
	created, _ := time.Parse(time.RFC3339, art.CreatedAt())
	var name *string
	if n := art.SourceSandbox(); n != nil {
		name = n
	}
	return &Snapshot{
		Digest:       art.Digest(),
		Name:         name,
		ParentDigest: parent,
		ImageRef:     art.ImageRef(),
		Format:       art.Format(),
		Path:         art.Path(),
		CreatedAt:    created,
	}, nil
}

func (s *Service) ListSnapshots(ctx context.Context) ([]Snapshot, error) {
	handles, err := msb.Snapshot.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	out := make([]Snapshot, 0, len(handles))
	for _, h := range handles {
		out = append(out, snapshotFromHandle(h))
	}
	return out, nil
}

func (s *Service) GetSnapshot(ctx context.Context, nameOrDigest string) (*Snapshot, error) {
	h, err := msb.Snapshot.Get(ctx, nameOrDigest)
	if err != nil {
		return nil, ErrNotFound
	}
	snap := snapshotFromHandle(h)
	return &snap, nil
}

func (s *Service) VerifySnapshot(ctx context.Context, pathOrName string) (*SnapshotVerify, error) {
	art, err := msb.Snapshot.Open(ctx, pathOrName)
	if err != nil {
		return nil, ErrNotFound
	}
	rep, err := art.Verify(ctx)
	if err != nil {
		return nil, fmt.Errorf("verify snapshot: %w", err)
	}
	return &SnapshotVerify{
		Digest:      rep.Digest,
		Path:        rep.Path,
		UpperKind:   rep.Upper.Kind,
		UpperAlgo:   rep.Upper.Algorithm,
		UpperDigest: rep.Upper.Digest,
	}, nil
}

func (s *Service) RemoveSnapshot(ctx context.Context, pathOrName string, force bool) error {
	if err := msb.Snapshot.Remove(ctx, pathOrName, force); err != nil {
		return fmt.Errorf("remove snapshot %s: %w", pathOrName, err)
	}
	return nil
}

func (s *Service) ReindexSnapshots(ctx context.Context, dir string) (uint32, error) {
	n, err := msb.Snapshot.Reindex(ctx, dir)
	if err != nil {
		return 0, fmt.Errorf("reindex snapshots: %w", err)
	}
	return n, nil
}

func (s *Service) ExportSnapshot(ctx context.Context, nameOrPath, outPath string, withParents, withImage, plainTar bool) error {
	if err := msb.Snapshot.Export(ctx, nameOrPath, outPath, msb.SnapshotExportOptions{
		WithParents: withParents,
		WithImage:   withImage,
		PlainTar:    plainTar,
	}); err != nil {
		return fmt.Errorf("export snapshot %s: %w", nameOrPath, err)
	}
	return nil
}

func (s *Service) ImportSnapshot(ctx context.Context, archive, dest string) (*Snapshot, error) {
	h, err := msb.Snapshot.Import(ctx, archive, dest)
	if err != nil {
		return nil, fmt.Errorf("import snapshot: %w", err)
	}
	snap := snapshotFromHandle(h)
	return &snap, nil
}
