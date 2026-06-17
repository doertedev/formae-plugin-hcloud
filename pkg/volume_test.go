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

// fakeVolumeClient implements hcloud.IVolumeClient. The five methods the
// suite exercises dispatch to function fields (nil field => zero response);
// the remaining methods are inherited from the nil-embedded interface.
type fakeVolumeClient struct {
	hcloud.IVolumeClient

	create  func(context.Context, hcloud.VolumeCreateOpts) (hcloud.VolumeCreateResult, *hcloud.Response, error)
	getByID func(context.Context, int64) (*hcloud.Volume, *hcloud.Response, error)
	update  func(context.Context, *hcloud.Volume, hcloud.VolumeUpdateOpts) (*hcloud.Volume, *hcloud.Response, error)
	delete  func(context.Context, *hcloud.Volume) (*hcloud.Response, error)
	all     func(context.Context) ([]*hcloud.Volume, error)
}

func (f fakeVolumeClient) Create(ctx context.Context, opts hcloud.VolumeCreateOpts) (hcloud.VolumeCreateResult, *hcloud.Response, error) {
	if f.create == nil {
		return hcloud.VolumeCreateResult{}, nil, nil
	}
	return f.create(ctx, opts)
}

func (f fakeVolumeClient) GetByID(ctx context.Context, id int64) (*hcloud.Volume, *hcloud.Response, error) {
	if f.getByID == nil {
		return nil, nil, nil
	}
	return f.getByID(ctx, id)
}

func (f fakeVolumeClient) Update(ctx context.Context, v *hcloud.Volume, opts hcloud.VolumeUpdateOpts) (*hcloud.Volume, *hcloud.Response, error) {
	if f.update == nil {
		return nil, nil, nil
	}
	return f.update(ctx, v, opts)
}

func (f fakeVolumeClient) Delete(ctx context.Context, v *hcloud.Volume) (*hcloud.Response, error) {
	if f.delete == nil {
		return nil, nil
	}
	return f.delete(ctx, v)
}

func (f fakeVolumeClient) All(ctx context.Context) ([]*hcloud.Volume, error) {
	if f.all == nil {
		return nil, nil
	}
	return f.all(ctx)
}

var _ hcloud.IVolumeClient = fakeVolumeClient{}

const validVolumeProps = `{"name":"web-vol","size":10,"location":"nbg1"}`

func sampleVolume() *hcloud.Volume {
	return &hcloud.Volume{
		ID:          88,
		Name:        "web-vol",
		Size:        10,
		Location:    &hcloud.Location{Name: "nbg1"},
		Labels:      map[string]string{"managed_by": "formae"},
		LinuxDevice: "/dev/disk/by-id/scsi-0HC_Volume_88",
	}
}

// --- Create ----------------------------------------------------------------

func TestVolume_Create_Success(t *testing.T) {
	var captured hcloud.VolumeCreateOpts
	api := fakeAPI{volume: fakeVolumeClient{
		create: func(_ context.Context, opts hcloud.VolumeCreateOpts) (hcloud.VolumeCreateResult, *hcloud.Response, error) {
			captured = opts
			return hcloud.VolumeCreateResult{Volume: sampleVolume()}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: VolumeResourceType,
		Properties:   json.RawMessage(validVolumeProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Create with no server attachment is synchronous.
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "88" {
		t.Errorf("NativeID: want 88, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae label injected, got %q", got)
	}
	if captured.Name != "web-vol" {
		t.Errorf("expected name forwarded, got %q", captured.Name)
	}
	if captured.Size != 10 {
		t.Errorf("expected size forwarded, got %d", captured.Size)
	}
	if captured.Location == nil || captured.Location.Name != "nbg1" {
		t.Errorf("expected location forwarded, got %+v", captured.Location)
	}
}

func TestVolume_Create_WithServer_AttachmentIsAsync(t *testing.T) {
	var captured hcloud.VolumeCreateOpts
	api := fakeAPI{volume: fakeVolumeClient{
		create: func(_ context.Context, opts hcloud.VolumeCreateOpts) (hcloud.VolumeCreateResult, *hcloud.Response, error) {
			captured = opts
			return hcloud.VolumeCreateResult{
				Volume: sampleVolume(),
				Action: &hcloud.Action{ID: 333},
			}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: VolumeResourceType,
		Properties:   json.RawMessage(`{"name":"web-vol","size":10,"server":42,"format":"ext4","autoMount":true}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Create attached to a server returns an Action — InProgress.
	if pr.OperationStatus != resource.OperationStatusInProgress {
		t.Errorf("status: want InProgress, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "88" {
		t.Errorf("NativeID: want 88, got %q", pr.NativeID)
	}
	if pr.RequestID != "333" {
		t.Errorf("RequestID: want 333, got %q", pr.RequestID)
	}
	if captured.Server == nil || captured.Server.ID != 42 {
		t.Errorf("expected server forwarded, got %+v", captured.Server)
	}
	if captured.Format == nil || *captured.Format != "ext4" {
		t.Errorf("expected format forwarded, got %+v", captured.Format)
	}
	if captured.Automount == nil || !*captured.Automount {
		t.Errorf("expected automount forwarded, got %+v", captured.Automount)
	}
}

func TestVolume_Create_MissingFields(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing-name", `{"size":10,"location":"nbg1"}`},
		{"missing-size", `{"name":"v","location":"nbg1"}`},
		{"non-positive-size", `{"name":"v","size":0,"location":"nbg1"}`},
		{"missing-server-and-location", `{"name":"v","size":10}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPluginWithClient(fakeAPI{})
			res, err := p.Create(context.Background(), &resource.CreateRequest{
				ResourceType: VolumeResourceType,
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

func TestVolume_Create_APIError(t *testing.T) {
	api := fakeAPI{volume: fakeVolumeClient{
		create: func(context.Context, hcloud.VolumeCreateOpts) (hcloud.VolumeCreateResult, *hcloud.Response, error) {
			return hcloud.VolumeCreateResult{}, nil, errors.New("rate limited")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: VolumeResourceType,
		Properties:   json.RawMessage(validVolumeProps),
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

func TestVolume_Read_Existing(t *testing.T) {
	api := fakeAPI{volume: fakeVolumeClient{
		getByID: func(_ context.Context, id int64) (*hcloud.Volume, *hcloud.Response, error) {
			if id != 88 {
				t.Errorf("GetByID id: want 88, got %d", id)
			}
			return sampleVolume(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: VolumeResourceType,
		NativeID:     "88",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != "" {
		t.Errorf("ErrorCode: want empty, got %q", res.ErrorCode)
	}
	var props VolumeProperties
	if err := json.Unmarshal([]byte(res.Properties), &props); err != nil {
		t.Fatalf("invalid properties JSON: %v", err)
	}
	if props.Name != "web-vol" {
		t.Errorf("Name: want web-vol, got %q", props.Name)
	}
	if props.Size != 10 {
		t.Errorf("Size: want 10, got %d", props.Size)
	}
	if props.LinuxDevice != "/dev/disk/by-id/scsi-0HC_Volume_88" {
		t.Errorf("LinuxDevice: want /dev/disk/by-id/scsi-0HC_Volume_88, got %q", props.LinuxDevice)
	}
}

func TestVolume_Read_NotFound(t *testing.T) {
	api := fakeAPI{volume: fakeVolumeClient{
		getByID: func(context.Context, int64) (*hcloud.Volume, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: VolumeResourceType,
		NativeID:     "88",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ErrorCode)
	}
}

func TestVolume_Read_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: VolumeResourceType,
		NativeID:     "not-a-number",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", res.ErrorCode)
	}
}

func TestVolume_Read_APIError(t *testing.T) {
	api := fakeAPI{volume: fakeVolumeClient{
		getByID: func(context.Context, int64) (*hcloud.Volume, *hcloud.Response, error) {
			return nil, nil, errors.New("boom")
		},
	}}
	p := newPluginWithClient(api)
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: VolumeResourceType,
		NativeID:     "88",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", res.ErrorCode)
	}
}

// --- Update ----------------------------------------------------------------

func TestVolume_Update_NameAndLabels(t *testing.T) {
	var captured hcloud.VolumeUpdateOpts
	api := fakeAPI{volume: fakeVolumeClient{
		update: func(_ context.Context, v *hcloud.Volume, opts hcloud.VolumeUpdateOpts) (*hcloud.Volume, *hcloud.Response, error) {
			captured = opts
			return v, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.Volume, *hcloud.Response, error) {
			return sampleVolume(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      VolumeResourceType,
		NativeID:          "88",
		DesiredProperties: json.RawMessage(`{"name":"web-vol-2","size":10,"location":"nbg1","labels":{"team":"infra"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "88" {
		t.Errorf("NativeID: want 88, got %q", pr.NativeID)
	}
	if captured.Name != "web-vol-2" {
		t.Errorf("update not forwarded: want web-vol-2, got %q", captured.Name)
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
	var props VolumeProperties
	if err := json.Unmarshal(pr.ResourceProperties, &props); err != nil {
		t.Fatalf("invalid readback properties: %v", err)
	}
}

func TestVolume_Update_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      VolumeResourceType,
		NativeID:          "oops",
		DesiredProperties: json.RawMessage(validVolumeProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

// --- Delete ----------------------------------------------------------------

func TestVolume_Delete_Success(t *testing.T) {
	api := fakeAPI{volume: fakeVolumeClient{
		getByID: func(context.Context, int64) (*hcloud.Volume, *hcloud.Response, error) {
			return sampleVolume(), nil, nil
		},
		delete: func(_ context.Context, v *hcloud.Volume) (*hcloud.Response, error) {
			if v.ID != 88 {
				t.Errorf("delete id: want 88, got %d", v.ID)
			}
			return nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: VolumeResourceType,
		NativeID:     "88",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Delete is synchronous — expect Success, not InProgress.
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "88" {
		t.Errorf("NativeID: want 88, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
}

func TestVolume_Delete_AlreadyGone(t *testing.T) {
	api := fakeAPI{volume: fakeVolumeClient{
		getByID: func(context.Context, int64) (*hcloud.Volume, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: VolumeResourceType,
		NativeID:     "88",
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

func TestVolume_Delete_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: VolumeResourceType,
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

func TestVolume_List_Success(t *testing.T) {
	api := fakeAPI{volume: fakeVolumeClient{
		all: func(context.Context) ([]*hcloud.Volume, error) {
			return []*hcloud.Volume{
				{ID: 10},
				{ID: 20},
			}, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: VolumeResourceType})
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

func TestVolume_List_EmptyOnError(t *testing.T) {
	api := fakeAPI{volume: fakeVolumeClient{
		all: func(context.Context) ([]*hcloud.Volume, error) {
			return nil, errors.New("unavailable")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: VolumeResourceType})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.NativeIDs) != 0 {
		t.Errorf("expected empty list on API error, got %v", res.NativeIDs)
	}
}

// --- Observed-state mapper --------------------------------------------------
//
// TestVolumeFrom_StripsManagedLabel locks down Finding #4 for Volumes: the
// synthetic managed_by=formae label is injected on every write path but
// MUST NOT leak into observed state. Other user labels must survive.
func TestVolumeFrom_StripsManagedLabel(t *testing.T) {
	v := &hcloud.Volume{
		ID:     88,
		Name:   "web-vol",
		Labels: map[string]string{"managed_by": "formae", "owner": "x"},
	}
	props := volumeFrom(v)
	if _, ok := props.Labels["managed_by"]; ok {
		t.Errorf("expected managed_by stripped from observed state, got %q", props.Labels["managed_by"])
	}
	if got := props.Labels["owner"]; got != "x" {
		t.Errorf("expected user label owner=x preserved, got %q", got)
	}
}

// TestVolume_Create_AutoMountFalseIsPropagated locks down Finding #19 for
// Volume: with the field changed to *bool, an explicit `autoMount: false` in
// the desired payload is now distinguishable from "unset" and is forwarded
// as a non-nil *bool pointing at false. Previously (with a plain bool) the
// zero-value false was indistinguishable from omitted and was silently
// dropped, making the field impossible to set back to false.
func TestVolume_Create_AutoMountFalseIsPropagated(t *testing.T) {
	var captured hcloud.VolumeCreateOpts
	api := fakeAPI{volume: fakeVolumeClient{
		create: func(_ context.Context, opts hcloud.VolumeCreateOpts) (hcloud.VolumeCreateResult, *hcloud.Response, error) {
			captured = opts
			return hcloud.VolumeCreateResult{Volume: sampleVolume()}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: VolumeResourceType,
		Properties:   json.RawMessage(`{"name":"web-vol","size":10,"location":"nbg1","autoMount":false}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", res.ProgressResult.OperationStatus)
	}
	if captured.Automount == nil {
		t.Fatal("expected Automount forwarded as non-nil *bool even when false; got nil (the bug being fixed)")
	}
	if *captured.Automount != false {
		t.Errorf("expected Automount=false forwarded, got true")
	}
}
