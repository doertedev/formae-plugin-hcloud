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

// fakeLoadBalancerClient implements hcloud.ILoadBalancerClient via the
// nil-embedded interface trick used by fakeServerClient. The methods this
// suite exercises dispatch to function fields.
type fakeLoadBalancerClient struct {
	hcloud.ILoadBalancerClient

	create  func(context.Context, hcloud.LoadBalancerCreateOpts) (hcloud.LoadBalancerCreateResult, *hcloud.Response, error)
	getByID func(context.Context, int64) (*hcloud.LoadBalancer, *hcloud.Response, error)
	update  func(context.Context, *hcloud.LoadBalancer, hcloud.LoadBalancerUpdateOpts) (*hcloud.LoadBalancer, *hcloud.Response, error)
	delete  func(context.Context, *hcloud.LoadBalancer) (*hcloud.Response, error)
	all     func(context.Context) ([]*hcloud.LoadBalancer, error)
}

func (f fakeLoadBalancerClient) Create(ctx context.Context, opts hcloud.LoadBalancerCreateOpts) (hcloud.LoadBalancerCreateResult, *hcloud.Response, error) {
	if f.create == nil {
		return hcloud.LoadBalancerCreateResult{}, nil, nil
	}
	return f.create(ctx, opts)
}

func (f fakeLoadBalancerClient) GetByID(ctx context.Context, id int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
	if f.getByID == nil {
		return nil, nil, nil
	}
	return f.getByID(ctx, id)
}

func (f fakeLoadBalancerClient) Update(ctx context.Context, lb *hcloud.LoadBalancer, opts hcloud.LoadBalancerUpdateOpts) (*hcloud.LoadBalancer, *hcloud.Response, error) {
	if f.update == nil {
		return nil, nil, nil
	}
	return f.update(ctx, lb, opts)
}

func (f fakeLoadBalancerClient) Delete(ctx context.Context, lb *hcloud.LoadBalancer) (*hcloud.Response, error) {
	if f.delete == nil {
		return nil, nil
	}
	return f.delete(ctx, lb)
}

func (f fakeLoadBalancerClient) All(ctx context.Context) ([]*hcloud.LoadBalancer, error) {
	if f.all == nil {
		return nil, nil
	}
	return f.all(ctx)
}

var _ hcloud.ILoadBalancerClient = fakeLoadBalancerClient{}

const validLoadBalancerProps = `{"name":"lb-1","loadBalancerType":"lb-example","location":"nbg1","algorithm":"round_robin"}`

func sampleLoadBalancer() *hcloud.LoadBalancer {
	return &hcloud.LoadBalancer{
		ID:               77,
		Name:             "lb-1",
		LoadBalancerType: &hcloud.LoadBalancerType{Name: "lb-example"},
		Location:         &hcloud.Location{Name: "nbg1"},
		Algorithm:        hcloud.LoadBalancerAlgorithm{Type: hcloud.LoadBalancerAlgorithmTypeRoundRobin},
		Labels:           map[string]string{"managed_by": "formae"},
		PublicNet: hcloud.LoadBalancerPublicNet{
			IPv4: hcloud.LoadBalancerPublicNetIPv4{IP: net.ParseIP("203.0.113.20")},
			IPv6: hcloud.LoadBalancerPublicNetIPv6{IP: net.ParseIP("2a01:db8::1")},
		},
	}
}

// --- Create ----------------------------------------------------------------

func TestLoadBalancer_Create_Success(t *testing.T) {
	var captured hcloud.LoadBalancerCreateOpts
	api := fakeAPI{loadBalancer: fakeLoadBalancerClient{
		create: func(_ context.Context, opts hcloud.LoadBalancerCreateOpts) (hcloud.LoadBalancerCreateResult, *hcloud.Response, error) {
			captured = opts
			return hcloud.LoadBalancerCreateResult{
				LoadBalancer: &hcloud.LoadBalancer{ID: 77},
				Action:       &hcloud.Action{ID: 200},
			}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: LoadBalancerResourceType,
		Properties:   json.RawMessage(validLoadBalancerProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusInProgress {
		t.Errorf("status: want InProgress, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "77" {
		t.Errorf("NativeID: want 77, got %q", pr.NativeID)
	}
	if pr.RequestID != "200" {
		t.Errorf("RequestID: want 200, got %q", pr.RequestID)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae injected, got %q", got)
	}
	if captured.Name != "lb-1" {
		t.Errorf("Name forwarded: want lb-1, got %q", captured.Name)
	}
	if captured.LoadBalancerType == nil || captured.LoadBalancerType.Name != "lb-example" {
		t.Errorf("LoadBalancerType.Name: want lb-example, got %+v", captured.LoadBalancerType)
	}
	if captured.Location == nil || captured.Location.Name != "nbg1" {
		t.Errorf("Location.Name: want nbg1, got %+v", captured.Location)
	}
	if captured.Algorithm == nil || captured.Algorithm.Type != hcloud.LoadBalancerAlgorithmTypeRoundRobin {
		t.Errorf("Algorithm.Type: want round_robin, got %+v", captured.Algorithm)
	}
}

func TestLoadBalancer_Create_InvalidRequest(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing-name", `{"loadBalancerType":"lb-example"}`},
		{"missing-loadBalancerType", `{"name":"lb-1"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPluginWithClient(fakeAPI{})
			res, err := p.Create(context.Background(), &resource.CreateRequest{
				ResourceType: LoadBalancerResourceType,
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

func TestLoadBalancer_Create_APIError(t *testing.T) {
	api := fakeAPI{loadBalancer: fakeLoadBalancerClient{
		create: func(context.Context, hcloud.LoadBalancerCreateOpts) (hcloud.LoadBalancerCreateResult, *hcloud.Response, error) {
			return hcloud.LoadBalancerCreateResult{}, nil, errors.New("rate limited")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: LoadBalancerResourceType,
		Properties:   json.RawMessage(validLoadBalancerProps),
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

func TestLoadBalancer_Read_Existing(t *testing.T) {
	api := fakeAPI{loadBalancer: fakeLoadBalancerClient{
		getByID: func(_ context.Context, id int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
			if id != 77 {
				t.Errorf("GetByID id: want 77, got %d", id)
			}
			return sampleLoadBalancer(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: LoadBalancerResourceType,
		NativeID:     "77",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != "" {
		t.Errorf("ErrorCode: want empty, got %q", res.ErrorCode)
	}
	var props LoadBalancerProperties
	if err := json.Unmarshal([]byte(res.Properties), &props); err != nil {
		t.Fatalf("invalid properties JSON: %v", err)
	}
	if props.Name != "lb-1" {
		t.Errorf("Name: want lb-1, got %q", props.Name)
	}
	if props.LoadBalancerType != "lb-example" {
		t.Errorf("LoadBalancerType: want lb-example, got %q", props.LoadBalancerType)
	}
	if props.IPv4 != "203.0.113.20" {
		t.Errorf("IPv4: want 203.0.113.20, got %q", props.IPv4)
	}
}

func TestLoadBalancer_Read_NotFound(t *testing.T) {
	api := fakeAPI{loadBalancer: fakeLoadBalancerClient{
		getByID: func(context.Context, int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: LoadBalancerResourceType,
		NativeID:     "77",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ErrorCode)
	}
}

func TestLoadBalancer_Read_InvalidID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: LoadBalancerResourceType,
		NativeID:     "not-a-number",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", res.ErrorCode)
	}
}

// --- Update ----------------------------------------------------------------

func TestLoadBalancer_Update_Success(t *testing.T) {
	var captured hcloud.LoadBalancerUpdateOpts
	api := fakeAPI{loadBalancer: fakeLoadBalancerClient{
		update: func(_ context.Context, lb *hcloud.LoadBalancer, opts hcloud.LoadBalancerUpdateOpts) (*hcloud.LoadBalancer, *hcloud.Response, error) {
			captured = opts
			return lb, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
			return sampleLoadBalancer(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      LoadBalancerResourceType,
		NativeID:          "77",
		DesiredProperties: json.RawMessage(`{"name":"lb-2","loadBalancerType":"lb-example","labels":{"team":"infra"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if captured.Name != "lb-2" {
		t.Errorf("Name forwarded: want lb-2, got %q", captured.Name)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae injected, got %q", got)
	}
	if got := captured.Labels["team"]; got != "infra" {
		t.Errorf("expected user label team=infra preserved, got %q", got)
	}
	var props LoadBalancerProperties
	if err := json.Unmarshal(pr.ResourceProperties, &props); err != nil {
		t.Fatalf("invalid readback properties: %v", err)
	}
	if props.IPv4 != "203.0.113.20" {
		t.Errorf("readback IPv4: want 203.0.113.20, got %q", props.IPv4)
	}
}

func TestLoadBalancer_Update_InvalidID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      LoadBalancerResourceType,
		NativeID:          "bad",
		DesiredProperties: json.RawMessage(validLoadBalancerProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

// --- Delete ----------------------------------------------------------------

func TestLoadBalancer_Delete_Success(t *testing.T) {
	api := fakeAPI{loadBalancer: fakeLoadBalancerClient{
		getByID: func(context.Context, int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
			return sampleLoadBalancer(), nil, nil
		},
		delete: func(_ context.Context, lb *hcloud.LoadBalancer) (*hcloud.Response, error) {
			if lb.ID != 77 {
				t.Errorf("delete id: want 77, got %d", lb.ID)
			}
			return nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: LoadBalancerResourceType,
		NativeID:     "77",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// LoadBalancer Delete is synchronous (no Action returned by hcloud-go).
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "77" {
		t.Errorf("NativeID: want 77, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
}

func TestLoadBalancer_Delete_AlreadyGone(t *testing.T) {
	api := fakeAPI{loadBalancer: fakeLoadBalancerClient{
		getByID: func(context.Context, int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: LoadBalancerResourceType,
		NativeID:     "77",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", pr.ErrorCode)
	}
}

func TestLoadBalancer_Delete_InvalidID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: LoadBalancerResourceType,
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

func TestLoadBalancer_List_Success(t *testing.T) {
	api := fakeAPI{loadBalancer: fakeLoadBalancerClient{
		all: func(context.Context) ([]*hcloud.LoadBalancer, error) {
			return []*hcloud.LoadBalancer{{ID: 1}, {ID: 2}}, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: LoadBalancerResourceType})
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

func TestLoadBalancer_List_EmptyOnError(t *testing.T) {
	api := fakeAPI{loadBalancer: fakeLoadBalancerClient{
		all: func(context.Context) ([]*hcloud.LoadBalancer, error) {
			return nil, errors.New("unavailable")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: LoadBalancerResourceType})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.NativeIDs) != 0 {
		t.Errorf("expected empty list on API error, got %v", res.NativeIDs)
	}
}
