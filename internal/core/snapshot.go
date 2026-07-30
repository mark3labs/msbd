package core

// snapshot.go — sandbox rootfs snapshots: create, list, inspect, verify,
// remove, export, import, reindex.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// newSnapshotName mints a server-side name for an unnamed snapshot. SDK 0.6.7
// requires a name; msbd's API does not, so we supply one that cannot collide
// with a user-chosen name or another generated one.
func newSnapshotName() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "snap_" + hex.EncodeToString(b[:])
}

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
		Format:       derefStr(h.Format()),
		SizeBytes:    h.SizeBytes(),
		Path:         h.Path(),
		CreatedAt:    h.CreatedAt(),
	}
}

// derefStr flattens an optional SDK string into the plain one msbd's wire
// contract uses (SDK 0.6.7 made several snapshot accessors return *string).
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// CreateSnapshot captures a sandbox's rootfs.
//
// SDK 0.6.7 moved the source sandbox into the options struct and made Name
// REQUIRED. msbd's API has always allowed an unnamed snapshot (the caller just
// wants a checkpoint), so generate a stable, collision-resistant name in that
// case rather than pushing the new constraint onto every client.
func (s *Service) CreateSnapshot(ctx context.Context, p SnapshotCreateParams) (*Snapshot, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = newSnapshotName()
	}
	art, err := msb.Snapshot.Create(ctx, msb.SnapshotCreateOptions{
		Name:            name,
		FromSandbox:     p.SourceSandbox,
		DestDir:         p.Path,
		Labels:          p.Labels,
		Force:           p.Force,
		RecordIntegrity: p.RecordIntegrity,
	})
	if err != nil {
		return nil, fmt.Errorf("create snapshot: %w", err)
	}
	created, _ := time.Parse(time.RFC3339, art.CreatedAt())
	return &Snapshot{
		Digest:       art.Digest(),
		Name:         &name,
		ParentDigest: art.Parent(),
		ImageRef:     art.ImageRef(),
		Format:       art.Format(),
		SizeBytes:    art.SizeBytes(),
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

// ExportSnapshot writes a snapshot to a portable archive on the msbd host.
// SDK 0.6.7 renamed Export → Save; msbd keeps the export/import vocabulary
// because it is part of the REST contract.
func (s *Service) ExportSnapshot(ctx context.Context, nameOrPath, outPath string, withParents, withImage, plainTar bool) error {
	safe, err := s.checkHostPath(outPath)
	if err != nil {
		return err
	}
	if err := msb.Snapshot.Save(ctx, nameOrPath, safe, msb.SnapshotSaveOptions{
		WithParents: withParents,
		WithImage:   withImage,
		PlainTar:    plainTar,
	}); err != nil {
		return fmt.Errorf("export snapshot %s: %w", nameOrPath, err)
	}
	return nil
}

// ImportSnapshot restores a snapshot archive. SDK 0.6.7 renamed Import → Load.
func (s *Service) ImportSnapshot(ctx context.Context, archive, dest string) (*Snapshot, error) {
	safeArchive, err := s.checkHostPath(archive)
	if err != nil {
		return nil, err
	}
	safeDest, err := s.checkHostPath(dest)
	if err != nil {
		return nil, err
	}
	h, err := msb.Snapshot.Load(ctx, safeArchive, safeDest)
	if err != nil {
		return nil, fmt.Errorf("import snapshot: %w", err)
	}
	snap := snapshotFromHandle(h)
	return &snap, nil
}
