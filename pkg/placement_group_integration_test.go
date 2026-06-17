//go:build integration && !conformance

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// TestIntegration_PlacementGroup creates a placement group, reads it back,
// and asserts type == spread. mustCleanup deletes it after the test.
func TestIntegration_PlacementGroup(t *testing.T) {
	c, ctx := withClient(t)

	res, _, err := c.PlacementGroup.Create(ctx, hcloud.PlacementGroupCreateOpts{
		Name:   "formae-int-pg",
		Type:   hcloud.PlacementGroupTypeSpread,
		Labels: integrationLabels(),
	})
	if err != nil {
		t.Fatalf("create placement group: %v", err)
	}
	pg := res.PlacementGroup
	mustCleanup(t, func() error {
		_, err := c.PlacementGroup.Delete(ctx, pg)
		return err
	})

	got, _, err := c.PlacementGroup.GetByID(ctx, pg.ID)
	if err != nil {
		t.Fatalf("get placement group: %v", err)
	}
	if got == nil {
		t.Fatal("placement group not found after create")
	}
	if got.Type != hcloud.PlacementGroupTypeSpread {
		t.Errorf("type: want spread, got %q", got.Type)
	}
}
