package core

// metrics.go — point-in-time sandbox resource metrics.

import (
	"context"
	"fmt"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Metrics is a point-in-time resource snapshot for one sandbox.
type Metrics struct {
	ID                      string
	CPUPercent              float64
	MemoryBytes             uint64
	MemoryAvailableBytes    *uint64
	MemoryHostResidentBytes *uint64
	MemoryLimitBytes        uint64
	DiskReadBytes           uint64
	DiskWriteBytes          uint64
	NetRxBytes              uint64
	NetTxBytes              uint64
	UpperUsedBytes          *uint64
	UpperFreeBytes          *uint64
	UptimeSeconds           float64
}

func metricsFromSDK(id string, m *msb.Metrics) *Metrics {
	if m == nil {
		return &Metrics{ID: id}
	}
	return &Metrics{
		ID:                      id,
		CPUPercent:              m.CPUPercent,
		MemoryBytes:             m.MemoryBytes,
		MemoryAvailableBytes:    m.MemoryAvailableBytes,
		MemoryHostResidentBytes: m.MemoryHostResidentBytes,
		MemoryLimitBytes:        m.MemoryLimitBytes,
		DiskReadBytes:           m.DiskReadBytes,
		DiskWriteBytes:          m.DiskWriteBytes,
		NetRxBytes:              m.NetRxBytes,
		NetTxBytes:              m.NetTxBytes,
		UpperUsedBytes:          m.UpperUsedBytes,
		UpperFreeBytes:          m.UpperFreeBytes,
		UptimeSeconds:           m.Uptime.Seconds(),
	}
}

// Metrics returns a point-in-time snapshot for one running sandbox.
func (s *Service) Metrics(ctx context.Context, id string) (*Metrics, error) {
	h, err := msb.GetSandbox(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	m, err := h.Metrics(ctx)
	if err != nil {
		return nil, fmt.Errorf("metrics %s: %w", id, err)
	}
	return metricsFromSDK(id, m), nil
}

// AllMetrics returns snapshots for every running sandbox.
func (s *Service) AllMetrics(ctx context.Context) ([]Metrics, error) {
	all, err := msb.AllSandboxMetrics(ctx)
	if err != nil {
		return nil, fmt.Errorf("all metrics: %w", err)
	}
	out := make([]Metrics, 0, len(all))
	for id, m := range all {
		out = append(out, *metricsFromSDK(id, m))
	}
	return out, nil
}
