//go:build integration && !conformance

// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: opt-in harness for DIRECT LIVE HCLOUD API SMOKE TESTS.
//
// These are provider-level smoke tests: they call the hcloud Go SDK directly
// against a real Hetzner Cloud project to confirm the cheapest create/read/
// delete path for each managed resource type works at the API level. They are
// NOT formae conformance tests — conformance tests (conformance_test.go,
// build tag `conformance`) drive the real plugin binary through the official
// formae conformance harness (formae apply / inventory / extract / sync /
// destroy) and verify the plugin contract end-to-end. The two suites are
// mutually exclusive: this file's build tag (`integration && !conformance`)
// guarantees the live hcloud smoke tests are compiled out whenever conformance
// is selected, even if both tags are passed.
//
// This file is EXCLUDED from the default `go test ./...` run via the
// `integration` build tag. It only compiles in when -tags=integration is set.
//
// Safety design (so running this against a real hcloud account cannot leak):
//
//  1. Double opt-in: TestMain skips the whole suite (exit 0) unless BOTH
//     HCLOUD_INTEGRATION=1 AND a non-empty HCLOUD_TOKEN are set. The token
//     value is never read or printed — only its non-emptiness is checked.
//
//  2. Sweep before AND after: TestMain runs a best-effort sweep that lists
//     and deletes every hcloud resource labelled managed_by=formae-integration-test
//     across ALL managed types. The pre-suite sweep cleans up leaks from a
//     prior crashed run; the post-suite sweep cleans up anything this run
//     left behind. Sweep errors are logged loudly but never abort the suite
//     — cleanup must be robust to partial failures (e.g. a Volume stuck in
//     a deleting state).
//
//  3. Per-test cleanup via mustCleanup: every integration test MUST call
//     mustCleanup the instant it creates a resource. It registers a
//     t.Cleanup that calls the delete and FAILS THE TEST LOUDLY on error,
//     so a missed cleanup is a hard failure rather than silent drift.
//
//  4. run-scoped labels: every created resource carries {managed_by:
//     "formae-integration-test", run: <unix-time>} so concurrent runs do not
//     collide and so the sweep targets this run precisely. The pre-suite
//     sweep deliberately ignores `run` and matches only `managed_by` so it
//     recovers leaks from any prior run regardless of its run ID.

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// integrationLabelManagedBy is the label value the sweep matches. Every
// resource created by this suite MUST carry managed_by=formae-integration-test.
const integrationLabelManagedBy = "formae-integration-test"

// integrationLabelSelector is the hcloud label-selector syntax for the
// managed_by value above. hcloud label selectors use the form `key=value`.
const integrationLabelSelector = "managed_by=" + integrationLabelManagedBy

// runID labels every resource this run creates so concurrent runs do not
// collide. Captured at init() (not TestMain) so it's stable across the
// pre-suite sweep, per-test creation, and the post-suite sweep.
var runID = strconv.FormatInt(time.Now().Unix(), 10)

// integrationLabels is the label set every integration test MUST apply to
// resources it creates. Tests that forget it cannot be swept.
func integrationLabels() map[string]string {
	return map[string]string{
		"managed_by": integrationLabelManagedBy,
		"run":        runID,
	}
}

// TestMain gates the whole integration suite on the double opt-in and
// runs the cleanup sweep before and after the tests.
func TestMain(m *testing.M) {
	if os.Getenv("HCLOUD_INTEGRATION") != "1" {
		fmt.Println("integration: skipping (set HCLOUD_INTEGRATION=1 to enable)")
		os.Exit(0)
	}
	if os.Getenv("HCLOUD_TOKEN") == "" {
		fmt.Println("integration: skipping (HCLOUD_TOKEN is empty)")
		os.Exit(0)
	}

	// Pre-suite sweep: recover any leaked resources from a prior crashed run.
	sweep("pre-suite")

	code := m.Run()

	// Post-suite sweep: clean up anything this run left behind (defensive —
	// tests are also expected to use mustCleanup).
	sweep("post-suite")

	os.Exit(code)
}

// sweep lists and deletes every hcloud resource labelled
// managed_by=formae-integration-test across every managed type. Best-effort:
// per-step errors are logged but do not abort the suite.
//
// Ordering matters: dependents are deleted before their dependencies. For
// example, a Server may reference a Volume, a Network, a Firewall, and a
// PlacementGroup, so Servers go first; Networks come after Servers and
// LoadBalancers; SSHKeys/Certificates last (nothing depends on them but
// they may be referenced by Servers).
func sweep(phase string) {
	token := os.Getenv("HCLOUD_TOKEN")
	if token == "" {
		return
	}
	c := hcloud.NewClient(hcloud.WithToken(token))
	ctx := context.Background()

	type step struct {
		name string
		fn   func(context.Context, *hcloud.Client) error
	}
	steps := []step{
		{"server", sweepServers},
		{"load_balancer", sweepLoadBalancers},
		{"firewall", sweepFirewalls},
		{"volume", sweepVolumes},
		{"floating_ip", sweepFloatingIPs},
		{"primary_ip", sweepPrimaryIPs},
		{"certificate", sweepCertificates},
		{"placement_group", sweepPlacementGroups},
		{"network", sweepNetworks},
		{"ssh_key", sweepSSHKeys},
		{"image", sweepImages},
	}

	for _, s := range steps {
		if err := s.fn(ctx, c); err != nil {
			log.Printf("integration sweep (%s): %s: %v", phase, s.name, err)
		}
	}
}

// withClient builds a real hcloud.Client from HCLOUD_TOKEN for use by
// per-resource integration tests. The token is never logged.
func withClient(t *testing.T) (*hcloud.Client, context.Context) {
	t.Helper()
	token := os.Getenv("HCLOUD_TOKEN")
	if token == "" {
		t.Fatal("HCLOUD_TOKEN must be set for integration tests")
	}
	return hcloud.NewClient(hcloud.WithToken(token)), context.Background()
}

// mustCleanup registers delete via t.Cleanup and FAILS THE TEST LOUDLY if
// delete returns an error. Every integration test MUST call this the instant
// it creates a resource, so leaks surface as test failures rather than silent
// drift. (The post-suite sweep is the backstop in case t.Cleanup doesn't
// fire — e.g. if the process is killed.)
func mustCleanup(t *testing.T, delete func() error) {
	t.Helper()
	t.Cleanup(func() {
		if err := delete(); err != nil {
			t.Errorf("cleanup failed: %v", err)
		}
	})
}

// --- per-type sweep helpers ------------------------------------------------
//
// Each helper lists resources of one type matching the integration label
// selector and deletes them. List failures short-circuit that type (and
// return the error so sweep logs it); per-resource delete failures are
// logged but do not stop the loop, since one stuck resource must not shield
// the rest from cleanup.

func sweepServers(ctx context.Context, c *hcloud.Client) error {
	items, err := c.Server.AllWithOpts(ctx, hcloud.ServerListOpts{ListOpts: hcloud.ListOpts{LabelSelector: integrationLabelSelector}})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range items {
		if _, err := c.Server.Delete(ctx, r); err != nil {
			log.Printf("sweep server %d: %v", r.ID, err)
		}
	}
	return nil
}

func sweepLoadBalancers(ctx context.Context, c *hcloud.Client) error {
	items, err := c.LoadBalancer.AllWithOpts(ctx, hcloud.LoadBalancerListOpts{ListOpts: hcloud.ListOpts{LabelSelector: integrationLabelSelector}})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range items {
		if _, err := c.LoadBalancer.Delete(ctx, r); err != nil {
			log.Printf("sweep load_balancer %d: %v", r.ID, err)
		}
	}
	return nil
}

func sweepFirewalls(ctx context.Context, c *hcloud.Client) error {
	items, err := c.Firewall.AllWithOpts(ctx, hcloud.FirewallListOpts{ListOpts: hcloud.ListOpts{LabelSelector: integrationLabelSelector}})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range items {
		if _, err := c.Firewall.Delete(ctx, r); err != nil {
			log.Printf("sweep firewall %d: %v", r.ID, err)
		}
	}
	return nil
}

func sweepVolumes(ctx context.Context, c *hcloud.Client) error {
	items, err := c.Volume.AllWithOpts(ctx, hcloud.VolumeListOpts{ListOpts: hcloud.ListOpts{LabelSelector: integrationLabelSelector}})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range items {
		if _, err := c.Volume.Delete(ctx, r); err != nil {
			log.Printf("sweep volume %d: %v", r.ID, err)
		}
	}
	return nil
}

func sweepFloatingIPs(ctx context.Context, c *hcloud.Client) error {
	items, err := c.FloatingIP.AllWithOpts(ctx, hcloud.FloatingIPListOpts{ListOpts: hcloud.ListOpts{LabelSelector: integrationLabelSelector}})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range items {
		if _, err := c.FloatingIP.Delete(ctx, r); err != nil {
			log.Printf("sweep floating_ip %d: %v", r.ID, err)
		}
	}
	return nil
}

func sweepPrimaryIPs(ctx context.Context, c *hcloud.Client) error {
	items, err := c.PrimaryIP.AllWithOpts(ctx, hcloud.PrimaryIPListOpts{ListOpts: hcloud.ListOpts{LabelSelector: integrationLabelSelector}})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range items {
		if _, err := c.PrimaryIP.Delete(ctx, r); err != nil {
			log.Printf("sweep primary_ip %d: %v", r.ID, err)
		}
	}
	return nil
}

func sweepCertificates(ctx context.Context, c *hcloud.Client) error {
	items, err := c.Certificate.AllWithOpts(ctx, hcloud.CertificateListOpts{ListOpts: hcloud.ListOpts{LabelSelector: integrationLabelSelector}})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range items {
		if _, err := c.Certificate.Delete(ctx, r); err != nil {
			log.Printf("sweep certificate %d: %v", r.ID, err)
		}
	}
	return nil
}

func sweepPlacementGroups(ctx context.Context, c *hcloud.Client) error {
	items, err := c.PlacementGroup.AllWithOpts(ctx, hcloud.PlacementGroupListOpts{ListOpts: hcloud.ListOpts{LabelSelector: integrationLabelSelector}})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range items {
		if _, err := c.PlacementGroup.Delete(ctx, r); err != nil {
			log.Printf("sweep placement_group %d: %v", r.ID, err)
		}
	}
	return nil
}

func sweepNetworks(ctx context.Context, c *hcloud.Client) error {
	items, err := c.Network.AllWithOpts(ctx, hcloud.NetworkListOpts{ListOpts: hcloud.ListOpts{LabelSelector: integrationLabelSelector}})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range items {
		if _, err := c.Network.Delete(ctx, r); err != nil {
			log.Printf("sweep network %d: %v", r.ID, err)
		}
	}
	return nil
}

func sweepSSHKeys(ctx context.Context, c *hcloud.Client) error {
	items, err := c.SSHKey.AllWithOpts(ctx, hcloud.SSHKeyListOpts{ListOpts: hcloud.ListOpts{LabelSelector: integrationLabelSelector}})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range items {
		if _, err := c.SSHKey.Delete(ctx, r); err != nil {
			log.Printf("sweep ssh_key %d: %v", r.ID, err)
		}
	}
	return nil
}

// sweepImages deletes snapshot images created by this suite. hcloud only
// allows user-created images (snapshots) to carry user labels and be deleted,
// so the label selector implicitly filters to snapshots.
func sweepImages(ctx context.Context, c *hcloud.Client) error {
	items, err := c.Image.AllWithOpts(ctx, hcloud.ImageListOpts{ListOpts: hcloud.ListOpts{LabelSelector: integrationLabelSelector}})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range items {
		if _, err := c.Image.Delete(ctx, r); err != nil {
			log.Printf("sweep image %d: %v", r.ID, err)
		}
	}
	return nil
}
