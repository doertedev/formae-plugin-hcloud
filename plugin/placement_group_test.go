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

// fakePlacementGroupClient implements hcloud.IPlacementGroupClient. The five
// methods the suite exercises dispatch to function fields (nil field => zero
// response); the remaining methods are inherited from the nil-embedded
// interface.
type fakePlacementGroupClient struct {
	hcloud.IPlacementGroupClient

	create  func(context.Context, hcloud.PlacementGroupCreateOpts) (hcloud.PlacementGroupCreateResult, *hcloud.Response, error)
	getByID func(context.Context, int64) (*hcloud.PlacementGroup, *hcloud.Response, error)
	update  func(context.Context, *hcloud.PlacementGroup, hcloud.PlacementGroupUpdateOpts) (*hcloud.PlacementGroup, *hcloud.Response, error)
	delete  func(context.Context, *hcloud.PlacementGroup) (*hcloud.Response, error)
	all     func(context.Context) ([]*hcloud.PlacementGroup, error)
}

func (f fakePlacementGroupClient) Create(ctx context.Context, opts hcloud.PlacementGroupCreateOpts) (hcloud.PlacementGroupCreateResult, *hcloud.Response, error) {
	if f.create == nil {
		return hcloud.PlacementGroupCreateResult{}, nil, nil
	}
	return f.create(ctx, opts)
}

func (f fakePlacementGroupClient) GetByID(ctx context.Context, id int64) (*hcloud.PlacementGroup, *hcloud.Response, error) {
	if f.getByID == nil {
		return nil, nil, nil
	}
	return f.getByID(ctx, id)
}

func (f fakePlacementGroupClient) Update(ctx context.Context, pg *hcloud.PlacementGroup, opts hcloud.PlacementGroupUpdateOpts) (*hcloud.PlacementGroup, *hcloud.Response, error) {
	if f.update == nil {
		return nil, nil, nil
	}
	return f.update(ctx, pg, opts)
}

func (f fakePlacementGroupClient) Delete(ctx context.Context, pg *hcloud.PlacementGroup) (*hcloud.Response, error) {
	if f.delete == nil {
		return nil, nil
	}
	return f.delete(ctx, pg)
}

func (f fakePlacementGroupClient) All(ctx context.Context) ([]*hcloud.PlacementGroup, error) {
	if f.all == nil {
		return nil, nil
	}
	return f.all(ctx)
}

var _ hcloud.IPlacementGroupClient = fakePlacementGroupClient{}

const validPlacementGroupProps = `{"name":"web-pg","type":"spread"}`

func samplePlacementGroup() *hcloud.PlacementGroup {
	return &hcloud.PlacementGroup{
		ID:      77,
		Name:    "web-pg",
		Type:    hcloud.PlacementGroupTypeSpread,
		Labels:  map[string]string{"managed_by": "formae"},
		Servers: []int64{1, 2, 3},
	}
}

// --- Create ----------------------------------------------------------------

func TestPlacementGroup_Create_Success(t *testing.T) {
	var captured hcloud.PlacementGroupCreateOpts
	api := fakeAPI{placementGroup: fakePlacementGroupClient{
		create: func(_ context.Context, opts hcloud.PlacementGroupCreateOpts) (hcloud.PlacementGroupCreateResult, *hcloud.Response, error) {
			captured = opts
			return hcloud.PlacementGroupCreateResult{PlacementGroup: samplePlacementGroup()}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: PlacementGroupResourceType,
		Properties:   json.RawMessage(validPlacementGroupProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Create is synchronous — expect Success, not InProgress.
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "77" {
		t.Errorf("NativeID: want 77, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae label injected, got %q", got)
	}
	if captured.Name != "web-pg" {
		t.Errorf("expected name forwarded, got %q", captured.Name)
	}
	if captured.Type != hcloud.PlacementGroupTypeSpread {
		t.Errorf("expected type forwarded, got %q", captured.Type)
	}
}

func TestPlacementGroup_Create_InvalidType(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: PlacementGroupResourceType,
		Properties:   json.RawMessage(`{"name":"pg","type":"unknown"}`),
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
}

func TestPlacementGroup_Create_MissingFields(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing-name", `{"type":"spread"}`},
		{"missing-type", `{"name":"pg"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPluginWithClient(fakeAPI{})
			res, err := p.Create(context.Background(), &resource.CreateRequest{
				ResourceType: PlacementGroupResourceType,
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

func TestPlacementGroup_Create_APIError(t *testing.T) {
	api := fakeAPI{placementGroup: fakePlacementGroupClient{
		create: func(context.Context, hcloud.PlacementGroupCreateOpts) (hcloud.PlacementGroupCreateResult, *hcloud.Response, error) {
			return hcloud.PlacementGroupCreateResult{}, nil, errors.New("rate limited")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: PlacementGroupResourceType,
		Properties:   json.RawMessage(validPlacementGroupProps),
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

func TestPlacementGroup_Read_Existing(t *testing.T) {
	api := fakeAPI{placementGroup: fakePlacementGroupClient{
		getByID: func(_ context.Context, id int64) (*hcloud.PlacementGroup, *hcloud.Response, error) {
			if id != 77 {
				t.Errorf("GetByID id: want 77, got %d", id)
			}
			return samplePlacementGroup(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: PlacementGroupResourceType,
		NativeID:     "77",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != "" {
		t.Errorf("ErrorCode: want empty, got %q", res.ErrorCode)
	}
	var props PlacementGroupProperties
	if err := json.Unmarshal([]byte(res.Properties), &props); err != nil {
		t.Fatalf("invalid properties JSON: %v", err)
	}
	if props.Name != "web-pg" {
		t.Errorf("Name: want web-pg, got %q", props.Name)
	}
	if props.Type != "spread" {
		t.Errorf("Type: want spread, got %q", props.Type)
	}
	if len(props.Servers) != 3 {
		t.Errorf("Servers len: want 3, got %d", len(props.Servers))
	}
}

func TestPlacementGroup_Read_NotFound(t *testing.T) {
	api := fakeAPI{placementGroup: fakePlacementGroupClient{
		getByID: func(context.Context, int64) (*hcloud.PlacementGroup, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: PlacementGroupResourceType,
		NativeID:     "77",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ErrorCode)
	}
}

func TestPlacementGroup_Read_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: PlacementGroupResourceType,
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

func TestPlacementGroup_Update_NameAndLabels(t *testing.T) {
	var captured hcloud.PlacementGroupUpdateOpts
	api := fakeAPI{placementGroup: fakePlacementGroupClient{
		update: func(_ context.Context, pg *hcloud.PlacementGroup, opts hcloud.PlacementGroupUpdateOpts) (*hcloud.PlacementGroup, *hcloud.Response, error) {
			captured = opts
			return pg, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.PlacementGroup, *hcloud.Response, error) {
			return samplePlacementGroup(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      PlacementGroupResourceType,
		NativeID:          "77",
		DesiredProperties: json.RawMessage(`{"name":"web-pg-2","type":"spread","labels":{"team":"infra"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "77" {
		t.Errorf("NativeID: want 77, got %q", pr.NativeID)
	}
	if captured.Name != "web-pg-2" {
		t.Errorf("update not forwarded: want web-pg-2, got %q", captured.Name)
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
	var props PlacementGroupProperties
	if err := json.Unmarshal(pr.ResourceProperties, &props); err != nil {
		t.Fatalf("invalid readback properties: %v", err)
	}
}

func TestPlacementGroup_Update_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      PlacementGroupResourceType,
		NativeID:          "oops",
		DesiredProperties: json.RawMessage(validPlacementGroupProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

// --- Delete ----------------------------------------------------------------

func TestPlacementGroup_Delete_Success(t *testing.T) {
	api := fakeAPI{placementGroup: fakePlacementGroupClient{
		getByID: func(context.Context, int64) (*hcloud.PlacementGroup, *hcloud.Response, error) {
			return samplePlacementGroup(), nil, nil
		},
		delete: func(_ context.Context, pg *hcloud.PlacementGroup) (*hcloud.Response, error) {
			if pg.ID != 77 {
				t.Errorf("delete id: want 77, got %d", pg.ID)
			}
			return nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: PlacementGroupResourceType,
		NativeID:     "77",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Delete is synchronous — expect Success, not InProgress.
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

func TestPlacementGroup_Delete_AlreadyGone(t *testing.T) {
	api := fakeAPI{placementGroup: fakePlacementGroupClient{
		getByID: func(context.Context, int64) (*hcloud.PlacementGroup, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: PlacementGroupResourceType,
		NativeID:     "77",
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

func TestPlacementGroup_Delete_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: PlacementGroupResourceType,
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

func TestPlacementGroup_List_Success(t *testing.T) {
	api := fakeAPI{placementGroup: fakePlacementGroupClient{
		all: func(context.Context) ([]*hcloud.PlacementGroup, error) {
			return []*hcloud.PlacementGroup{
				{ID: 10},
				{ID: 20},
			}, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: PlacementGroupResourceType})
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

func TestPlacementGroup_List_EmptyOnError(t *testing.T) {
	api := fakeAPI{placementGroup: fakePlacementGroupClient{
		all: func(context.Context) ([]*hcloud.PlacementGroup, error) {
			return nil, errors.New("unavailable")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: PlacementGroupResourceType})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.NativeIDs) != 0 {
		t.Errorf("expected empty list on API error, got %v", res.NativeIDs)
	}
}
