//go:build integration && !conformance

// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: integration test for HETZNER::Storage::Volume.
//
// Opt-in via the `integration` build tag. NOT run by `make test`. Creates a
// volume with the cheapest size (10 GB) and a location (no server attachment),
// so create is synchronous and no Action wait is required. mustCleanup is
// registered immediately after create.

package main

import (
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestIntegration_Volume(t *testing.T) {
	client, ctx := withClient(t)

	opts := hcloud.VolumeCreateOpts{
		Name:     "formae-it-vol-" + runID,
		Size:     10,
		Location: &hcloud.Location{Name: "nbg1"},
		Labels:   integrationLabels(),
	}
	res, _, err := client.Volume.Create(ctx, opts)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	vol := res.Volume
	t.Logf("created volume %d (%s)", vol.ID, vol.LinuxDevice)
	mustCleanup(t, func() error {
		_, err := client.Volume.Delete(ctx, vol)
		return err
	})

	got, _, err := client.Volume.GetByID(ctx, vol.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("volume not found after create")
	}
	if got.Name != opts.Name {
		t.Errorf("Name: want %q, got %q", opts.Name, got.Name)
	}
	if got.Size != 10 {
		t.Errorf("Size: want 10, got %d", got.Size)
	}
	if got.LinuxDevice == "" {
		t.Errorf("LinuxDevice: want non-empty, got empty")
	}
	if got.Labels["managed_by"] != integrationLabelManagedBy {
		t.Errorf("managed_by label: want %q, got %q", integrationLabelManagedBy, got.Labels["managed_by"])
	}
}
