package core

// image.go — cached OCI image inventory management.

import (
	"context"
	"fmt"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Image is the provider-neutral cached-image summary.
type Image struct {
	Reference      string
	ManifestDigest string
	Architecture   string
	OS             string
	LayerCount     uint
	SizeBytes      *int64
	CreatedAt      time.Time
	LastUsedAt     time.Time
}

// ImageConfig mirrors the parsed OCI config block.
type ImageConfig struct {
	Digest     string            `json:"digest"`
	Env        []string          `json:"env"`
	Cmd        []string          `json:"cmd"`
	Entrypoint []string          `json:"entrypoint"`
	WorkingDir string            `json:"working_dir"`
	User       string            `json:"user"`
	Labels     map[string]string `json:"labels"`
	StopSignal string            `json:"stop_signal"`
}

// ImageLayer mirrors one manifest layer.
type ImageLayer struct {
	DiffID              string `json:"diff_id"`
	BlobDigest          string `json:"blob_digest"`
	MediaType           string `json:"media_type"`
	CompressedSizeBytes *int64 `json:"compressed_size_bytes"`
	Position            int32  `json:"position"`
}

// ImageDetail is the full image inspect result.
type ImageDetail struct {
	Image
	Config *ImageConfig `json:"config"`
	Layers []ImageLayer `json:"layers"`
}

// ImagePruneReport summarizes artifacts removed by a prune.
type ImagePruneReport struct {
	ImageRefsRemoved uint32  `json:"image_refs_removed"`
	ManifestsRemoved uint32  `json:"manifests_removed"`
	LayersRemoved    uint32  `json:"layers_removed"`
	FsmetaRemoved    uint32  `json:"fsmeta_removed"`
	VMDKRemoved      uint32  `json:"vmdk_removed"`
	BytesReclaimed   *uint64 `json:"bytes_reclaimed"`
}

func imageFromHandle(h *msb.ImageHandle) Image {
	return Image{
		Reference:      h.Reference(),
		ManifestDigest: h.ManifestDigest(),
		Architecture:   h.Architecture(),
		OS:             h.OS(),
		LayerCount:     h.LayerCount(),
		SizeBytes:      h.SizeBytes(),
		CreatedAt:      h.CreatedAt(),
		LastUsedAt:     h.LastUsedAt(),
	}
}

func (s *Service) ListImages(ctx context.Context) ([]Image, error) {
	handles, err := msb.Image.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	out := make([]Image, 0, len(handles))
	for _, h := range handles {
		out = append(out, imageFromHandle(h))
	}
	return out, nil
}

func (s *Service) InspectImage(ctx context.Context, ref string) (*ImageDetail, error) {
	d, err := msb.Image.Inspect(ctx, ref)
	if err != nil {
		return nil, ErrNotFound
	}
	detail := &ImageDetail{Image: imageFromHandle(d.ImageHandle)}
	if d.Config != nil {
		detail.Config = &ImageConfig{
			Digest:     d.Config.Digest,
			Env:        d.Config.Env,
			Cmd:        d.Config.Cmd,
			Entrypoint: d.Config.Entrypoint,
			WorkingDir: d.Config.WorkingDir,
			User:       d.Config.User,
			Labels:     d.Config.Labels,
			StopSignal: d.Config.StopSignal,
		}
	}
	for _, l := range d.Layers {
		detail.Layers = append(detail.Layers, ImageLayer{
			DiffID:              l.DiffID,
			BlobDigest:          l.BlobDigest,
			MediaType:           l.MediaType,
			CompressedSizeBytes: l.CompressedSizeBytes,
			Position:            l.Position,
		})
	}
	return detail, nil
}

// PullImage ensures an OCI image is present in the local cache and returns the
// cached summary. force=true re-fetches even when the image is already cached
// (PullPolicyAlways); force=false fetches only when missing (PullPolicyIfMissing).
//
// The microsandbox SDK exposes NO standalone pull operation — the only way to
// fetch an image is to boot a sandbox with a pull policy. So PullImage creates
// a throwaway detached sandbox with WithImage(ref)+WithPullPolicy, tears it
// down, then reads the now-cached image back via msb.Image.Get. This boots a
// real microVM and a cold pull can take minutes: keep it off any low-timeout
// path, exactly like Run.
func (s *Service) PullImage(ctx context.Context, ref string, force bool) (*Image, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("image reference is required")
	}
	policy := msb.PullPolicyIfMissing
	if force {
		policy = msb.PullPolicyAlways
	}
	name := newName()
	opts := []msb.SandboxOption{
		msb.WithImage(ref),
		msb.WithDetached(),
		msb.WithPullPolicy(policy),
	}

	pctx, cancel := context.WithTimeout(ctx, s.pullTO)
	defer cancel()

	sb, err := msb.CreateSandbox(pctx, name, opts...)
	if err != nil {
		return nil, fmt.Errorf("pull %s: %w", ref, err)
	}
	// Tear the throwaway box down on a fresh context so cleanup still runs even
	// if the caller's ctx was cancelled mid-pull. Best-effort.
	defer func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		_ = sb.Stop(cctx)
		_ = msb.RemoveSandbox(cctx, name)
	}()

	h, err := msb.Image.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("pull %s: image not cached after pull: %w", ref, err)
	}
	img := imageFromHandle(h)
	return &img, nil
}

func (s *Service) RemoveImage(ctx context.Context, ref string, force bool) error {
	if err := msb.Image.Remove(ctx, ref, force); err != nil {
		return fmt.Errorf("remove image %s: %w", ref, err)
	}
	return nil
}

func (s *Service) PruneImages(ctx context.Context) (*ImagePruneReport, error) {
	rep, err := msb.Image.Prune(ctx)
	if err != nil {
		return nil, fmt.Errorf("prune images: %w", err)
	}
	return &ImagePruneReport{
		ImageRefsRemoved: rep.ImageRefsRemoved,
		ManifestsRemoved: rep.ManifestsRemoved,
		LayersRemoved:    rep.LayersRemoved,
		FsmetaRemoved:    rep.FsmetaRemoved,
		VMDKRemoved:      rep.VMDKRemoved,
		BytesReclaimed:   rep.BytesReclaimed,
	}, nil
}
