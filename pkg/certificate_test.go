// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// fakeCertificateClient implements hcloud.ICertificateClient.
type fakeCertificateClient struct {
	hcloud.ICertificateClient

	create  func(context.Context, hcloud.CertificateCreateOpts) (*hcloud.Certificate, *hcloud.Response, error)
	getByID func(context.Context, int64) (*hcloud.Certificate, *hcloud.Response, error)
	update  func(context.Context, *hcloud.Certificate, hcloud.CertificateUpdateOpts) (*hcloud.Certificate, *hcloud.Response, error)
	delete  func(context.Context, *hcloud.Certificate) (*hcloud.Response, error)
	all     func(context.Context) ([]*hcloud.Certificate, error)
}

func (f fakeCertificateClient) Create(ctx context.Context, opts hcloud.CertificateCreateOpts) (*hcloud.Certificate, *hcloud.Response, error) {
	if f.create == nil {
		return nil, nil, nil
	}
	return f.create(ctx, opts)
}

func (f fakeCertificateClient) GetByID(ctx context.Context, id int64) (*hcloud.Certificate, *hcloud.Response, error) {
	if f.getByID == nil {
		return nil, nil, nil
	}
	return f.getByID(ctx, id)
}

func (f fakeCertificateClient) Update(ctx context.Context, c *hcloud.Certificate, opts hcloud.CertificateUpdateOpts) (*hcloud.Certificate, *hcloud.Response, error) {
	if f.update == nil {
		return nil, nil, nil
	}
	return f.update(ctx, c, opts)
}

func (f fakeCertificateClient) Delete(ctx context.Context, c *hcloud.Certificate) (*hcloud.Response, error) {
	if f.delete == nil {
		return nil, nil
	}
	return f.delete(ctx, c)
}

func (f fakeCertificateClient) All(ctx context.Context) ([]*hcloud.Certificate, error) {
	if f.all == nil {
		return nil, nil
	}
	return f.all(ctx)
}

var _ hcloud.ICertificateClient = fakeCertificateClient{}

const (
	validCertPEM = "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----\n"
	validKeyPEM  = "-----BEGIN PRIVATE KEY-----\nMIIE...\n-----END PRIVATE KEY-----\n"
)

const validCertificateProps = `{"name":"cert-1","certificate":"` + `-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----\n` + `","privateKey":"` + `-----BEGIN PRIVATE KEY-----\nMIIE...\n-----END PRIVATE KEY-----\n` + `"}`

func sampleCertificate() *hcloud.Certificate {
	return &hcloud.Certificate{
		ID:             88,
		Name:           "cert-1",
		Type:           hcloud.CertificateTypeUploaded,
		Certificate:    validCertPEM,
		Fingerprint:    "aa:bb:cc",
		Labels:         map[string]string{"managed_by": "formae"},
		NotValidBefore: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		NotValidAfter:  time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// --- Create ----------------------------------------------------------------

func TestCertificate_Create_Success(t *testing.T) {
	var captured hcloud.CertificateCreateOpts
	api := fakeAPI{certificate: fakeCertificateClient{
		create: func(_ context.Context, opts hcloud.CertificateCreateOpts) (*hcloud.Certificate, *hcloud.Response, error) {
			captured = opts
			return sampleCertificate(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: CertificateResourceType,
		Properties:   json.RawMessage(validCertificateProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Certificate Create is synchronous — no Action returned by hcloud-go.
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "88" {
		t.Errorf("NativeID: want 88, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae injected, got %q", got)
	}
	if captured.Name != "cert-1" {
		t.Errorf("Name forwarded: want cert-1, got %q", captured.Name)
	}
	if captured.Certificate != validCertPEM {
		t.Errorf("Certificate PEM not forwarded")
	}
	if captured.PrivateKey != validKeyPEM {
		t.Errorf("PrivateKey PEM not forwarded")
	}
	// Read-back MUST NOT contain the private key — write-only field.
	if pr.ResourceProperties != nil {
		var readback map[string]any
		if err := json.Unmarshal(pr.ResourceProperties, &readback); err != nil {
			t.Fatalf("invalid readback JSON: %v", err)
		}
		if _, present := readback["privateKey"]; present {
			t.Errorf("privateKey MUST NOT appear in create read-back; got %v", readback["privateKey"])
		}
	}
}

func TestCertificate_Create_InvalidRequest(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing-name", `{"certificate":"x","privateKey":"y"}`},
		{"missing-certificate", `{"name":"cert-1","privateKey":"y"}`},
		{"missing-privateKey", `{"name":"cert-1","certificate":"x"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPluginWithClient(fakeAPI{})
			res, err := p.Create(context.Background(), &resource.CreateRequest{
				ResourceType: CertificateResourceType,
				Properties:   json.RawMessage(c.json),
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
				t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
			}
		})
	}
}

func TestCertificate_Create_APIError(t *testing.T) {
	api := fakeAPI{certificate: fakeCertificateClient{
		create: func(context.Context, hcloud.CertificateCreateOpts) (*hcloud.Certificate, *hcloud.Response, error) {
			return nil, nil, errors.New("invalid pem")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: CertificateResourceType,
		Properties:   json.RawMessage(validCertificateProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", code)
	}
}

// --- Read ------------------------------------------------------------------

func TestCertificate_Read_Existing(t *testing.T) {
	api := fakeAPI{certificate: fakeCertificateClient{
		getByID: func(_ context.Context, id int64) (*hcloud.Certificate, *hcloud.Response, error) {
			if id != 88 {
				t.Errorf("GetByID id: want 88, got %d", id)
			}
			return sampleCertificate(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: CertificateResourceType,
		NativeID:     "88",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != "" {
		t.Errorf("ErrorCode: want empty, got %q", res.ErrorCode)
	}
	var props CertificateProperties
	if err := json.Unmarshal([]byte(res.Properties), &props); err != nil {
		t.Fatalf("invalid properties JSON: %v", err)
	}
	if props.Name != "cert-1" {
		t.Errorf("Name: want cert-1, got %q", props.Name)
	}
	if props.Fingerprint != "aa:bb:cc" {
		t.Errorf("Fingerprint: want aa:bb:cc, got %q", props.Fingerprint)
	}
	if props.NotValidAfter == "" {
		t.Errorf("NotValidAfter: want non-empty, got empty")
	}
	// Private key must never round-trip into observed state.
	if props.PrivateKey != "" {
		t.Errorf("PrivateKey MUST NOT be present in Read output, got %q", props.PrivateKey)
	}
	// And it must not even appear as a JSON key.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.Properties), &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, present := raw["privateKey"]; present {
		t.Errorf("privateKey key MUST NOT be present in Read output JSON")
	}
}

func TestCertificate_Read_NotFound(t *testing.T) {
	api := fakeAPI{certificate: fakeCertificateClient{
		getByID: func(context.Context, int64) (*hcloud.Certificate, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: CertificateResourceType,
		NativeID:     "88",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ErrorCode)
	}
}

func TestCertificate_Read_InvalidID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: CertificateResourceType,
		NativeID:     "nope",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", res.ErrorCode)
	}
}

// --- Update ----------------------------------------------------------------

func TestCertificate_Update_Success(t *testing.T) {
	var captured hcloud.CertificateUpdateOpts
	api := fakeAPI{certificate: fakeCertificateClient{
		update: func(_ context.Context, c *hcloud.Certificate, opts hcloud.CertificateUpdateOpts) (*hcloud.Certificate, *hcloud.Response, error) {
			captured = opts
			return c, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.Certificate, *hcloud.Response, error) {
			return sampleCertificate(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      CertificateResourceType,
		NativeID:          "88",
		DesiredProperties: json.RawMessage(`{"name":"cert-2","labels":{"team":"infra"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if captured.Name != "cert-2" {
		t.Errorf("Name forwarded: want cert-2, got %q", captured.Name)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae injected, got %q", got)
	}
	// Read-back must not contain the private key.
	var readback CertificateProperties
	if err := json.Unmarshal(pr.ResourceProperties, &readback); err != nil {
		t.Fatalf("invalid readback JSON: %v", err)
	}
	if readback.PrivateKey != "" {
		t.Errorf("PrivateKey MUST NOT appear in update read-back, got %q", readback.PrivateKey)
	}
}

func TestCertificate_Update_InvalidID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      CertificateResourceType,
		NativeID:          "bad",
		DesiredProperties: json.RawMessage(`{"name":"cert-2"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

// --- Delete ----------------------------------------------------------------

func TestCertificate_Delete_Success(t *testing.T) {
	api := fakeAPI{certificate: fakeCertificateClient{
		getByID: func(context.Context, int64) (*hcloud.Certificate, *hcloud.Response, error) {
			return sampleCertificate(), nil, nil
		},
		delete: func(_ context.Context, c *hcloud.Certificate) (*hcloud.Response, error) {
			if c.ID != 88 {
				t.Errorf("delete id: want 88, got %d", c.ID)
			}
			return nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: CertificateResourceType,
		NativeID:     "88",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
}

func TestCertificate_Delete_AlreadyGone(t *testing.T) {
	api := fakeAPI{certificate: fakeCertificateClient{
		getByID: func(context.Context, int64) (*hcloud.Certificate, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: CertificateResourceType,
		NativeID:     "88",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProgressResult.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ProgressResult.ErrorCode)
	}
}

func TestCertificate_Delete_InvalidID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: CertificateResourceType,
		NativeID:     "bad",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

// --- List ------------------------------------------------------------------

func TestCertificate_List_Success(t *testing.T) {
	api := fakeAPI{certificate: fakeCertificateClient{
		all: func(context.Context) ([]*hcloud.Certificate, error) {
			return []*hcloud.Certificate{{ID: 1}, {ID: 2}}, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: CertificateResourceType})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"1", "2"}
	if len(res.NativeIDs) != len(want) {
		t.Fatalf("NativeIDs: want %v, got %v", want, res.NativeIDs)
	}
	for i, id := range want {
		if res.NativeIDs[i] != id {
			t.Errorf("NativeIDs[%d]: want %q, got %q", i, id, res.NativeIDs[i])
		}
	}
}

func TestCertificate_List_EmptyOnError(t *testing.T) {
	api := fakeAPI{certificate: fakeCertificateClient{
		all: func(context.Context) ([]*hcloud.Certificate, error) {
			return nil, errors.New("unavailable")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: CertificateResourceType})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.NativeIDs) != 0 {
		t.Errorf("expected empty list on API error, got %v", res.NativeIDs)
	}
}
