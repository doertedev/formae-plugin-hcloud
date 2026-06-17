//go:build integration && !conformance

// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: integration test for HETZNER::Network::PrimaryIP.
//
// Opt-in via the `integration` build tag. NOT run by `make test`. Creates a
// cheapest (ipv4) Primary IP with location set and no assignee, so create
// is synchronous and no Action wait is required. mustCleanup is registered
// immediately after create.

package main

import (
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestIntegration_PrimaryIP(t *testing.T) {
	client, ctx := withClient(t)

	opts := hcloud.PrimaryIPCreateOpts{
		Name:     "formae-it-pip-" + runID,
		Type:     hcloud.PrimaryIPTypeIPv4,
		Location: "nbg1",
		Labels:   integrationLabels(),
	}
	res, _, err := client.PrimaryIP.Create(ctx, opts)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pip := res.PrimaryIP
	t.Logf("created primary ip %d (%s)", pip.ID, pip.IP)
	mustCleanup(t, func() error {
		_, err := client.PrimaryIP.Delete(ctx, pip)
		return err
	})

	got, _, err := client.PrimaryIP.GetByID(ctx, pip.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("primary ip not found after create")
	}
	if got.Type != hcloud.PrimaryIPTypeIPv4 {
		t.Errorf("Type: want ipv4, got %q", got.Type)
	}
	if got.IP == nil {
		t.Errorf("IP: want non-nil, got nil")
	}
	if got.Labels["managed_by"] != integrationLabelManagedBy {
		t.Errorf("managed_by label: want %q, got %q", integrationLabelManagedBy, got.Labels["managed_by"])
	}
}
