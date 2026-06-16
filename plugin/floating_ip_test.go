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

// fakeFloatingIPClient implements hcloud.IFloatingIPClient.
type fakeFloatingIPClient struct {
	hcloud.IFloatingIPClient

	create  func(context.Context, hcloud.FloatingIPCreateOpts) (hcloud.FloatingIPCreateResult, *hcloud.Response, error)
	getByID func(context.Context, int64) (*hcloud.FloatingIP, *hcloud.Response, error)
	update  func(context.Context, *hcloud.FloatingIP, hcloud.FloatingIPUpdateOpts) (*hcloud.FloatingIP, *hcloud.Response, error)
	delete  func(context.Context, *hcloud.FloatingIP) (*hcloud.Response, error)
	all     func(context.Context) ([]*hcloud.FloatingIP, error)
}

func (f fakeFloatingIPClient) Create(ctx context.Context, opts hcloud.FloatingIPCreateOpts) (hcloud.FloatingIPCreateResult, *hcloud.Response, error) {
	if f.create == nil {
		return hcloud.FloatingIPCreateResult{}, nil, nil
	}
	return f.create(ctx, opts)
}

func (f fakeFloatingIPClient) GetByID(ctx context.Context, id int64) (*hcloud.FloatingIP, *hcloud.Response, error) {
	if f.getByID == nil {
		return nil, nil, nil
	}
	return f.getByID(ctx, id)
}

func (f fakeFloatingIPClient) Update(ctx context.Context, ip *hcloud.FloatingIP, opts hcloud.FloatingIPUpdateOpts) (*hcloud.FloatingIP, *hcloud.Response, error) {
	if f.update == nil {
		return nil, nil, nil
	}
	return f.update(ctx, ip, opts)
}

func (f fakeFloatingIPClient) Delete(ctx context.Context, ip *hcloud.FloatingIP) (*hcloud.Response, error) {
	if f.delete == nil {
		return nil, nil
	}
	return f.delete(ctx, ip)
}

func (f fakeFloatingIPClient) All(ctx context.Context) ([]*hcloud.FloatingIP, error) {
	if f.all == nil {
		return nil, nil
	}
	return f.all(ctx)
}

var _ hcloud.IFloatingIPClient = fakeFloatingIPClient{}

const validFloatingIPProps = `{"type":"ipv4","homeLocation":"nbg1","description":"floating-1"}`

func sampleFloatingIP() *hcloud.FloatingIP {
	return &hcloud.FloatingIP{
		ID:           55,
		Type:         hcloud.FloatingIPTypeIPv4,
		Description:  "floating-1",
		HomeLocation: &hcloud.Location{Name: "nbg1"},
		Labels:       map[string]string{"managed_by": "formae"},
		IP:           net.ParseIP("203.0.113.55"),
	}
}

// --- Create ----------------------------------------------------------------

func TestFloatingIP_Create_Success_SyncWhenNoAction(t *testing.T) {
	var captured hcloud.FloatingIPCreateOpts
	api := fakeAPI{floatingIP: fakeFloatingIPClient{
		create: func(_ context.Context, opts hcloud.FloatingIPCreateOpts) (hcloud.FloatingIPCreateResult, *hcloud.Response, error) {
			captured = opts
			// No server assignment → no action → synchronous success path.
			return hcloud.FloatingIPCreateResult{
				FloatingIP: sampleFloatingIP(),
				Action:     nil,
			}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: FloatingIPResourceType,
		Properties:   json.RawMessage(validFloatingIPProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status: want Success (sync), got %q", pr.OperationStatus)
	}
	if pr.NativeID != "55" {
		t.Errorf("NativeID: want 55, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae injected, got %q", got)
	}
	if captured.Type != hcloud.FloatingIPTypeIPv4 {
		t.Errorf("Type forwarded: want ipv4, got %q", captured.Type)
	}
	if captured.HomeLocation == nil || captured.HomeLocation.Name != "nbg1" {
		t.Errorf("HomeLocation.Name: want nbg1, got %+v", captured.HomeLocation)
	}
	if captured.Description == nil || *captured.Description != "floating-1" {
		t.Errorf("Description: want floating-1, got %+v", captured.Description)
	}
}

func TestFloatingIP_Create_InProgress_WhenActionReturned(t *testing.T) {
	api := fakeAPI{floatingIP: fakeFloatingIPClient{
		create: func(context.Context, hcloud.FloatingIPCreateOpts) (hcloud.FloatingIPCreateResult, *hcloud.Response, error) {
			return hcloud.FloatingIPCreateResult{
				FloatingIP: &hcloud.FloatingIP{ID: 55},
				Action:     &hcloud.Action{ID: 333},
			}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: FloatingIPResourceType,
		Properties:   json.RawMessage(`{"type":"ipv4","server":99}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusInProgress {
		t.Errorf("Status: want InProgress, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "55" {
		t.Errorf("NativeID: want 55, got %q", pr.NativeID)
	}
	if pr.RequestID != "333" {
		t.Errorf("RequestID: want 333, got %q", pr.RequestID)
	}
}

func TestFloatingIP_Create_InvalidRequest(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing-type", `{"homeLocation":"nbg1"}`},
		{"invalid-type", `{"type":"ipx","homeLocation":"nbg1"}`},
		{"missing-homeLocation-and-server", `{"type":"ipv4"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPluginWithClient(fakeAPI{})
			res, err := p.Create(context.Background(), &resource.CreateRequest{
				ResourceType: FloatingIPResourceType,
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

func TestFloatingIP_Create_APIError(t *testing.T) {
	api := fakeAPI{floatingIP: fakeFloatingIPClient{
		create: func(context.Context, hcloud.FloatingIPCreateOpts) (hcloud.FloatingIPCreateResult, *hcloud.Response, error) {
			return hcloud.FloatingIPCreateResult{}, nil, errors.New("rate limited")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: FloatingIPResourceType,
		Properties:   json.RawMessage(validFloatingIPProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", code)
	}
}

// --- Read ------------------------------------------------------------------

func TestFloatingIP_Read_Existing(t *testing.T) {
	api := fakeAPI{floatingIP: fakeFloatingIPClient{
		getByID: func(_ context.Context, id int64) (*hcloud.FloatingIP, *hcloud.Response, error) {
			if id != 55 {
				t.Errorf("GetByID id: want 55, got %d", id)
			}
			return sampleFloatingIP(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: FloatingIPResourceType,
		NativeID:     "55",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != "" {
		t.Errorf("ErrorCode: want empty, got %q", res.ErrorCode)
	}
	var props FloatingIPProperties
	if err := json.Unmarshal([]byte(res.Properties), &props); err != nil {
		t.Fatalf("invalid properties JSON: %v", err)
	}
	if props.Type != "ipv4" {
		t.Errorf("Type: want ipv4, got %q", props.Type)
	}
	if props.IP != "203.0.113.55" {
		t.Errorf("IP: want 203.0.113.55, got %q", props.IP)
	}
}

func TestFloatingIP_Read_NotFound(t *testing.T) {
	api := fakeAPI{floatingIP: fakeFloatingIPClient{
		getByID: func(context.Context, int64) (*hcloud.FloatingIP, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: FloatingIPResourceType,
		NativeID:     "55",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ErrorCode)
	}
}

func TestFloatingIP_Read_InvalidID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: FloatingIPResourceType,
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

func TestFloatingIP_Update_Success(t *testing.T) {
	var captured hcloud.FloatingIPUpdateOpts
	api := fakeAPI{floatingIP: fakeFloatingIPClient{
		update: func(_ context.Context, ip *hcloud.FloatingIP, opts hcloud.FloatingIPUpdateOpts) (*hcloud.FloatingIP, *hcloud.Response, error) {
			captured = opts
			return ip, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.FloatingIP, *hcloud.Response, error) {
			return sampleFloatingIP(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      FloatingIPResourceType,
		NativeID:          "55",
		DesiredProperties: json.RawMessage(`{"type":"ipv4","homeLocation":"nbg1","description":"updated","labels":{"team":"infra"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if captured.Description != "updated" {
		t.Errorf("Description forwarded: want updated, got %q", captured.Description)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae injected, got %q", got)
	}
}

func TestFloatingIP_Update_InvalidID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      FloatingIPResourceType,
		NativeID:          "bad",
		DesiredProperties: json.RawMessage(validFloatingIPProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

// --- Delete ----------------------------------------------------------------

func TestFloatingIP_Delete_Success(t *testing.T) {
	api := fakeAPI{floatingIP: fakeFloatingIPClient{
		getByID: func(context.Context, int64) (*hcloud.FloatingIP, *hcloud.Response, error) {
			return sampleFloatingIP(), nil, nil
		},
		delete: func(_ context.Context, ip *hcloud.FloatingIP) (*hcloud.Response, error) {
			if ip.ID != 55 {
				t.Errorf("delete id: want 55, got %d", ip.ID)
			}
			return nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: FloatingIPResourceType,
		NativeID:     "55",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// FloatingIP Delete is synchronous (no Action returned by hcloud-go).
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
}

func TestFloatingIP_Delete_AlreadyGone(t *testing.T) {
	api := fakeAPI{floatingIP: fakeFloatingIPClient{
		getByID: func(context.Context, int64) (*hcloud.FloatingIP, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: FloatingIPResourceType,
		NativeID:     "55",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProgressResult.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ProgressResult.ErrorCode)
	}
}

func TestFloatingIP_Delete_InvalidID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: FloatingIPResourceType,
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

func TestFloatingIP_List_Success(t *testing.T) {
	api := fakeAPI{floatingIP: fakeFloatingIPClient{
		all: func(context.Context) ([]*hcloud.FloatingIP, error) {
			return []*hcloud.FloatingIP{{ID: 1}, {ID: 2}}, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: FloatingIPResourceType})
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

func TestFloatingIP_List_EmptyOnError(t *testing.T) {
	api := fakeAPI{floatingIP: fakeFloatingIPClient{
		all: func(context.Context) ([]*hcloud.FloatingIP, error) {
			return nil, errors.New("unavailable")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: FloatingIPResourceType})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.NativeIDs) != 0 {
		t.Errorf("expected empty list on API error, got %v", res.NativeIDs)
	}
}

// --- Observed-state mapper --------------------------------------------------
//
// TestFloatingIPFrom_StripsManagedLabel locks down Finding #4 for Floating
// IPs: the synthetic managed_by=formae label is injected on every write path
// but MUST NOT leak into observed state. Other user labels must survive.
func TestFloatingIPFrom_StripsManagedLabel(t *testing.T) {
	f := &hcloud.FloatingIP{
		ID:     55,
		Type:   hcloud.FloatingIPTypeIPv4,
		Labels: map[string]string{"managed_by": "formae", "owner": "x"},
	}
	props := floatingIPFrom(f)
	if _, ok := props.Labels["managed_by"]; ok {
		t.Errorf("expected managed_by stripped from observed state, got %q", props.Labels["managed_by"])
	}
	if got := props.Labels["owner"]; got != "x" {
		t.Errorf("expected user label owner=x preserved, got %q", got)
	}
}
