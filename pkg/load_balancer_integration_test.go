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
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
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

// TestIntegration_LoadBalancer_WithService exercises the synchronous
// services/targets path through the real hcloud API: it drives the plugin's
// create + reconcile path directly against a real LB. A TCP service on
// listen_port 64321 is added at create time and then mutated via update to
// confirm the add/update/delete diff path works end-to-end.
func TestIntegration_LoadBalancer_WithService(t *testing.T) {
	client, ctx := withClient(t)
	p := newPluginWithClient(productionClient{c: client})

	createProps := fmt.Sprintf(
		`{"name":"formae-it-lb-svc-%s","loadBalancerType":"lb11","location":"nbg1",`+
			`"labels":%s,`+
			`"services":[{"protocol":"tcp","listenPort":64321,"destinationPort":64321}]}`,
		runID, integrationLabelsJSON(),
	)
	createRes, err := p.Create(ctx, &resource.CreateRequest{
		ResourceType: LoadBalancerResourceType,
		Properties:   json.RawMessage(createProps),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if createRes.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("create status: want Success, got %q (msg=%q code=%q)",
			createRes.ProgressResult.OperationStatus,
			createRes.ProgressResult.StatusMessage,
			createRes.ProgressResult.ErrorCode)
	}
	nativeID := createRes.ProgressResult.NativeID
	t.Logf("created load balancer %s with a TCP service", nativeID)
	id, _ := strconv.ParseInt(nativeID, 10, 64)
	mustCleanup(t, func() error {
		_, err := client.LoadBalancer.Delete(ctx, &hcloud.LoadBalancer{ID: id})
		return err
	})

	// Update the service in place (same listenPort, new destinationPort)
	// and add a second service — exercises UpdateService and AddService.
	updateProps := fmt.Sprintf(
		`{"name":"formae-it-lb-svc-%s","loadBalancerType":"lb11",`+
			`"services":[`+
			`{"protocol":"tcp","listenPort":64321,"destinationPort":64322},`+
			`{"protocol":"tcp","listenPort":64323,"destinationPort":64323}`+
			`]}`,
		runID,
	)
	if _, err := p.Update(ctx, &resource.UpdateRequest{
		ResourceType:      LoadBalancerResourceType,
		NativeID:          nativeID,
		DesiredProperties: json.RawMessage(updateProps),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, _, err := client.LoadBalancer.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("read-back: %v", err)
	}
	ports := make(map[int]int, len(got.Services))
	for _, s := range got.Services {
		ports[s.ListenPort] = s.DestinationPort
	}
	if ports[64321] != 64322 {
		t.Errorf("service 64321 destinationPort after update: want 64322, got %d", ports[64321])
	}
	if _, ok := ports[64323]; !ok {
		t.Errorf("service 64323 missing after update; got services %v", ports)
	}
}

// integrationLabelsJSON returns the integration labels as a JSON object
// string, suitable for embedding directly in a desired-properties JSON blob.
func integrationLabelsJSON() string {
	return `{"managed_by":"` + integrationLabelManagedBy + `","run":"` + runID + `"}`
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
