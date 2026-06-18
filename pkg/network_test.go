// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// fakeNetworkClient implements hcloud.INetworkClient. The five methods the
// suite exercises dispatch to function fields (nil field => zero response);
// the remaining methods are inherited from the nil-embedded interface.
type fakeNetworkClient struct {
	hcloud.INetworkClient

	create  func(context.Context, hcloud.NetworkCreateOpts) (*hcloud.Network, *hcloud.Response, error)
	getByID func(context.Context, int64) (*hcloud.Network, *hcloud.Response, error)
	update  func(context.Context, *hcloud.Network, hcloud.NetworkUpdateOpts) (*hcloud.Network, *hcloud.Response, error)
	delete  func(context.Context, *hcloud.Network) (*hcloud.Response, error)
	all     func(context.Context) ([]*hcloud.Network, error)
}

func (f fakeNetworkClient) Create(ctx context.Context, opts hcloud.NetworkCreateOpts) (*hcloud.Network, *hcloud.Response, error) {
	if f.create == nil {
		return nil, nil, nil
	}
	return f.create(ctx, opts)
}

func (f fakeNetworkClient) GetByID(ctx context.Context, id int64) (*hcloud.Network, *hcloud.Response, error) {
	if f.getByID == nil {
		return nil, nil, nil
	}
	return f.getByID(ctx, id)
}

func (f fakeNetworkClient) Update(ctx context.Context, n *hcloud.Network, opts hcloud.NetworkUpdateOpts) (*hcloud.Network, *hcloud.Response, error) {
	if f.update == nil {
		return nil, nil, nil
	}
	return f.update(ctx, n, opts)
}

func (f fakeNetworkClient) Delete(ctx context.Context, n *hcloud.Network) (*hcloud.Response, error) {
	if f.delete == nil {
		return nil, nil
	}
	return f.delete(ctx, n)
}

func (f fakeNetworkClient) All(ctx context.Context) ([]*hcloud.Network, error) {
	if f.all == nil {
		return nil, nil
	}
	return f.all(ctx)
}

var _ hcloud.INetworkClient = fakeNetworkClient{}

const validNetworkProps = `{"name":"web-net","ipRange":"10.0.0.0/16"}`

func sampleNetwork() *hcloud.Network {
	_, ipNet, _ := net.ParseCIDR("10.0.0.0/16")
	return &hcloud.Network{
		ID:      55,
		Name:    "web-net",
		IPRange: ipNet,
		Labels:  map[string]string{"managed_by": "formae"},
	}
}

// --- Create ----------------------------------------------------------------

func TestNetwork_Create_Success(t *testing.T) {
	var captured hcloud.NetworkCreateOpts
	api := fakeAPI{network: fakeNetworkClient{
		create: func(_ context.Context, opts hcloud.NetworkCreateOpts) (*hcloud.Network, *hcloud.Response, error) {
			captured = opts
			return sampleNetwork(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: NetworkResourceType,
		Properties:   json.RawMessage(validNetworkProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Create is synchronous — expect Success, not InProgress.
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "55" {
		t.Errorf("NativeID: want 55, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae label injected, got %q", got)
	}
	if captured.Name != "web-net" {
		t.Errorf("expected name forwarded, got %q", captured.Name)
	}
	if captured.IPRange == nil || captured.IPRange.String() != "10.0.0.0/16" {
		t.Errorf("expected ipRange forwarded, got %v", captured.IPRange)
	}
}

func TestNetwork_Create_MissingFields(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing-name", `{"ipRange":"10.0.0.0/16"}`},
		{"missing-ipRange", `{"name":"web"}`},
		{"invalid-cidr", `{"name":"web","ipRange":"not-a-cidr"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPluginWithClient(fakeAPI{})
			res, err := p.Create(context.Background(), &resource.CreateRequest{
				ResourceType: NetworkResourceType,
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

func TestNetwork_Create_APIError(t *testing.T) {
	api := fakeAPI{network: fakeNetworkClient{
		create: func(context.Context, hcloud.NetworkCreateOpts) (*hcloud.Network, *hcloud.Response, error) {
			return nil, nil, errors.New("rate limited")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: NetworkResourceType,
		Properties:   json.RawMessage(validNetworkProps),
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

func TestNetwork_Read_Existing(t *testing.T) {
	api := fakeAPI{network: fakeNetworkClient{
		getByID: func(_ context.Context, id int64) (*hcloud.Network, *hcloud.Response, error) {
			if id != 55 {
				t.Errorf("GetByID id: want 55, got %d", id)
			}
			return sampleNetwork(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: NetworkResourceType,
		NativeID:     "55",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != "" {
		t.Errorf("ErrorCode: want empty, got %q", res.ErrorCode)
	}
	var props NetworkProperties
	if err := json.Unmarshal([]byte(res.Properties), &props); err != nil {
		t.Fatalf("invalid properties JSON: %v", err)
	}
	if props.Name != "web-net" {
		t.Errorf("Name: want web-net, got %q", props.Name)
	}
	if props.IPRange != "10.0.0.0/16" {
		t.Errorf("IPRange: want 10.0.0.0/16, got %q", props.IPRange)
	}
}

func TestNetwork_Read_NotFound(t *testing.T) {
	api := fakeAPI{network: fakeNetworkClient{
		getByID: func(context.Context, int64) (*hcloud.Network, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: NetworkResourceType,
		NativeID:     "55",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ErrorCode)
	}
}

func TestNetwork_Read_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: NetworkResourceType,
		NativeID:     "not-a-number",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", res.ErrorCode)
	}
}

func TestNetwork_Read_APIError(t *testing.T) {
	api := fakeAPI{network: fakeNetworkClient{
		getByID: func(context.Context, int64) (*hcloud.Network, *hcloud.Response, error) {
			return nil, nil, errors.New("boom")
		},
	}}
	p := newPluginWithClient(api)
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: NetworkResourceType,
		NativeID:     "55",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", res.ErrorCode)
	}
}

// --- Update ----------------------------------------------------------------

func TestNetwork_Update_NameAndLabels(t *testing.T) {
	var captured hcloud.NetworkUpdateOpts
	api := fakeAPI{network: fakeNetworkClient{
		update: func(_ context.Context, n *hcloud.Network, opts hcloud.NetworkUpdateOpts) (*hcloud.Network, *hcloud.Response, error) {
			captured = opts
			return n, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.Network, *hcloud.Response, error) {
			return sampleNetwork(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      NetworkResourceType,
		NativeID:          "55",
		DesiredProperties: json.RawMessage(`{"name":"web-net-2","ipRange":"10.0.0.0/16","labels":{"team":"infra"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "55" {
		t.Errorf("NativeID: want 55, got %q", pr.NativeID)
	}
	if captured.Name != "web-net-2" {
		t.Errorf("update not forwarded: want web-net-2, got %q", captured.Name)
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
	var props NetworkProperties
	if err := json.Unmarshal(pr.ResourceProperties, &props); err != nil {
		t.Fatalf("invalid readback properties: %v", err)
	}
}

func TestNetwork_Update_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      NetworkResourceType,
		NativeID:          "oops",
		DesiredProperties: json.RawMessage(validNetworkProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

// --- Delete ----------------------------------------------------------------

func TestNetwork_Delete_Success(t *testing.T) {
	api := fakeAPI{network: fakeNetworkClient{
		getByID: func(context.Context, int64) (*hcloud.Network, *hcloud.Response, error) {
			return sampleNetwork(), nil, nil
		},
		delete: func(_ context.Context, n *hcloud.Network) (*hcloud.Response, error) {
			if n.ID != 55 {
				t.Errorf("delete id: want 55, got %d", n.ID)
			}
			return nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: NetworkResourceType,
		NativeID:     "55",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Delete is synchronous — expect Success, not InProgress.
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "55" {
		t.Errorf("NativeID: want 55, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
}

func TestNetwork_Delete_AlreadyGone(t *testing.T) {
	api := fakeAPI{network: fakeNetworkClient{
		getByID: func(context.Context, int64) (*hcloud.Network, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: NetworkResourceType,
		NativeID:     "55",
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

func TestNetwork_Delete_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: NetworkResourceType,
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

func TestNetwork_List_Success(t *testing.T) {
	api := fakeAPI{network: fakeNetworkClient{
		all: func(context.Context) ([]*hcloud.Network, error) {
			return []*hcloud.Network{
				{ID: 10},
				{ID: 20},
			}, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: NetworkResourceType})
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

func TestNetwork_List_PropagatesError(t *testing.T) {
	// Discovery errors must be surfaced, not hidden as empty lists —
	// otherwise an invalid token or a 5xx during discovery looks like "no
	// resources" to the drift workflow.
	api := fakeAPI{network: fakeNetworkClient{
		all: func(context.Context) ([]*hcloud.Network, error) {
			return nil, errors.New("unavailable")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: NetworkResourceType})
	if err == nil {
		t.Fatalf("expected error from List on API failure, got nil (result=%+v)", res)
	}
	if res != nil {
		t.Errorf("expected nil result on error, got %+v", res)
	}
}
