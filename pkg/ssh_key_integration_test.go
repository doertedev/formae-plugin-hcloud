//go:build integration && !conformance

// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: integration test for HETZNER::Security::SSHKey.
//
// Opt-in via the `integration` build tag. NOT run by `make test`. Creates an
// SSH key from a freshly-generated ed25519 pair (encoded to OpenSSH wire
// format using stdlib only — no golang.org/x/crypto dependency), then
// immediately registers mustCleanup. hcloud SSHKey Create is synchronous, so
// there is no Action to wait on.

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestIntegration_SSHKey(t *testing.T) {
	client, ctx := withClient(t)

	publicKey := generateEd25519OpenSSH(t)

	opts := hcloud.SSHKeyCreateOpts{
		Name:      "formae-it-key-" + runID,
		PublicKey: publicKey,
		Labels:    integrationLabels(),
	}
	key, _, err := client.SSHKey.Create(ctx, opts)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Logf("created ssh key %d (%s)", key.ID, key.Fingerprint)
	mustCleanup(t, func() error {
		_, err := client.SSHKey.Delete(ctx, key)
		return err
	})

	got, _, err := client.SSHKey.GetByID(ctx, key.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("ssh key not found after create")
	}
	if got.Name != opts.Name {
		t.Errorf("Name: want %q, got %q", opts.Name, got.Name)
	}
	if got.PublicKey != publicKey {
		t.Errorf("PublicKey: want forwarded, got mismatch")
	}
	if got.Fingerprint == "" {
		t.Errorf("Fingerprint: want non-empty, got empty")
	}
	if got.Labels["managed_by"] != integrationLabelManagedBy {
		t.Errorf("managed_by label: want %q, got %q", integrationLabelManagedBy, got.Labels["managed_by"])
	}
}

// generateEd25519OpenSSH mints a fresh ed25519 keypair and returns the public
// key in OpenSSH authorized_keys format ("ssh-ed25519 <base64-wire>"), using
// stdlib only. The wire format is two length-prefixed fields: the key-type
// string and the 32-byte raw public key.
func generateEd25519OpenSSH(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	const keyType = "ssh-ed25519"
	buf := make([]byte, 0, 4+len(keyType)+4+len(pub))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(keyType)))
	buf = append(buf, keyType...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(pub)))
	buf = append(buf, pub...)
	return keyType + " " + base64.StdEncoding.EncodeToString(buf)
}
