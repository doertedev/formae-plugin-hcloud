// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: HETZNER::Security::SSHKey handler.
//
// Signature notes (verified in hcloud-go v2.43.0):
//   - ISSHKeyClient.Create returns (*SSHKey, *Response, error). Create is
//     synchronous and returns no Action, so the handler reports Success with
//     read-back.
//   - ISSHKeyClient.Delete returns (*Response, error). Delete is synchronous.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// SSHKeyResourceType is the formae resource type for a Hetzner Cloud SSH key.
const SSHKeyResourceType = "HETZNER::Security::SSHKey"

func init() {
	register(SSHKeyResourceType, sshKeyHandler{})
}

// SSHKeyProperties is the desired/observed state of an hcloud SSH key.
type SSHKeyProperties struct {
	Name      string            `json:"name"`
	PublicKey string            `json:"publicKey"`
	Labels    map[string]string `json:"labels,omitempty"`

	// Observed outputs:
	ID          int64  `json:"id,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func parseSSHKeyProperties(data json.RawMessage) (*SSHKeyProperties, error) {
	var p SSHKeyProperties
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid ssh key properties: %w", err)
	}
	if p.Name == "" {
		return nil, errors.New("ssh key properties missing 'name'")
	}
	if p.PublicKey == "" {
		return nil, errors.New("ssh key properties missing 'publicKey'")
	}
	return &p, nil
}

// sshKeyHandler implements resourceHandler for HETZNER::Security::SSHKey.
type sshKeyHandler struct{}

func (sshKeyHandler) create(ctx context.Context, c hcloudAPI, req *resource.CreateRequest) (*resource.CreateResult, error) {
	props, err := parseSSHKeyProperties(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	labels := mergeManagedLabels(props.Labels)

	opts := hcloud.SSHKeyCreateOpts{
		Name:      props.Name,
		PublicKey: props.PublicKey,
		Labels:    labels,
	}

	// ISSHKeyClient.Create is synchronous: no Action to poll.
	key, _, err := c.SSHKey().Create(ctx, opts)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), mapHcloudError(err))}, nil
	}
	b, _ := json.Marshal(sshKeyFrom(key))
	pr := progress(resource.OperationCreate, resource.OperationStatusSuccess, strconv.FormatInt(key.ID, 10), "")
	pr.ResourceProperties = b
	return &resource.CreateResult{ProgressResult: pr}, nil
}

func (sshKeyHandler) read(ctx context.Context, c hcloudAPI, req *resource.ReadRequest) (*resource.ReadResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInvalidRequest}, nil
	}
	key, _, err := c.SSHKey().GetByID(ctx, id)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: mapHcloudError(err)}, nil
	}
	if key == nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeNotFound}, nil
	}
	props := sshKeyFrom(key)
	b, _ := json.Marshal(props)
	return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(b)}, nil
}

// update changes mutable fields (name, labels). The public key is immutable
// after creation.
func (sshKeyHandler) update(ctx context.Context, c hcloudAPI, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	var desired SSHKeyProperties
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("invalid ssh key properties: %v", err), resource.OperationErrorCodeInvalidRequest)}, nil
	}
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}

	opts := hcloud.SSHKeyUpdateOpts{}
	if desired.Name != "" {
		opts.Name = desired.Name
	}
	if desired.Labels != nil {
		opts.Labels = mergeManagedLabels(desired.Labels)
	}
	if opts.Name != "" || opts.Labels != nil {
		if _, _, err := c.SSHKey().Update(ctx, &hcloud.SSHKey{ID: id}, opts); err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
		}
	}
	key, _, err := c.SSHKey().GetByID(ctx, id)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("read-back failed: %v", err), mapHcloudError(err))}, nil
	}
	if key == nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "ssh key not found", resource.OperationErrorCodeNotFound)}, nil
	}
	b, _ := json.Marshal(sshKeyFrom(key))
	pr := progress(resource.OperationUpdate, resource.OperationStatusSuccess, req.NativeID, "")
	pr.ResourceProperties = b
	return &resource.UpdateResult{ProgressResult: pr}, nil
}

func (sshKeyHandler) delete(ctx context.Context, c hcloudAPI, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	key, _, err := c.SSHKey().GetByID(ctx, id)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	if key == nil {
		pr := progress(resource.OperationDelete, resource.OperationStatusFailure, req.NativeID, "")
		pr.ErrorCode = resource.OperationErrorCodeNotFound
		return &resource.DeleteResult{ProgressResult: pr}, nil
	}
	// ISSHKeyClient.Delete is synchronous — no Action to poll.
	if _, err := c.SSHKey().Delete(ctx, key); err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	return &resource.DeleteResult{ProgressResult: progress(
		resource.OperationDelete, resource.OperationStatusSuccess,
		req.NativeID, "",
	)}, nil
}

func (sshKeyHandler) list(ctx context.Context, c hcloudAPI, _ *resource.ListRequest) (*resource.ListResult, error) {
	keys, err := c.SSHKey().All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, strconv.FormatInt(k.ID, 10))
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func sshKeyFrom(k *hcloud.SSHKey) SSHKeyProperties {
	return SSHKeyProperties{
		Name:        k.Name,
		PublicKey:   k.PublicKey,
		Labels:      stripManagedLabel(k.Labels),
		ID:          k.ID,
		Fingerprint: k.Fingerprint,
	}
}
