// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: HETZNER::Security::Certificate handler.
//
// Supports the *uploaded* certificate type. Create and Delete are both
// synchronous in hcloud-go (no Action is returned), so the handler reports
// Success directly. The PEM private key is treated as write-only: it is
// accepted on Create, never echoed in Read/Update output, and not present
// on the hcloud.Certificate struct the API returns.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// CertificateResourceType is the formae resource type for a Hetzner Cloud
// certificate.
const CertificateResourceType = "HETZNER::Security::Certificate"

func init() {
	register(CertificateResourceType, certificateHandler{})
}

// CertificateProperties is the desired/observed state of an hcloud
// certificate. PrivateKey is write-only: it is consumed by Create but never
// populated by certificateFrom, so it cannot round-trip into observed state.
type CertificateProperties struct {
	Name        string `json:"name"`
	Certificate string `json:"certificate,omitempty"`

	// PrivateKey is write-only. The json tag keeps the field name stable for
	// the create path; the field is never populated by the read mapper.
	PrivateKey string `json:"privateKey,omitempty"`

	Labels map[string]string `json:"labels,omitempty"`

	// Observed outputs (read-only):
	ID             int64  `json:"id,omitempty"`
	Fingerprint    string `json:"fingerprint,omitempty"`
	NotValidAfter  string `json:"notValidAfter,omitempty"`
	NotValidBefore string `json:"notValidBefore,omitempty"`
}

func parseCertificateProperties(data json.RawMessage) (*CertificateProperties, error) {
	var p CertificateProperties
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid certificate properties: %w", err)
	}
	if p.Name == "" {
		return nil, errors.New("certificate properties missing 'name'")
	}
	if p.Certificate == "" {
		return nil, errors.New("certificate properties missing 'certificate'")
	}
	if p.PrivateKey == "" {
		return nil, errors.New("certificate properties missing 'privateKey'")
	}
	return &p, nil
}

// certificateHandler implements resourceHandler for
// HETZNER::Security::Certificate.
type certificateHandler struct{}

func (certificateHandler) create(ctx context.Context, c hcloudAPI, req *resource.CreateRequest) (*resource.CreateResult, error) {
	props, err := parseCertificateProperties(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	labels := mergeManagedLabels(props.Labels)

	opts := hcloud.CertificateCreateOpts{
		Name:        props.Name,
		Type:        hcloud.CertificateTypeUploaded,
		Certificate: props.Certificate,
		PrivateKey:  props.PrivateKey,
		Labels:      labels,
	}

	// ICertificateClient.Create for uploaded certs is synchronous: it returns
	// the new *Certificate directly (no Action).
	cert, _, err := c.Certificate().Create(ctx, opts)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), mapHcloudError(err))}, nil
	}
	b, _ := json.Marshal(certificateFrom(cert))
	pr := progress(resource.OperationCreate, resource.OperationStatusSuccess, strconv.FormatInt(cert.ID, 10), "")
	pr.ResourceProperties = b
	return &resource.CreateResult{ProgressResult: pr}, nil
}

func (certificateHandler) read(ctx context.Context, c hcloudAPI, req *resource.ReadRequest) (*resource.ReadResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInvalidRequest}, nil
	}
	cert, _, err := c.Certificate().GetByID(ctx, id)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: mapHcloudError(err)}, nil
	}
	if cert == nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeNotFound}, nil
	}
	props := certificateFrom(cert)
	b, _ := json.Marshal(props)
	return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(b)}, nil
}

// update changes mutable fields (name, labels). Certificate, PrivateKey, and
// the validity/fingerprint outputs are immutable. The desired payload is
// parsed leniently (the create-only certificate/privateKey fields are
// typically absent from an update payload and must not trigger validation).
func (certificateHandler) update(ctx context.Context, c hcloudAPI, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	var desired CertificateProperties
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("invalid certificate properties: %v", err), resource.OperationErrorCodeInvalidRequest)}, nil
	}
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}

	opts := hcloud.CertificateUpdateOpts{}
	if desired.Name != "" {
		opts.Name = desired.Name
	}
	if desired.Labels != nil {
		opts.Labels = mergeManagedLabels(desired.Labels)
	}
	if opts.Name != "" || opts.Labels != nil {
		if _, _, err := c.Certificate().Update(ctx, &hcloud.Certificate{ID: id}, opts); err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
		}
	}
	cert, _, err := c.Certificate().GetByID(ctx, id)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("read-back failed: %v", err), mapHcloudError(err))}, nil
	}
	if cert == nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "certificate not found", resource.OperationErrorCodeNotFound)}, nil
	}
	b, _ := json.Marshal(certificateFrom(cert))
	pr := progress(resource.OperationUpdate, resource.OperationStatusSuccess, req.NativeID, "")
	pr.ResourceProperties = b
	return &resource.UpdateResult{ProgressResult: pr}, nil
}

func (certificateHandler) delete(ctx context.Context, c hcloudAPI, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	cert, _, err := c.Certificate().GetByID(ctx, id)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	if cert == nil {
		pr := progress(resource.OperationDelete, resource.OperationStatusFailure, req.NativeID, "")
		pr.ErrorCode = resource.OperationErrorCodeNotFound
		return &resource.DeleteResult{ProgressResult: pr}, nil
	}
	// ICertificateClient.Delete is synchronous — no Action returned.
	if _, err := c.Certificate().Delete(ctx, cert); err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	return &resource.DeleteResult{ProgressResult: progress(
		resource.OperationDelete, resource.OperationStatusSuccess,
		req.NativeID, "",
	)}, nil
}

func (certificateHandler) list(ctx context.Context, c hcloudAPI, _ *resource.ListRequest) (*resource.ListResult, error) {
	certs, err := c.Certificate().All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(certs))
	for _, cert := range certs {
		ids = append(ids, strconv.FormatInt(cert.ID, 10))
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

// certificateFrom maps an hcloud.Certificate to its observed-state
// properties. The PEM private key is intentionally omitted — it is
// write-only and not present on the API's representation anyway.
//
// The certificate PEM is normalized with TrimSpace: hcloud echoes the
// uploaded PEM with a trailing newline, while the desired-state payload
// is trimmed by the schema fixture. Without normalization the read-back
// never byte-matches the input, which fails conformance verification.
func certificateFrom(c *hcloud.Certificate) CertificateProperties {
	props := CertificateProperties{
		Name:        c.Name,
		Labels:      stripManagedLabel(c.Labels),
		ID:          c.ID,
		Certificate: strings.TrimSpace(c.Certificate),
		Fingerprint: c.Fingerprint,
	}
	if !c.NotValidAfter.IsZero() {
		props.NotValidAfter = c.NotValidAfter.Format("2006-01-02T15:04:05Z07:00")
	}
	if !c.NotValidBefore.IsZero() {
		props.NotValidBefore = c.NotValidBefore.Format("2006-01-02T15:04:05Z07:00")
	}
	return props
}
