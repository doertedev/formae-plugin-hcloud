//go:build integration && !conformance

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// TestIntegration_ImageSnapshot creates a cheapest server, snapshots it, then
// reads the image back and asserts type == snapshot. The server and the image
// are both cleaned up via mustCleanup.
func TestIntegration_ImageSnapshot(t *testing.T) {
	c, ctx := withClient(t)

	// Create a cheapest server first — a snapshot needs a source.
	sres, _, err := c.Server.Create(ctx, hcloud.ServerCreateOpts{
		Name:       "formae-int-snap-source",
		ServerType: &hcloud.ServerType{Name: "cx23"},
		Image:      &hcloud.Image{Name: "ubuntu-24.04"},
		Labels:     integrationLabels(),
	})
	if err != nil {
		t.Fatalf("create source server: %v", err)
	}
	server := sres.Server
	mustCleanup(t, func() error {
		_, err := c.Server.Delete(ctx, server)
		return err
	})
	if err := c.Action.WaitFor(ctx, sres.Action); err != nil {
		t.Fatalf("wait for server create: %v", err)
	}

	desc := "formae integration snapshot"
	ires, _, err := c.Server.CreateImage(ctx, server, &hcloud.ServerCreateImageOpts{
		Type:        hcloud.ImageTypeSnapshot,
		Description: &desc,
		Labels:      integrationLabels(),
	})
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	img := ires.Image
	mustCleanup(t, func() error {
		_, err := c.Image.Delete(ctx, img)
		return err
	})
	if err := c.Action.WaitFor(ctx, ires.Action); err != nil {
		t.Fatalf("wait for create image: %v", err)
	}

	got, _, err := c.Image.GetByID(ctx, img.ID)
	if err != nil {
		t.Fatalf("get image: %v", err)
	}
	if got == nil {
		t.Fatal("image not found after create")
	}
	if got.Type != hcloud.ImageTypeSnapshot {
		t.Errorf("type: want snapshot, got %q", got.Type)
	}
}
