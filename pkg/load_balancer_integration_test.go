//go:build integration && !conformance

// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: integration test for HETZNER::Network::LoadBalancer.
//
// Opt-in via the `integration` build tag. NOT run by `make test` — only
// compiled when -tags=integration is set, and only executed when both
// HCLOUD_INTEGRATION=1 and HCLOUD_TOKEN are set (gated by TestMain in
// integration_test.go). Creates with the cheapest LB type and the run-scoped
// integrationLabels, then immediately registers mustCleanup.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestIntegration_LoadBalancer(t *testing.T) {
	client, ctx := withClient(t)

	opts := hcloud.LoadBalancerCreateOpts{
		Name:             "formae-it-lb-" + runID,
		LoadBalancerType: &hcloud.LoadBalancerType{Name: "lb11"},
		Location:         &hcloud.Location{Name: "nbg1"},
		Labels:           integrationLabels(),
	}
	res, _, err := client.LoadBalancer.Create(ctx, opts)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	lb := res.LoadBalancer
	t.Logf("created load balancer %d", lb.ID)
	mustCleanup(t, func() error {
		_, err := client.LoadBalancer.Delete(ctx, lb)
		return err
	})

	// Wait briefly for the create action to settle, then Read.
	waitForAction(t, ctx, client, res.Action.ID)

	got, _, err := client.LoadBalancer.GetByID(ctx, lb.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("load balancer not found after create")
	}
	if got.Name != opts.Name {
		t.Errorf("Name: want %q, got %q", opts.Name, got.Name)
	}
	if got.Labels["managed_by"] != integrationLabelManagedBy {
		t.Errorf("managed_by label: want %q, got %q", integrationLabelManagedBy, got.Labels["managed_by"])
	}
}

// waitForAction polls an hcloud Action until it leaves the running state.
// Best-effort: bounded retry so a stuck action does not hang the suite.
func waitForAction(t *testing.T, ctx context.Context, c *hcloud.Client, actionID int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		a, _, err := c.Action.GetByID(ctx, actionID)
		if err != nil {
			t.Fatalf("action status: %v", err)
		}
		if a == nil {
			t.Fatalf("action %d not found", actionID)
		}
		switch a.Status {
		case hcloud.ActionStatusSuccess:
			return
		case hcloud.ActionStatusError:
			t.Fatalf("action %d failed: %s", actionID, a.ErrorMessage)
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("action %d timed out", actionID)
}
