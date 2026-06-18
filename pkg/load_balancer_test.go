// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// fakeLoadBalancerClient implements hcloud.ILoadBalancerClient via the
// nil-embedded interface trick used by fakeServerClient. The methods this
// suite exercises dispatch to function fields.
type fakeLoadBalancerClient struct {
	hcloud.ILoadBalancerClient

	create                    func(context.Context, hcloud.LoadBalancerCreateOpts) (hcloud.LoadBalancerCreateResult, *hcloud.Response, error)
	getByID                   func(context.Context, int64) (*hcloud.LoadBalancer, *hcloud.Response, error)
	update                    func(context.Context, *hcloud.LoadBalancer, hcloud.LoadBalancerUpdateOpts) (*hcloud.LoadBalancer, *hcloud.Response, error)
	delete                    func(context.Context, *hcloud.LoadBalancer) (*hcloud.Response, error)
	all                       func(context.Context) ([]*hcloud.LoadBalancer, error)
	addService                func(context.Context, *hcloud.LoadBalancer, hcloud.LoadBalancerAddServiceOpts) (*hcloud.Action, *hcloud.Response, error)
	updateService             func(context.Context, *hcloud.LoadBalancer, int, hcloud.LoadBalancerUpdateServiceOpts) (*hcloud.Action, *hcloud.Response, error)
	deleteService             func(context.Context, *hcloud.LoadBalancer, int) (*hcloud.Action, *hcloud.Response, error)
	addServerTarget           func(context.Context, *hcloud.LoadBalancer, hcloud.LoadBalancerAddServerTargetOpts) (*hcloud.Action, *hcloud.Response, error)
	addLabelSelectorTarget    func(context.Context, *hcloud.LoadBalancer, hcloud.LoadBalancerAddLabelSelectorTargetOpts) (*hcloud.Action, *hcloud.Response, error)
	addIPTarget               func(context.Context, *hcloud.LoadBalancer, hcloud.LoadBalancerAddIPTargetOpts) (*hcloud.Action, *hcloud.Response, error)
	removeServerTarget        func(context.Context, *hcloud.LoadBalancer, *hcloud.Server) (*hcloud.Action, *hcloud.Response, error)
	removeLabelSelectorTarget func(context.Context, *hcloud.LoadBalancer, string) (*hcloud.Action, *hcloud.Response, error)
	removeIPTarget            func(context.Context, *hcloud.LoadBalancer, net.IP) (*hcloud.Action, *hcloud.Response, error)
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

func (f fakeLoadBalancerClient) AddService(ctx context.Context, lb *hcloud.LoadBalancer, opts hcloud.LoadBalancerAddServiceOpts) (*hcloud.Action, *hcloud.Response, error) {
	if f.addService == nil {
		return nil, nil, nil
	}
	return f.addService(ctx, lb, opts)
}

func (f fakeLoadBalancerClient) UpdateService(ctx context.Context, lb *hcloud.LoadBalancer, listenPort int, opts hcloud.LoadBalancerUpdateServiceOpts) (*hcloud.Action, *hcloud.Response, error) {
	if f.updateService == nil {
		return nil, nil, nil
	}
	return f.updateService(ctx, lb, listenPort, opts)
}

func (f fakeLoadBalancerClient) DeleteService(ctx context.Context, lb *hcloud.LoadBalancer, listenPort int) (*hcloud.Action, *hcloud.Response, error) {
	if f.deleteService == nil {
		return nil, nil, nil
	}
	return f.deleteService(ctx, lb, listenPort)
}

func (f fakeLoadBalancerClient) AddServerTarget(ctx context.Context, lb *hcloud.LoadBalancer, opts hcloud.LoadBalancerAddServerTargetOpts) (*hcloud.Action, *hcloud.Response, error) {
	if f.addServerTarget == nil {
		return nil, nil, nil
	}
	return f.addServerTarget(ctx, lb, opts)
}

func (f fakeLoadBalancerClient) AddLabelSelectorTarget(ctx context.Context, lb *hcloud.LoadBalancer, opts hcloud.LoadBalancerAddLabelSelectorTargetOpts) (*hcloud.Action, *hcloud.Response, error) {
	if f.addLabelSelectorTarget == nil {
		return nil, nil, nil
	}
	return f.addLabelSelectorTarget(ctx, lb, opts)
}

func (f fakeLoadBalancerClient) AddIPTarget(ctx context.Context, lb *hcloud.LoadBalancer, opts hcloud.LoadBalancerAddIPTargetOpts) (*hcloud.Action, *hcloud.Response, error) {
	if f.addIPTarget == nil {
		return nil, nil, nil
	}
	return f.addIPTarget(ctx, lb, opts)
}

func (f fakeLoadBalancerClient) RemoveServerTarget(ctx context.Context, lb *hcloud.LoadBalancer, server *hcloud.Server) (*hcloud.Action, *hcloud.Response, error) {
	if f.removeServerTarget == nil {
		return nil, nil, nil
	}
	return f.removeServerTarget(ctx, lb, server)
}

func (f fakeLoadBalancerClient) RemoveLabelSelectorTarget(ctx context.Context, lb *hcloud.LoadBalancer, selector string) (*hcloud.Action, *hcloud.Response, error) {
	if f.removeLabelSelectorTarget == nil {
		return nil, nil, nil
	}
	return f.removeLabelSelectorTarget(ctx, lb, selector)
}

func (f fakeLoadBalancerClient) RemoveIPTarget(ctx context.Context, lb *hcloud.LoadBalancer, ip net.IP) (*hcloud.Action, *hcloud.Response, error) {
	if f.removeIPTarget == nil {
		return nil, nil, nil
	}
	return f.removeIPTarget(ctx, lb, ip)
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

func TestLoadBalancer_List_PropagatesError(t *testing.T) {
	// Discovery errors must be surfaced, not hidden as empty lists —
	// otherwise an invalid token or a 5xx during discovery looks like "no
	// resources" to the drift workflow.
	api := fakeAPI{loadBalancer: fakeLoadBalancerClient{
		all: func(context.Context) ([]*hcloud.LoadBalancer, error) {
			return nil, errors.New("unavailable")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: LoadBalancerResourceType})
	if err == nil {
		t.Fatalf("expected error from List on API failure, got nil (result=%+v)", res)
	}
	if res != nil {
		t.Errorf("expected nil result on error, got %+v", res)
	}
}

func TestLoadBalancer_List_UnsupportedTypeIsEmpty(t *testing.T) {
	// Unsupported types still return an empty list (not an error) so the
	// formae agent's per-type fan-out can complete.
	p := newPluginWithClient(fakeAPI{})
	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: "HETZNER::Bogus::Type"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || len(res.NativeIDs) != 0 {
		t.Errorf("expected empty list for unsupported type, got %+v", res)
	}
}

// --- Validation: services / targets ----------------------------------------

func TestLoadBalancer_ValidateServices(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			"duplicate listenPort rejected",
			`{"name":"lb","loadBalancerType":"lb11","services":[` +
				`{"protocol":"tcp","listenPort":443,"destinationPort":443},` +
				`{"protocol":"tcp","listenPort":443,"destinationPort":443}]}`,
			true,
		},
		{
			"invalid protocol rejected",
			`{"name":"lb","loadBalancerType":"lb11","services":[` +
				`{"protocol":"smtp","listenPort":25,"destinationPort":25}]}`,
			true,
		},
		{
			"zero listenPort rejected",
			`{"name":"lb","loadBalancerType":"lb11","services":[` +
				`{"protocol":"tcp","listenPort":0,"destinationPort":443}]}`,
			true,
		},
		{
			"missing destinationPort rejected",
			`{"name":"lb","loadBalancerType":"lb11","services":[` +
				`{"protocol":"tcp","listenPort":443}]}`,
			true,
		},
		{
			"bad duration rejected",
			`{"name":"lb","loadBalancerType":"lb11","services":[` +
				`{"protocol":"http","listenPort":80,"destinationPort":80,"http":{"cookieLifetime":"not-a-duration"}}]}`,
			true,
		},
		{
			"valid service accepted",
			`{"name":"lb","loadBalancerType":"lb11","services":[` +
				`{"protocol":"http","listenPort":80,"destinationPort":8080,` +
				`"healthCheck":{"protocol":"http","interval":"10s","timeout":"5s"}}]}`,
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseLoadBalancerProperties(json.RawMessage(c.json))
			if c.wantErr && err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestLoadBalancer_ValidateTargets(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			"server without serverId rejected",
			`{"name":"lb","loadBalancerType":"lb11","targets":[{"type":"server"}]}`,
			true,
		},
		{
			"label_selector without selector rejected",
			`{"name":"lb","loadBalancerType":"lb11","targets":[{"type":"label_selector"}]}`,
			true,
		},
		{
			"ip without ip rejected",
			`{"name":"lb","loadBalancerType":"lb11","targets":[{"type":"ip"}]}`,
			true,
		},
		{
			"invalid ip rejected",
			`{"name":"lb","loadBalancerType":"lb11","targets":[{"type":"ip","ip":"not-an-ip"}]}`,
			true,
		},
		{
			"invalid type rejected",
			`{"name":"lb","loadBalancerType":"lb11","targets":[{"type":"cloud"}]}`,
			true,
		},
		{
			"duplicate serverId rejected",
			`{"name":"lb","loadBalancerType":"lb11","targets":[` +
				`{"type":"server","serverId":42},` +
				`{"type":"server","serverId":42}]}`,
			true,
		},
		{
			"usePrivateIP on ip target rejected",
			`{"name":"lb","loadBalancerType":"lb11","targets":[` +
				`{"type":"ip","ip":"203.0.113.5","usePrivateIP":true}]}`,
			true,
		},
		{
			"valid server target accepted",
			`{"name":"lb","loadBalancerType":"lb11","targets":[` +
				`{"type":"server","serverId":42,"usePrivateIP":true}]}`,
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseLoadBalancerProperties(json.RawMessage(c.json))
			if c.wantErr && err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

// --- Create: services / targets synchronous path --------------------------

func TestLoadBalancer_Create_WithServicesAndTargets(t *testing.T) {
	var (
		gotCreateOpts     hcloud.LoadBalancerCreateOpts
		addServiceCalls   int
		addServerCalls    int
		addLabelSelCalls  int
		addIPCalls        int
		actionStatusCalls map[int64]hcloud.ActionStatus
	)
	actionStatusCalls = map[int64]hcloud.ActionStatus{
		200: hcloud.ActionStatusSuccess, // create
		201: hcloud.ActionStatusSuccess, // first target
		202: hcloud.ActionStatusSuccess, // second target
		203: hcloud.ActionStatusSuccess, // third target
		204: hcloud.ActionStatusSuccess, // service
	}
	api := fakeAPI{
		loadBalancer: fakeLoadBalancerClient{
			create: func(_ context.Context, opts hcloud.LoadBalancerCreateOpts) (hcloud.LoadBalancerCreateResult, *hcloud.Response, error) {
				gotCreateOpts = opts
				return hcloud.LoadBalancerCreateResult{
					LoadBalancer: &hcloud.LoadBalancer{ID: 77, Name: opts.Name},
					Action:       &hcloud.Action{ID: 200},
				}, nil, nil
			},
			getByID: func(_ context.Context, id int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
				return sampleLoadBalancerWithServicesAndTargets(), nil, nil
			},
			addServerTarget: func(_ context.Context, lb *hcloud.LoadBalancer, opts hcloud.LoadBalancerAddServerTargetOpts) (*hcloud.Action, *hcloud.Response, error) {
				if lb.ID != 77 {
					t.Errorf("AddServerTarget lb id: want 77, got %d", lb.ID)
				}
				if opts.Server == nil || opts.Server.ID != 42 {
					t.Errorf("AddServerTarget server id: want 42, got %+v", opts.Server)
				}
				addServerCalls++
				return &hcloud.Action{ID: 201}, nil, nil
			},
			addLabelSelectorTarget: func(_ context.Context, lb *hcloud.LoadBalancer, opts hcloud.LoadBalancerAddLabelSelectorTargetOpts) (*hcloud.Action, *hcloud.Response, error) {
				if opts.Selector != "env=prod" {
					t.Errorf("AddLabelSelectorTarget selector: want env=prod, got %q", opts.Selector)
				}
				addLabelSelCalls++
				return &hcloud.Action{ID: 202}, nil, nil
			},
			addIPTarget: func(_ context.Context, lb *hcloud.LoadBalancer, opts hcloud.LoadBalancerAddIPTargetOpts) (*hcloud.Action, *hcloud.Response, error) {
				if opts.IP.String() != "203.0.113.5" {
					t.Errorf("AddIPTarget ip: want 203.0.113.5, got %s", opts.IP)
				}
				addIPCalls++
				return &hcloud.Action{ID: 203}, nil, nil
			},
			addService: func(_ context.Context, lb *hcloud.LoadBalancer, opts hcloud.LoadBalancerAddServiceOpts) (*hcloud.Action, *hcloud.Response, error) {
				if lb.ID != 77 {
					t.Errorf("AddService lb id: want 77, got %d", lb.ID)
				}
				if opts.ListenPort == nil || *opts.ListenPort != 443 {
					t.Errorf("AddService listenPort: want 443, got %+v", opts.ListenPort)
				}
				addServiceCalls++
				return &hcloud.Action{ID: 204}, nil, nil
			},
		},
		action: fakeActionClient{
			getByID: func(_ context.Context, id int64) (*hcloud.Action, *hcloud.Response, error) {
				status, ok := actionStatusCalls[id]
				if !ok {
					t.Fatalf("unexpected action id %d", id)
				}
				return &hcloud.Action{ID: id, Status: status}, nil, nil
			},
		},
	}
	p := newPluginWithClient(api)

	props := `{"name":"lb-1","loadBalancerType":"lb11","location":"nbg1",` +
		`"services":[{"protocol":"http","listenPort":443,"destinationPort":80}],` +
		`"targets":[` +
		`{"type":"server","serverId":42,"usePrivateIP":true},` +
		`{"type":"label_selector","selector":"env=prod"},` +
		`{"type":"ip","ip":"203.0.113.5"}` +
		`]}`

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: LoadBalancerResourceType,
		Properties:   json.RawMessage(props),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("Status: want Success (synchronous services/targets path), got %q", pr.OperationStatus)
	}
	if pr.NativeID != "77" {
		t.Errorf("NativeID: want 77, got %q", pr.NativeID)
	}
	if addServerCalls != 1 || addLabelSelCalls != 1 || addIPCalls != 1 {
		t.Errorf("target add calls: want server=1 label=1 ip=1, got server=%d label=%d ip=%d", addServerCalls, addLabelSelCalls, addIPCalls)
	}
	if addServiceCalls != 1 {
		t.Errorf("AddService calls: want 1, got %d", addServiceCalls)
	}
	if gotCreateOpts.Name != "lb-1" {
		t.Errorf("Create name: want lb-1, got %q", gotCreateOpts.Name)
	}
	// ResourceProperties must carry services/targets for the post-create
	// inventory comparison to pass.
	var got LoadBalancerProperties
	if err := json.Unmarshal(pr.ResourceProperties, &got); err != nil {
		t.Fatalf("invalid readback properties: %v", err)
	}
	if len(got.Services) != 1 {
		t.Errorf("readback services: want 1, got %d", len(got.Services))
	}
	if len(got.Targets) != 3 {
		t.Errorf("readback targets: want 3, got %d", len(got.Targets))
	}
}

func TestLoadBalancer_Create_ActionWaitFailure(t *testing.T) {
	// Create with services declared but the create Action ends in error →
	// the synchronous path must surface the failure rather than report
	// Success against a half-created LB.
	api := fakeAPI{
		loadBalancer: fakeLoadBalancerClient{
			create: func(_ context.Context, opts hcloud.LoadBalancerCreateOpts) (hcloud.LoadBalancerCreateResult, *hcloud.Response, error) {
				return hcloud.LoadBalancerCreateResult{
					LoadBalancer: &hcloud.LoadBalancer{ID: 78, Name: opts.Name},
					Action:       &hcloud.Action{ID: 300},
				}, nil, nil
			},
		},
		action: fakeActionClient{
			getByID: func(_ context.Context, id int64) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: id, Status: hcloud.ActionStatusError, ErrorMessage: "boom"}, nil, nil
			},
		},
	}
	p := newPluginWithClient(api)

	props := `{"name":"lb-1","loadBalancerType":"lb11","services":[{"protocol":"tcp","listenPort":443,"destinationPort":443}]}`

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: LoadBalancerResourceType,
		Properties:   json.RawMessage(props),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusFailure {
		t.Errorf("Status: want Failure, got %q", res.ProgressResult.OperationStatus)
	}
	if res.ProgressResult.StatusMessage != "boom" {
		t.Errorf("StatusMessage: want boom, got %q", res.ProgressResult.StatusMessage)
	}
}

// --- Update: services / targets reconciliation -----------------------------

func TestLoadBalancer_Update_ReconcilesServices(t *testing.T) {
	// Current LB has services on 80 and 443. Desired state keeps 80 (with
	// updated destination port), drops 443, adds 8443. Expect one
	// UpdateService, one DeleteService, one AddService.
	var (
		updateCalls []int
		deleteCalls []int
		addCalls    []int
	)
	currentLB := sampleLoadBalancerWithServicesAndTargets()
	currentLB.Services = []hcloud.LoadBalancerService{
		{Protocol: hcloud.LoadBalancerServiceProtocolHTTP, ListenPort: 80, DestinationPort: 8080},
		{Protocol: hcloud.LoadBalancerServiceProtocolHTTPS, ListenPort: 443, DestinationPort: 8443},
	}
	api := fakeAPI{
		loadBalancer: fakeLoadBalancerClient{
			getByID: func(_ context.Context, id int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
				return currentLB, nil, nil
			},
			updateService: func(_ context.Context, _ *hcloud.LoadBalancer, listenPort int, _ hcloud.LoadBalancerUpdateServiceOpts) (*hcloud.Action, *hcloud.Response, error) {
				updateCalls = append(updateCalls, listenPort)
				return &hcloud.Action{ID: int64(900 + listenPort), Status: hcloud.ActionStatusSuccess}, nil, nil
			},
			deleteService: func(_ context.Context, _ *hcloud.LoadBalancer, listenPort int) (*hcloud.Action, *hcloud.Response, error) {
				deleteCalls = append(deleteCalls, listenPort)
				return &hcloud.Action{ID: int64(800 + listenPort), Status: hcloud.ActionStatusSuccess}, nil, nil
			},
			addService: func(_ context.Context, _ *hcloud.LoadBalancer, _ hcloud.LoadBalancerAddServiceOpts) (*hcloud.Action, *hcloud.Response, error) {
				addCalls = append(addCalls, 1)
				return &hcloud.Action{ID: 700, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
		},
		action: fakeActionClient{
			getByID: func(_ context.Context, id int64) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: id, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
		},
	}
	p := newPluginWithClient(api)

	desired := `{"name":"lb-1","loadBalancerType":"lb-example",` +
		`"services":[` +
		`{"protocol":"http","listenPort":80,"destinationPort":9000},` +
		`{"protocol":"tcp","listenPort":8443,"destinationPort":8443}` +
		`]}`

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      LoadBalancerResourceType,
		NativeID:          "77",
		DesiredProperties: json.RawMessage(desired),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", res.ProgressResult.OperationStatus)
	}
	if len(updateCalls) != 1 || updateCalls[0] != 80 {
		t.Errorf("UpdateService calls: want [80], got %v", updateCalls)
	}
	if len(deleteCalls) != 1 || deleteCalls[0] != 443 {
		t.Errorf("DeleteService calls: want [443], got %v", deleteCalls)
	}
	if len(addCalls) != 1 {
		t.Errorf("AddService calls: want 1, got %d", len(addCalls))
	}
}

func TestLoadBalancer_Update_ReconcilesTargets(t *testing.T) {
	// Current LB has server:42 and ip:203.0.113.5. Desired keeps server:42
	// (no-op), drops the ip, adds label_selector:env=prod.
	var (
		removeServerCalls int
		removeIPCalls     int
		addLabelSelCalls  int
	)
	currentLB := sampleLoadBalancerWithServicesAndTargets()
	currentLB.Targets = []hcloud.LoadBalancerTarget{
		{
			Type:   hcloud.LoadBalancerTargetTypeServer,
			Server: &hcloud.LoadBalancerTargetServer{Server: &hcloud.Server{ID: 42}},
		},
		{
			Type: hcloud.LoadBalancerTargetTypeIP,
			IP:   &hcloud.LoadBalancerTargetIP{IP: "203.0.113.5"},
		},
	}
	api := fakeAPI{
		loadBalancer: fakeLoadBalancerClient{
			getByID: func(_ context.Context, id int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
				return currentLB, nil, nil
			},
			removeServerTarget: func(_ context.Context, _ *hcloud.LoadBalancer, _ *hcloud.Server) (*hcloud.Action, *hcloud.Response, error) {
				removeServerCalls++
				return &hcloud.Action{ID: 901, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
			removeIPTarget: func(_ context.Context, _ *hcloud.LoadBalancer, _ net.IP) (*hcloud.Action, *hcloud.Response, error) {
				removeIPCalls++
				return &hcloud.Action{ID: 902, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
			addLabelSelectorTarget: func(_ context.Context, _ *hcloud.LoadBalancer, opts hcloud.LoadBalancerAddLabelSelectorTargetOpts) (*hcloud.Action, *hcloud.Response, error) {
				addLabelSelCalls++
				if opts.Selector != "env=prod" {
					t.Errorf("AddLabelSelectorTarget selector: want env=prod, got %q", opts.Selector)
				}
				return &hcloud.Action{ID: 903, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
		},
		action: fakeActionClient{
			getByID: func(_ context.Context, id int64) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: id, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
		},
	}
	p := newPluginWithClient(api)

	desired := `{"name":"lb-1","loadBalancerType":"lb-example",` +
		`"targets":[` +
		`{"type":"server","serverId":42},` +
		`{"type":"label_selector","selector":"env=prod"}` +
		`]}`

	if _, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      LoadBalancerResourceType,
		NativeID:          "77",
		DesiredProperties: json.RawMessage(desired),
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removeServerCalls != 0 {
		t.Errorf("RemoveServerTarget (server:42 is kept): want 0, got %d", removeServerCalls)
	}
	if removeIPCalls != 1 {
		t.Errorf("RemoveIPTarget (ip:203.0.113.5 is dropped): want 1, got %d", removeIPCalls)
	}
	if addLabelSelCalls != 1 {
		t.Errorf("AddLabelSelectorTarget: want 1, got %d", addLabelSelCalls)
	}
}

func TestLoadBalancer_Update_TargetRemovalAndAdd(t *testing.T) {
	// Force a server-target swap to exercise the Remove+Add server branch.
	currentLB := sampleLoadBalancerWithServicesAndTargets()
	currentLB.Targets = []hcloud.LoadBalancerTarget{
		{
			Type:   hcloud.LoadBalancerTargetTypeServer,
			Server: &hcloud.LoadBalancerTargetServer{Server: &hcloud.Server{ID: 42}},
		},
	}
	var (
		removeServerCalls []int64
		addServerCalls    []int64
	)
	api := fakeAPI{
		loadBalancer: fakeLoadBalancerClient{
			getByID: func(_ context.Context, id int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
				return currentLB, nil, nil
			},
			removeServerTarget: func(_ context.Context, _ *hcloud.LoadBalancer, s *hcloud.Server) (*hcloud.Action, *hcloud.Response, error) {
				removeServerCalls = append(removeServerCalls, s.ID)
				return &hcloud.Action{ID: 910, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
			addServerTarget: func(_ context.Context, _ *hcloud.LoadBalancer, opts hcloud.LoadBalancerAddServerTargetOpts) (*hcloud.Action, *hcloud.Response, error) {
				addServerCalls = append(addServerCalls, opts.Server.ID)
				return &hcloud.Action{ID: 911, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
		},
		action: fakeActionClient{
			getByID: func(_ context.Context, id int64) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: id, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
		},
	}
	p := newPluginWithClient(api)

	desired := `{"name":"lb-1","loadBalancerType":"lb-example",` +
		`"targets":[{"type":"server","serverId":99}]}`

	if _, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      LoadBalancerResourceType,
		NativeID:          "77",
		DesiredProperties: json.RawMessage(desired),
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(removeServerCalls) != 1 || removeServerCalls[0] != 42 {
		t.Errorf("RemoveServerTarget ids: want [42], got %v", removeServerCalls)
	}
	if len(addServerCalls) != 1 || addServerCalls[0] != 99 {
		t.Errorf("AddServerTarget ids: want [99], got %v", addServerCalls)
	}
}

func TestLoadBalancer_Update_ReplacesTargetWhenUsePrivateIPChanges(t *testing.T) {
	currentLB := sampleLoadBalancerWithServicesAndTargets()
	currentLB.Targets = []hcloud.LoadBalancerTarget{
		{
			Type:         hcloud.LoadBalancerTargetTypeServer,
			Server:       &hcloud.LoadBalancerTargetServer{Server: &hcloud.Server{ID: 42}},
			UsePrivateIP: false,
		},
	}
	var (
		removeServerCalls []int64
		addServerCalls    []hcloud.LoadBalancerAddServerTargetOpts
	)
	api := fakeAPI{
		loadBalancer: fakeLoadBalancerClient{
			getByID: func(_ context.Context, id int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
				return currentLB, nil, nil
			},
			removeServerTarget: func(_ context.Context, _ *hcloud.LoadBalancer, s *hcloud.Server) (*hcloud.Action, *hcloud.Response, error) {
				removeServerCalls = append(removeServerCalls, s.ID)
				return &hcloud.Action{ID: 920, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
			addServerTarget: func(_ context.Context, _ *hcloud.LoadBalancer, opts hcloud.LoadBalancerAddServerTargetOpts) (*hcloud.Action, *hcloud.Response, error) {
				addServerCalls = append(addServerCalls, opts)
				return &hcloud.Action{ID: 921, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
		},
		action: fakeActionClient{
			getByID: func(_ context.Context, id int64) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: id, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
		},
	}
	p := newPluginWithClient(api)

	desired := `{"name":"lb-1","loadBalancerType":"lb-example",` +
		`"targets":[{"type":"server","serverId":42,"usePrivateIP":true}]}`

	if _, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      LoadBalancerResourceType,
		NativeID:          "77",
		DesiredProperties: json.RawMessage(desired),
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(removeServerCalls) != 1 || removeServerCalls[0] != 42 {
		t.Errorf("RemoveServerTarget ids: want [42], got %v", removeServerCalls)
	}
	if len(addServerCalls) != 1 {
		t.Fatalf("AddServerTarget calls: want 1, got %d", len(addServerCalls))
	}
	if addServerCalls[0].Server == nil || addServerCalls[0].Server.ID != 42 {
		t.Errorf("AddServerTarget server: want 42, got %+v", addServerCalls[0].Server)
	}
	if addServerCalls[0].UsePrivateIP == nil || *addServerCalls[0].UsePrivateIP != true {
		t.Errorf("AddServerTarget UsePrivateIP: want Ptr(true), got %+v", addServerCalls[0].UsePrivateIP)
	}
}

// --- Round-trip from hcloud.LoadBalancer -----------------------------------

func TestLoadBalancer_PropertiesFrom_RoundTripsServicesAndTargets(t *testing.T) {
	lb := sampleLoadBalancerWithServicesAndTargets()
	props := loadBalancerPropertiesFrom(lb)

	if len(props.Services) != 1 {
		t.Fatalf("services: want 1, got %d", len(props.Services))
	}
	svc := props.Services[0]
	if svc.ListenPort != 443 || svc.Protocol != "https" {
		t.Errorf("service: want https/443, got %q/%d", svc.Protocol, svc.ListenPort)
	}
	if svc.HTTP == nil {
		t.Fatal("service.HTTP: want non-nil")
	}
	if svc.ProxyProtocol == nil || *svc.ProxyProtocol != false {
		t.Errorf("proxyProtocol read-back: want Ptr(false), got %+v", svc.ProxyProtocol)
	}
	if svc.HTTP.RedirectHTTP == nil || *svc.HTTP.RedirectHTTP != false {
		t.Errorf("http.redirectHTTP read-back: want Ptr(false), got %+v", svc.HTTP.RedirectHTTP)
	}
	if svc.HTTP.StickySessions == nil || *svc.HTTP.StickySessions != false {
		t.Errorf("http.stickySessions read-back: want Ptr(false), got %+v", svc.HTTP.StickySessions)
	}
	if len(svc.HTTP.CertificateIDs) != 1 || svc.HTTP.CertificateIDs[0] != 555 {
		t.Errorf("certificateIds: want [555], got %v", svc.HTTP.CertificateIDs)
	}
	if svc.HealthCheck == nil || svc.HealthCheck.Protocol != "http" {
		t.Errorf("healthCheck.protocol: want http, got %+v", svc.HealthCheck)
	}
	if svc.HealthCheck.HTTP == nil || svc.HealthCheck.HTTP.Path != "/health" {
		t.Errorf("healthCheck.http.path: want /health, got %+v", svc.HealthCheck.HTTP)
	}
	if svc.HealthCheck.HTTP == nil || svc.HealthCheck.HTTP.TLS == nil || *svc.HealthCheck.HTTP.TLS != false {
		t.Errorf("healthCheck.http.tls read-back: want Ptr(false), got %+v", svc.HealthCheck.HTTP)
	}

	if len(props.Targets) != 3 {
		t.Fatalf("targets: want 3, got %d", len(props.Targets))
	}
	gotKeys := make(map[string]bool, len(props.Targets))
	for _, tg := range props.Targets {
		gotKeys[loadBalancerTargetKey(tg)] = true
	}
	for _, want := range []string{"server:42", "label_selector:env=prod", "ip:203.0.113.5"} {
		if !gotKeys[want] {
			t.Errorf("missing target key %q (got %v)", want, gotKeys)
		}
	}
}

func TestLoadBalancer_TargetKeyStableBetweenShapes(t *testing.T) {
	// The desired-shape and observed-shape keyers MUST produce the same
	// string for the same identity — otherwise the update-time diff would
	// churn targets as add+remove on every reconcile.
	cases := []struct {
		name     string
		desired  LoadBalancerTargetProperties
		observed hcloud.LoadBalancerTarget
	}{
		{
			"server",
			LoadBalancerTargetProperties{Type: "server", ServerID: 42},
			hcloud.LoadBalancerTarget{
				Type:   hcloud.LoadBalancerTargetTypeServer,
				Server: &hcloud.LoadBalancerTargetServer{Server: &hcloud.Server{ID: 42}},
			},
		},
		{
			"label_selector",
			LoadBalancerTargetProperties{Type: "label_selector", Selector: "env=prod"},
			hcloud.LoadBalancerTarget{
				Type:          hcloud.LoadBalancerTargetTypeLabelSelector,
				LabelSelector: &hcloud.LoadBalancerTargetLabelSelector{Selector: "env=prod"},
			},
		},
		{
			"ip",
			LoadBalancerTargetProperties{Type: "ip", IP: "203.0.113.5"},
			hcloud.LoadBalancerTarget{
				Type: hcloud.LoadBalancerTargetTypeIP,
				IP:   &hcloud.LoadBalancerTargetIP{IP: "203.0.113.5"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dk := loadBalancerTargetKey(c.desired)
			ok := loadBalancerTargetKeyFromHcloud(c.observed)
			if dk != ok {
				t.Errorf("key mismatch: desired=%q observed=%q", dk, ok)
			}
		})
	}
}

// --- nil-action safety (Finding 3) ----------------------------------------
//
// addLoadBalancerTarget (like AddService) may surface a nil *Action on
// success-with-no-action. The create-time target loop used to dereference
// action.ID unconditionally and would panic; this test pins the nil-guard.

func TestLoadBalancer_Create_NilActionFromAddTargetDoesNotPanic(t *testing.T) {
	api := fakeAPI{
		loadBalancer: fakeLoadBalancerClient{
			create: func(_ context.Context, opts hcloud.LoadBalancerCreateOpts) (hcloud.LoadBalancerCreateResult, *hcloud.Response, error) {
				return hcloud.LoadBalancerCreateResult{
					LoadBalancer: &hcloud.LoadBalancer{ID: 81, Name: opts.Name},
					Action:       &hcloud.Action{ID: 600, Status: hcloud.ActionStatusSuccess},
				}, nil, nil
			},
			getByID: func(_ context.Context, id int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
				return sampleLoadBalancerWithServicesAndTargets(), nil, nil
			},
			addServerTarget: func(_ context.Context, _ *hcloud.LoadBalancer, _ hcloud.LoadBalancerAddServerTargetOpts) (*hcloud.Action, *hcloud.Response, error) {
				// hcloud returns no Action for this add: success-with-no-action.
				return nil, nil, nil
			},
		},
		action: fakeActionClient{
			getByID: func(_ context.Context, id int64) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: id, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
		},
	}
	p := newPluginWithClient(api)

	props := `{"name":"lb-1","loadBalancerType":"lb11",` +
		`"targets":[{"type":"server","serverId":42}]}`

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: LoadBalancerResourceType,
		Properties:   json.RawMessage(props),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success despite nil action from AddServerTarget, got %q", res.ProgressResult.OperationStatus)
	}
}

// --- Aggregate deadline (Finding 2) ----------------------------------------
//
// The create/update attach chains fan out one action wait per target and
// service. Each wait is individually bounded by lbActionTimeout (30s), so
// "create action + N targets + M services" could otherwise stack to
// minutes and overrun the framework's per-RPC budget. lbAggregateTimeout
// (applied via context.WithDeadline) bounds the whole chain; these tests
// pin that behaviour by passing a short parent context and confirming the
// chain aborts well inside a single per-action budget.

func TestLoadBalancer_Create_AggregateDeadlineBoundsAttachChain(t *testing.T) {
	// Actions never settle: every action polls as Running. Without the
	// aggregate deadline the create action + one service + two targets
	// would wait up to 4*lbActionTimeout = 120s.
	api := fakeAPI{
		loadBalancer: fakeLoadBalancerClient{
			create: func(_ context.Context, opts hcloud.LoadBalancerCreateOpts) (hcloud.LoadBalancerCreateResult, *hcloud.Response, error) {
				return hcloud.LoadBalancerCreateResult{
					LoadBalancer: &hcloud.LoadBalancer{ID: 80, Name: opts.Name},
					Action:       &hcloud.Action{ID: 400},
				}, nil, nil
			},
			getByID: func(_ context.Context, id int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
				return sampleLoadBalancerWithServicesAndTargets(), nil, nil
			},
			addServerTarget: func(_ context.Context, _ *hcloud.LoadBalancer, _ hcloud.LoadBalancerAddServerTargetOpts) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: 401}, nil, nil
			},
			addService: func(_ context.Context, _ *hcloud.LoadBalancer, _ hcloud.LoadBalancerAddServiceOpts) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: 402}, nil, nil
			},
		},
		action: fakeActionClient{
			getByID: func(_ context.Context, id int64) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: id, Status: hcloud.ActionStatusRunning}, nil, nil
			},
		},
	}
	p := newPluginWithClient(api)

	// 300ms parent deadline: the derived aggregate context inherits the
	// earlier of (parent, now+lbAggregateTimeout), so the chain must abort
	// within roughly the poll cadence after the deadline fires.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	res, err := p.Create(ctx, &resource.CreateRequest{
		ResourceType: LoadBalancerResourceType,
		Properties: json.RawMessage(`{"name":"lb-1","loadBalancerType":"lb11",` +
			`"services":[{"protocol":"tcp","listenPort":443,"destinationPort":443}],` +
			`"targets":[{"type":"server","serverId":1},{"type":"server","serverId":2}]}`),
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Finding #2: on aggregate deadline the LB itself was created, so the
	// plugin surfaces Success (with a warning + best-effort read-back) and
	// lets the next Sync finish the pending attaches. Failure would mislead
	// the formae agent into treating the create as rolled back and orphan
	// the LB.
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success on aggregate deadline (LB exists; pending attaches recover via next Sync), got %q", res.ProgressResult.OperationStatus)
	}
	if res.ProgressResult.NativeID != "80" {
		t.Errorf("NativeID: want 80, got %q", res.ProgressResult.NativeID)
	}
	if !strings.Contains(res.ProgressResult.StatusMessage, "timed out") {
		t.Errorf("StatusMessage: want 'timed out ...' warning, got %q", res.ProgressResult.StatusMessage)
	}
	// Read-back populates observed state so the framework's post-create
	// inventory comparison can pass without waiting for the next Sync.
	if len(res.ProgressResult.ResourceProperties) == 0 {
		t.Errorf("ResourceProperties: want non-empty best-effort read-back, got empty")
	}
	// Must abort well inside a single per-action lbActionTimeout budget.
	if elapsed > 5*time.Second {
		t.Errorf("aggregate deadline did not bound the chain: elapsed %v (want < 5s)", elapsed)
	}
}

func TestLoadBalancer_Update_AggregateDeadlineBoundsReconcile(t *testing.T) {
	currentLB := sampleLoadBalancerWithServicesAndTargets()
	currentLB.Services = nil
	currentLB.Targets = []hcloud.LoadBalancerTarget{
		{Type: hcloud.LoadBalancerTargetTypeServer, Server: &hcloud.LoadBalancerTargetServer{Server: &hcloud.Server{ID: 1}}},
	}
	api := fakeAPI{
		loadBalancer: fakeLoadBalancerClient{
			getByID: func(_ context.Context, id int64) (*hcloud.LoadBalancer, *hcloud.Response, error) {
				return currentLB, nil, nil
			},
			addServerTarget: func(_ context.Context, _ *hcloud.LoadBalancer, _ hcloud.LoadBalancerAddServerTargetOpts) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: 500}, nil, nil
			},
		},
		action: fakeActionClient{
			getByID: func(_ context.Context, id int64) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: id, Status: hcloud.ActionStatusRunning}, nil, nil
			},
		},
	}
	p := newPluginWithClient(api)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	res, err := p.Update(ctx, &resource.UpdateRequest{
		ResourceType:      LoadBalancerResourceType,
		NativeID:          "80",
		DesiredProperties: json.RawMessage(`{"name":"lb-1","loadBalancerType":"lb11","targets":[{"type":"server","serverId":1},{"type":"server","serverId":2}]}`),
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Finding #2 (update counterpart): the LB exists throughout update, so
	// a truncated reconcile surfaces Success with a warning and lets the
	// next Sync complete the remaining attaches — Failure would imply the
	// LB itself failed to update.
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success on aggregate deadline (LB exists; partial reconcile recovers via next Sync), got %q", res.ProgressResult.OperationStatus)
	}
	if !strings.Contains(res.ProgressResult.StatusMessage, "timed out") {
		t.Errorf("StatusMessage: want 'timed out ...' warning, got %q", res.ProgressResult.StatusMessage)
	}
	if elapsed > 5*time.Second {
		t.Errorf("aggregate deadline did not bound the reconcile: elapsed %v (want < 5s)", elapsed)
	}
}

// --- Optional-scalar mapping ------------------------------------------------
//
// destinationPort is required. Other optional scalars (proxyProtocol,
// redirectHTTP, stickySessions, healthCheck.http.tls) are pointer fields so
// the mapper can tell "omitted" apart from explicit false.

func TestLoadBalancer_AddServiceOpts_OmittedOptionalScalarsSendNil(t *testing.T) {
	port := 443
	s := LoadBalancerServiceProperties{Protocol: "tcp", ListenPort: 443, DestinationPort: &port}
	opts := loadBalancerAddServiceOpts(s)

	if opts.DestinationPort == nil || *opts.DestinationPort != 443 {
		t.Fatalf("DestinationPort: want Ptr(443), got %+v", opts.DestinationPort)
	}
	if opts.Proxyprotocol != nil {
		t.Errorf("Proxyprotocol: want nil when omitted, got %v", *opts.Proxyprotocol)
	}
	if opts.HTTP != nil {
		t.Errorf("HTTP: want nil when no http block, got %+v", opts.HTTP)
	}
	if opts.HealthCheck != nil {
		t.Errorf("HealthCheck: want nil when no healthCheck block, got %+v", opts.HealthCheck)
	}
}

func TestLoadBalancer_AddServiceOpts_ExplicitFalseBooleansPropagate(t *testing.T) {
	// Explicit false must reach hcloud as Ptr(false) so update can clear a
	// provider-side true. The legacy behaviour collapsed false into omitted
	// and could never clear these fields.
	falseVal := false
	port := 443
	s := LoadBalancerServiceProperties{
		Protocol:        "https",
		ListenPort:      443,
		DestinationPort: &port,
		ProxyProtocol:   &falseVal,
		HTTP: &LoadBalancerServiceHTTPProperties{
			RedirectHTTP:   &falseVal,
			StickySessions: &falseVal,
		},
		HealthCheck: &LoadBalancerServiceHealthCheckProperties{
			Protocol: "http",
			HTTP: &LoadBalancerServiceHealthCheckHTTPProperties{
				TLS: &falseVal,
			},
		},
	}

	opts := loadBalancerAddServiceOpts(s)
	if opts.Proxyprotocol == nil || *opts.Proxyprotocol != false {
		t.Errorf("Proxyprotocol: want Ptr(false), got %+v", opts.Proxyprotocol)
	}
	if opts.HTTP == nil || opts.HTTP.RedirectHTTP == nil || *opts.HTTP.RedirectHTTP != false {
		t.Errorf("HTTP.RedirectHTTP: want Ptr(false), got %+v", opts.HTTP)
	}
	if opts.HTTP == nil || opts.HTTP.StickySessions == nil || *opts.HTTP.StickySessions != false {
		t.Errorf("HTTP.StickySessions: want Ptr(false), got %+v", opts.HTTP)
	}
	if opts.HealthCheck == nil || opts.HealthCheck.HTTP == nil || opts.HealthCheck.HTTP.TLS == nil || *opts.HealthCheck.HTTP.TLS != false {
		t.Errorf("HealthCheck.HTTP.TLS: want Ptr(false), got %+v", opts.HealthCheck)
	}
}

func TestLoadBalancer_UpdateServiceOpts_OmittedOptionalScalarsSendNil(t *testing.T) {
	port := 443
	s := LoadBalancerServiceProperties{Protocol: "tcp", ListenPort: 443, DestinationPort: &port}
	opts := loadBalancerUpdateServiceOpts(s)

	if opts.DestinationPort == nil || *opts.DestinationPort != 443 {
		t.Fatalf("DestinationPort: want Ptr(443), got %+v", opts.DestinationPort)
	}
	if opts.Proxyprotocol != nil {
		t.Errorf("Proxyprotocol: want nil when omitted, got %v", *opts.Proxyprotocol)
	}
	if opts.HTTP != nil {
		t.Errorf("HTTP: want nil when no http block, got %+v", opts.HTTP)
	}
	if opts.HealthCheck != nil {
		t.Errorf("HealthCheck: want nil when no healthCheck block, got %+v", opts.HealthCheck)
	}
}

func TestLoadBalancer_AddServiceOpts_RequiredDestinationPortIsSent(t *testing.T) {
	port80 := 80
	s := LoadBalancerServiceProperties{Protocol: "http", ListenPort: 443, DestinationPort: &port80}
	opts := loadBalancerAddServiceOpts(s)
	if opts.DestinationPort == nil || *opts.DestinationPort != 80 {
		t.Fatalf("DestinationPort: want Ptr(80), got %+v", opts.DestinationPort)
	}
}

func TestLoadBalancer_ValidateServices_RejectsZeroDestinationPort(t *testing.T) {
	zero := 0
	err := validateLoadBalancerServices([]LoadBalancerServiceProperties{
		{Protocol: "tcp", ListenPort: 443, DestinationPort: &zero},
	})
	if err == nil {
		t.Fatal("expected error for destinationPort=0, got nil")
	}
}

func TestLoadBalancer_ValidateServices_RejectsMissingDestinationPort(t *testing.T) {
	err := validateLoadBalancerServices([]LoadBalancerServiceProperties{
		{Protocol: "tcp", ListenPort: 443},
	})
	if err == nil {
		t.Fatal("expected error for missing destinationPort, got nil")
	}
}

// --- Test helpers ----------------------------------------------------------

func sampleLoadBalancerWithServicesAndTargets() *hcloud.LoadBalancer {
	lb := sampleLoadBalancer()
	certID := int64(555)
	idle := 30 * time.Second
	lb.Services = []hcloud.LoadBalancerService{
		{
			Protocol:        hcloud.LoadBalancerServiceProtocolHTTPS,
			ListenPort:      443,
			DestinationPort: 8080,
			HTTP: hcloud.LoadBalancerServiceHTTP{
				Certificates: []*hcloud.Certificate{{ID: certID}},
				TimeoutIdle:  idle,
			},
			HealthCheck: hcloud.LoadBalancerServiceHealthCheck{
				Protocol: hcloud.LoadBalancerServiceProtocolHTTP,
				Port:     8080,
				Interval: 15 * time.Second,
				Timeout:  10 * time.Second,
				Retries:  3,
				HTTP: &hcloud.LoadBalancerServiceHealthCheckHTTP{
					Path:        "/health",
					StatusCodes: []string{"200", "204"},
				},
			},
		},
	}
	lb.Targets = []hcloud.LoadBalancerTarget{
		{
			Type:   hcloud.LoadBalancerTargetTypeServer,
			Server: &hcloud.LoadBalancerTargetServer{Server: &hcloud.Server{ID: 42}},
		},
		{
			Type:          hcloud.LoadBalancerTargetTypeLabelSelector,
			LabelSelector: &hcloud.LoadBalancerTargetLabelSelector{Selector: "env=prod"},
		},
		{
			Type: hcloud.LoadBalancerTargetTypeIP,
			IP:   &hcloud.LoadBalancerTargetIP{IP: "203.0.113.5"},
		},
	}
	return lb
}
