//go:build integration && !conformance

// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: integration test for HETZNER::Network::FloatingIP.
//
// Opt-in via the `integration` build tag. NOT run by `make test`. Creates a
// cheapest (ipv4) Floating IP with homeLocation set (no server assignment),
// so create is synchronous and no Action wait is required. mustCleanup is
// registered immediately after create.

package main

import (
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestIntegration_FloatingIP(t *testing.T) {
	client, ctx := withClient(t)

	opts := hcloud.FloatingIPCreateOpts{
		Type:         hcloud.FloatingIPTypeIPv4,
		HomeLocation: &hcloud.Location{Name: "nbg1"},
		Labels:       integrationLabels(),
	}
	res, _, err := client.FloatingIP.Create(ctx, opts)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	fip := res.FloatingIP
	t.Logf("created floating ip %d (%s)", fip.ID, fip.IP)
	mustCleanup(t, func() error {
		_, err := client.FloatingIP.Delete(ctx, fip)
		return err
	})

	got, _, err := client.FloatingIP.GetByID(ctx, fip.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("floating ip not found after create")
	}
	if got.Type != hcloud.FloatingIPTypeIPv4 {
		t.Errorf("Type: want ipv4, got %q", got.Type)
	}
	if got.IP == nil {
		t.Errorf("IP: want non-nil, got nil")
	}
	if got.Labels["managed_by"] != integrationLabelManagedBy {
		t.Errorf("managed_by label: want %q, got %q", integrationLabelManagedBy, got.Labels["managed_by"])
	}
}
