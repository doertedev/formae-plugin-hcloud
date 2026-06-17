// SPDX-License-Identifier: Apache-2.0

//go:build conformance && !integration

// formae conformance tests for the plugin. Run with: make conformance-test
//
// These tests do NOT call the hcloud API directly. They use the official
// formae conformance harness
// (github.com/platform-engineering-labs/formae/pkg/plugin-conformance-tests)
// together with the Pkl fixtures under testdata/ to drive the real
// plugin binary end-to-end through formae apply / inventory / extract / sync /
// destroy. They verify the plugin CONTRACT (CRUD semantics, action handling,
// inventory sync, label propagation, discovery) — not raw hcloud API
// behaviour. Direct live hcloud provider smoke tests live in the
// `*_integration_test.go` files under the mutually exclusive `integration`
// build tag.
package main

import (
	"testing"

	conformance "github.com/platform-engineering-labs/formae/pkg/plugin-conformance-tests"
)

func TestPluginConformance(t *testing.T) {
	conformance.RunCRUDTests(t)
}

func TestPluginDiscovery(t *testing.T) {
	conformance.RunDiscoveryTests(t)
}
