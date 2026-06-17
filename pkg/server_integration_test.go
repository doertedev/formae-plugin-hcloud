//go:build integration && !conformance

// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: integration test for HETZNER::Compute::Server.
//
// Opt-in via the `integration` build tag. NOT run by `make test` — only
// compiled when -tags=integration is set, and only executed when both
// HCLOUD_INTEGRATION=1 and HCLOUD_TOKEN are set (gated by TestMain in
// integration_test.go). Creates with the cheapest server type and the
// run-scoped integrationLabels, then immediately registers mustCleanup.

package main

import (
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestIntegration_Server(t *testing.T) {
	client, ctx := withClient(t)

	opts := hcloud.ServerCreateOpts{
		Name:       "formae-it-server-" + runID,
		ServerType: &hcloud.ServerType{Name: "cx23"},
		Image:      &hcloud.Image{Name: "ubuntu-24.04"},
		Location:   &hcloud.Location{Name: "nbg1"},
		Labels:     integrationLabels(),
	}
	res, _, err := client.Server.Create(ctx, opts)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	server := res.Server
	t.Logf("created server %d", server.ID)
	mustCleanup(t, func() error {
		_, err := client.Server.Delete(ctx, server)
		return err
	})

	// Wait briefly for the create action to settle, then Read.
	waitForAction(t, ctx, client, res.Action.ID)

	got, _, err := client.Server.GetByID(ctx, server.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("server not found after create")
	}
	if got.Name != opts.Name {
		t.Errorf("Name: want %q, got %q", opts.Name, got.Name)
	}
	if got.Labels["managed_by"] != integrationLabelManagedBy {
		t.Errorf("managed_by label: want %q, got %q", integrationLabelManagedBy, got.Labels["managed_by"])
	}
	if got.PublicNet.IPv4.IP == nil {
		t.Errorf("PublicNet.IPv4: want non-nil, got nil")
	}
}
