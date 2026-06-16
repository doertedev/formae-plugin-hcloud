//go:build integration && !conformance

// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: integration test for HETZNER::Security::Certificate.
//
// Opt-in via the `integration` build tag. NOT run by `make test`. Creates an
// uploaded certificate with the cheapest valid PEM (a freshly-generated
// self-signed pair) labelled with integrationLabels, then immediately
// registers mustCleanup. hcloud Certificate Create is synchronous, so there
// is no Action to wait on.

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestIntegration_Certificate(t *testing.T) {
	client, ctx := withClient(t)

	certPEM, keyPEM := selfSignedPEM(t, "formae-it-cert-"+runID)

	opts := hcloud.CertificateCreateOpts{
		Name:        "formae-it-cert-" + runID,
		Type:        hcloud.CertificateTypeUploaded,
		Certificate: certPEM,
		PrivateKey:  keyPEM,
		Labels:      integrationLabels(),
	}
	cert, _, err := client.Certificate.Create(ctx, opts)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Logf("created certificate %d", cert.ID)
	mustCleanup(t, func() error {
		_, err := client.Certificate.Delete(ctx, cert)
		return err
	})

	got, _, err := client.Certificate.GetByID(ctx, cert.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("certificate not found after create")
	}
	if got.Name != opts.Name {
		t.Errorf("Name: want %q, got %q", opts.Name, got.Name)
	}
	if got.Fingerprint == "" {
		t.Errorf("Fingerprint: want non-empty, got empty")
	}
	if got.Labels["managed_by"] != integrationLabelManagedBy {
		t.Errorf("managed_by label: want %q, got %q", integrationLabelManagedBy, got.Labels["managed_by"])
	}
}

// selfSignedPEM mints a minimal self-signed certificate + EC private key for
// integration tests. It must not be used outside tests.
func selfSignedPEM(t *testing.T, commonName string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}
