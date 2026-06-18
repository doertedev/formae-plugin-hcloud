// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// fakeSSHKeyClient implements hcloud.ISSHKeyClient. The five methods the
// suite exercises dispatch to function fields (nil field => zero response);
// the remaining methods are inherited from the nil-embedded interface.
type fakeSSHKeyClient struct {
	hcloud.ISSHKeyClient

	create  func(context.Context, hcloud.SSHKeyCreateOpts) (*hcloud.SSHKey, *hcloud.Response, error)
	getByID func(context.Context, int64) (*hcloud.SSHKey, *hcloud.Response, error)
	update  func(context.Context, *hcloud.SSHKey, hcloud.SSHKeyUpdateOpts) (*hcloud.SSHKey, *hcloud.Response, error)
	delete  func(context.Context, *hcloud.SSHKey) (*hcloud.Response, error)
	all     func(context.Context) ([]*hcloud.SSHKey, error)
}

func (f fakeSSHKeyClient) Create(ctx context.Context, opts hcloud.SSHKeyCreateOpts) (*hcloud.SSHKey, *hcloud.Response, error) {
	if f.create == nil {
		return nil, nil, nil
	}
	return f.create(ctx, opts)
}

func (f fakeSSHKeyClient) GetByID(ctx context.Context, id int64) (*hcloud.SSHKey, *hcloud.Response, error) {
	if f.getByID == nil {
		return nil, nil, nil
	}
	return f.getByID(ctx, id)
}

func (f fakeSSHKeyClient) Update(ctx context.Context, k *hcloud.SSHKey, opts hcloud.SSHKeyUpdateOpts) (*hcloud.SSHKey, *hcloud.Response, error) {
	if f.update == nil {
		return nil, nil, nil
	}
	return f.update(ctx, k, opts)
}

func (f fakeSSHKeyClient) Delete(ctx context.Context, k *hcloud.SSHKey) (*hcloud.Response, error) {
	if f.delete == nil {
		return nil, nil
	}
	return f.delete(ctx, k)
}

func (f fakeSSHKeyClient) All(ctx context.Context) ([]*hcloud.SSHKey, error) {
	if f.all == nil {
		return nil, nil
	}
	return f.all(ctx)
}

var _ hcloud.ISSHKeyClient = fakeSSHKeyClient{}

const validSSHKeyProps = `{"name":"web-key","publicKey":"ssh-ed25519 AAAA fake@host"}`

func sampleSSHKey() *hcloud.SSHKey {
	return &hcloud.SSHKey{
		ID:          66,
		Name:        "web-key",
		PublicKey:   "ssh-ed25519 AAAA fake@host",
		Fingerprint: "aa:bb:cc:dd:ee:ff",
		Labels:      map[string]string{"managed_by": "formae"},
	}
}

// --- Create ----------------------------------------------------------------

func TestSSHKey_Create_Success(t *testing.T) {
	var captured hcloud.SSHKeyCreateOpts
	api := fakeAPI{sshKey: fakeSSHKeyClient{
		create: func(_ context.Context, opts hcloud.SSHKeyCreateOpts) (*hcloud.SSHKey, *hcloud.Response, error) {
			captured = opts
			return sampleSSHKey(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: SSHKeyResourceType,
		Properties:   json.RawMessage(validSSHKeyProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Create is synchronous — expect Success, not InProgress.
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "66" {
		t.Errorf("NativeID: want 66, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae label injected, got %q", got)
	}
	if captured.Name != "web-key" {
		t.Errorf("expected name forwarded, got %q", captured.Name)
	}
	if captured.PublicKey != "ssh-ed25519 AAAA fake@host" {
		t.Errorf("expected publicKey forwarded, got %q", captured.PublicKey)
	}
}

func TestSSHKey_Create_MissingFields(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing-name", `{"publicKey":"ssh-ed25519 AAAA"}`},
		{"missing-publicKey", `{"name":"k"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPluginWithClient(fakeAPI{})
			res, err := p.Create(context.Background(), &resource.CreateRequest{
				ResourceType: SSHKeyResourceType,
				Properties:   json.RawMessage(c.json),
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
				t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
			}
			if res.ProgressResult.OperationStatus != resource.OperationStatusFailure {
				t.Errorf("Status: want Failure, got %q", res.ProgressResult.OperationStatus)
			}
		})
	}
}

func TestSSHKey_Create_APIError(t *testing.T) {
	api := fakeAPI{sshKey: fakeSSHKeyClient{
		create: func(context.Context, hcloud.SSHKeyCreateOpts) (*hcloud.SSHKey, *hcloud.Response, error) {
			return nil, nil, errors.New("rate limited")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: SSHKeyResourceType,
		Properties:   json.RawMessage(validSSHKeyProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", code)
	}
	if msg := res.ProgressResult.StatusMessage; msg != "rate limited" {
		t.Errorf("StatusMessage: want %q, got %q", "rate limited", msg)
	}
}

// --- Read ------------------------------------------------------------------

func TestSSHKey_Read_Existing(t *testing.T) {
	api := fakeAPI{sshKey: fakeSSHKeyClient{
		getByID: func(_ context.Context, id int64) (*hcloud.SSHKey, *hcloud.Response, error) {
			if id != 66 {
				t.Errorf("GetByID id: want 66, got %d", id)
			}
			return sampleSSHKey(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: SSHKeyResourceType,
		NativeID:     "66",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != "" {
		t.Errorf("ErrorCode: want empty, got %q", res.ErrorCode)
	}
	var props SSHKeyProperties
	if err := json.Unmarshal([]byte(res.Properties), &props); err != nil {
		t.Fatalf("invalid properties JSON: %v", err)
	}
	if props.Name != "web-key" {
		t.Errorf("Name: want web-key, got %q", props.Name)
	}
	if props.PublicKey != "ssh-ed25519 AAAA fake@host" {
		t.Errorf("PublicKey: want ssh-ed25519 AAAA fake@host, got %q", props.PublicKey)
	}
	if props.Fingerprint != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("Fingerprint: want aa:bb:cc:dd:ee:ff, got %q", props.Fingerprint)
	}
}

func TestSSHKey_Read_NotFound(t *testing.T) {
	api := fakeAPI{sshKey: fakeSSHKeyClient{
		getByID: func(context.Context, int64) (*hcloud.SSHKey, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: SSHKeyResourceType,
		NativeID:     "66",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ErrorCode)
	}
}

func TestSSHKey_Read_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: SSHKeyResourceType,
		NativeID:     "not-a-number",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", res.ErrorCode)
	}
}

func TestSSHKey_Read_APIError(t *testing.T) {
	api := fakeAPI{sshKey: fakeSSHKeyClient{
		getByID: func(context.Context, int64) (*hcloud.SSHKey, *hcloud.Response, error) {
			return nil, nil, errors.New("boom")
		},
	}}
	p := newPluginWithClient(api)
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: SSHKeyResourceType,
		NativeID:     "66",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", res.ErrorCode)
	}
}

// --- Update ----------------------------------------------------------------

func TestSSHKey_Update_NameAndLabels(t *testing.T) {
	var captured hcloud.SSHKeyUpdateOpts
	api := fakeAPI{sshKey: fakeSSHKeyClient{
		update: func(_ context.Context, k *hcloud.SSHKey, opts hcloud.SSHKeyUpdateOpts) (*hcloud.SSHKey, *hcloud.Response, error) {
			captured = opts
			return k, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.SSHKey, *hcloud.Response, error) {
			return sampleSSHKey(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      SSHKeyResourceType,
		NativeID:          "66",
		DesiredProperties: json.RawMessage(`{"name":"web-key-2","publicKey":"ssh-ed25519 AAAA","labels":{"team":"infra"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "66" {
		t.Errorf("NativeID: want 66, got %q", pr.NativeID)
	}
	if captured.Name != "web-key-2" {
		t.Errorf("update not forwarded: want web-key-2, got %q", captured.Name)
	}
	if captured.Labels == nil {
		t.Fatal("expected Labels to be set in update opts")
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae label injected, got %q", got)
	}
	if got := captured.Labels["team"]; got != "infra" {
		t.Errorf("expected user label team=infra preserved, got %q", got)
	}
	var props SSHKeyProperties
	if err := json.Unmarshal(pr.ResourceProperties, &props); err != nil {
		t.Fatalf("invalid readback properties: %v", err)
	}
}

func TestSSHKey_Update_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      SSHKeyResourceType,
		NativeID:          "oops",
		DesiredProperties: json.RawMessage(validSSHKeyProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

// --- Delete ----------------------------------------------------------------

func TestSSHKey_Delete_Success(t *testing.T) {
	api := fakeAPI{sshKey: fakeSSHKeyClient{
		getByID: func(context.Context, int64) (*hcloud.SSHKey, *hcloud.Response, error) {
			return sampleSSHKey(), nil, nil
		},
		delete: func(_ context.Context, k *hcloud.SSHKey) (*hcloud.Response, error) {
			if k.ID != 66 {
				t.Errorf("delete id: want 66, got %d", k.ID)
			}
			return nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: SSHKeyResourceType,
		NativeID:     "66",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Delete is synchronous — expect Success, not InProgress.
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "66" {
		t.Errorf("NativeID: want 66, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
}

func TestSSHKey_Delete_AlreadyGone(t *testing.T) {
	api := fakeAPI{sshKey: fakeSSHKeyClient{
		getByID: func(context.Context, int64) (*hcloud.SSHKey, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: SSHKeyResourceType,
		NativeID:     "66",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", pr.ErrorCode)
	}
	if pr.OperationStatus != resource.OperationStatusFailure {
		t.Errorf("Status: want Failure, got %q", pr.OperationStatus)
	}
}

func TestSSHKey_Delete_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: SSHKeyResourceType,
		NativeID:     "nope",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

// --- List ------------------------------------------------------------------

func TestSSHKey_List_Success(t *testing.T) {
	api := fakeAPI{sshKey: fakeSSHKeyClient{
		all: func(context.Context) ([]*hcloud.SSHKey, error) {
			return []*hcloud.SSHKey{
				{ID: 10},
				{ID: 20},
			}, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: SSHKeyResourceType})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"10", "20"}
	if len(res.NativeIDs) != len(want) {
		t.Fatalf("NativeIDs: want %v, got %v", want, res.NativeIDs)
	}
	for i, id := range want {
		if res.NativeIDs[i] != id {
			t.Errorf("NativeIDs[%d]: want %q, got %q", i, id, res.NativeIDs[i])
		}
	}
}

func TestSSHKey_List_PropagatesError(t *testing.T) {
	// Discovery errors must be surfaced, not hidden as empty lists —
	// otherwise an invalid token or a 5xx during discovery looks like "no
	// resources" to the drift workflow.
	api := fakeAPI{sshKey: fakeSSHKeyClient{
		all: func(context.Context) ([]*hcloud.SSHKey, error) {
			return nil, errors.New("unavailable")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: SSHKeyResourceType})
	if err == nil {
		t.Fatalf("expected error from List on API failure, got nil (result=%+v)", res)
	}
	if res != nil {
		t.Errorf("expected nil result on error, got %+v", res)
	}
}
