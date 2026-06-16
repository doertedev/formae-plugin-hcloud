//go:build integration && !conformance

// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: integration test for HETZNER::Network::Network.
//
// Opt-in via the `integration` build tag. NOT run by `make test`. Creates a
// network with the cheapest ipRange ("10.0.0.0/16") and integrationLabels,
// then immediately registers mustCleanup. hcloud Network Create is
// synchronous, so there is no Action to wait on.

package main

import (
	"net"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestIntegration_Network(t *testing.T) {
	client, ctx := withClient(t)

	_, ipNet, err := net.ParseCIDR("10.0.0.0/16")
	if err != nil {
		t.Fatalf("parse cidr: %v", err)
	}
	opts := hcloud.NetworkCreateOpts{
		Name:    "formae-it-net-" + runID,
		IPRange: ipNet,
		Labels:  integrationLabels(),
	}
	network, _, err := client.Network.Create(ctx, opts)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Logf("created network %d", network.ID)
	mustCleanup(t, func() error {
		_, err := client.Network.Delete(ctx, network)
		return err
	})

	got, _, err := client.Network.GetByID(ctx, network.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("network not found after create")
	}
	if got.Name != opts.Name {
		t.Errorf("Name: want %q, got %q", opts.Name, got.Name)
	}
	if got.IPRange == nil || got.IPRange.String() != "10.0.0.0/16" {
		t.Errorf("IPRange: want 10.0.0.0/16, got %v", got.IPRange)
	}
	if got.Labels["managed_by"] != integrationLabelManagedBy {
		t.Errorf("managed_by label: want %q, got %q", integrationLabelManagedBy, got.Labels["managed_by"])
	}
}
