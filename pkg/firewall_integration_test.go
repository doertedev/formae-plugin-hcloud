//go:build integration && !conformance

// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: integration test for HETZNER::Security::Firewall.
//
// Opt-in via the `integration` build tag. NOT run by `make test`. Creates a
// firewall with a single inbound HTTPS rule and integrationLabels, then
// immediately registers mustCleanup. hcloud Firewall Create returns Actions
// only when ApplyTo is set; with no ApplyTo the create is synchronous and no
// Action wait is required.

package main

import (
	"net"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestIntegration_Firewall(t *testing.T) {
	client, ctx := withClient(t)

	_, srcNet, err := net.ParseCIDR("0.0.0.0/0")
	if err != nil {
		t.Fatalf("parse cidr: %v", err)
	}
	port := "443"
	opts := hcloud.FirewallCreateOpts{
		Name:   "formae-it-fw-" + runID,
		Labels: integrationLabels(),
		Rules: []hcloud.FirewallRule{{
			Direction: hcloud.FirewallRuleDirectionIn,
			Protocol:  hcloud.FirewallRuleProtocolTCP,
			SourceIPs: []net.IPNet{*srcNet},
			Port:      &port,
		}},
	}
	res, _, err := client.Firewall.Create(ctx, opts)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	fw := res.Firewall
	t.Logf("created firewall %d", fw.ID)
	mustCleanup(t, func() error {
		_, err := client.Firewall.Delete(ctx, fw)
		return err
	})

	got, _, err := client.Firewall.GetByID(ctx, fw.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("firewall not found after create")
	}
	if got.Name != opts.Name {
		t.Errorf("Name: want %q, got %q", opts.Name, got.Name)
	}
	if len(got.Rules) != 1 {
		t.Fatalf("Rules len: want 1, got %d", len(got.Rules))
	}
	if got.Rules[0].Direction != hcloud.FirewallRuleDirectionIn {
		t.Errorf("Rule[0] Direction: want in, got %q", got.Rules[0].Direction)
	}
	if got.Rules[0].Protocol != hcloud.FirewallRuleProtocolTCP {
		t.Errorf("Rule[0] Protocol: want tcp, got %q", got.Rules[0].Protocol)
	}
	if got.Labels["managed_by"] != integrationLabelManagedBy {
		t.Errorf("managed_by label: want %q, got %q", integrationLabelManagedBy, got.Labels["managed_by"])
	}
}
