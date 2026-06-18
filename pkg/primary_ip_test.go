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

// fakePrimaryIPClient implements hcloud.IPrimaryIPClient.
type fakePrimaryIPClient struct {
	hcloud.IPrimaryIPClient

	create  func(context.Context, hcloud.PrimaryIPCreateOpts) (*hcloud.PrimaryIPCreateResult, *hcloud.Response, error)
	getByID func(context.Context, int64) (*hcloud.PrimaryIP, *hcloud.Response, error)
	update  func(context.Context, *hcloud.PrimaryIP, hcloud.PrimaryIPUpdateOpts) (*hcloud.PrimaryIP, *hcloud.Response, error)
	delete  func(context.Context, *hcloud.PrimaryIP) (*hcloud.Response, error)
	all     func(context.Context) ([]*hcloud.PrimaryIP, error)
}

func (f fakePrimaryIPClient) Create(ctx context.Context, opts hcloud.PrimaryIPCreateOpts) (*hcloud.PrimaryIPCreateResult, *hcloud.Response, error) {
	if f.create == nil {
		return nil, nil, nil
	}
	return f.create(ctx, opts)
}

func (f fakePrimaryIPClient) GetByID(ctx context.Context, id int64) (*hcloud.PrimaryIP, *hcloud.Response, error) {
	if f.getByID == nil {
		return nil, nil, nil
	}
	return f.getByID(ctx, id)
}

func (f fakePrimaryIPClient) Update(ctx context.Context, ip *hcloud.PrimaryIP, opts hcloud.PrimaryIPUpdateOpts) (*hcloud.PrimaryIP, *hcloud.Response, error) {
	if f.update == nil {
		return nil, nil, nil
	}
	return f.update(ctx, ip, opts)
}

func (f fakePrimaryIPClient) Delete(ctx context.Context, ip *hcloud.PrimaryIP) (*hcloud.Response, error) {
	if f.delete == nil {
		return nil, nil
	}
	return f.delete(ctx, ip)
}

func (f fakePrimaryIPClient) All(ctx context.Context) ([]*hcloud.PrimaryIP, error) {
	if f.all == nil {
		return nil, nil
	}
	return f.all(ctx)
}

var _ hcloud.IPrimaryIPClient = fakePrimaryIPClient{}

const validPrimaryIPProps = `{"name":"pip-1","type":"ipv4","location":"nbg1"}`

func samplePrimaryIP() *hcloud.PrimaryIP {
	return &hcloud.PrimaryIP{
		ID:           66,
		Name:         "pip-1",
		Type:         hcloud.PrimaryIPTypeIPv4,
		AssigneeType: "server",
		Location:     &hcloud.Location{Name: "nbg1"},
		Labels:       map[string]string{"managed_by": "formae"},
		IP:           net.ParseIP("203.0.113.66"),
	}
}

// --- Create ----------------------------------------------------------------

func TestPrimaryIP_Create_Success_SyncWhenNoAction(t *testing.T) {
	var captured hcloud.PrimaryIPCreateOpts
	api := fakeAPI{primaryIP: fakePrimaryIPClient{
		create: func(_ context.Context, opts hcloud.PrimaryIPCreateOpts) (*hcloud.PrimaryIPCreateResult, *hcloud.Response, error) {
			captured = opts
			// No assignee → no action → synchronous success path.
			return &hcloud.PrimaryIPCreateResult{
				PrimaryIP: samplePrimaryIP(),
				Action:    nil,
			}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: PrimaryIPResourceType,
		Properties:   json.RawMessage(validPrimaryIPProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status: want Success (sync), got %q", pr.OperationStatus)
	}
	if pr.NativeID != "66" {
		t.Errorf("NativeID: want 66, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae injected, got %q", got)
	}
	if captured.Type != hcloud.PrimaryIPTypeIPv4 {
		t.Errorf("Type forwarded: want ipv4, got %q", captured.Type)
	}
	if captured.Name != "pip-1" {
		t.Errorf("Name forwarded: want pip-1, got %q", captured.Name)
	}
	if captured.Location != "nbg1" {
		t.Errorf("Location forwarded: want nbg1, got %q", captured.Location)
	}
}

func TestPrimaryIP_Create_InProgress_WhenActionReturned(t *testing.T) {
	var captured hcloud.PrimaryIPCreateOpts
	api := fakeAPI{primaryIP: fakePrimaryIPClient{
		create: func(_ context.Context, opts hcloud.PrimaryIPCreateOpts) (*hcloud.PrimaryIPCreateResult, *hcloud.Response, error) {
			captured = opts
			return &hcloud.PrimaryIPCreateResult{
				PrimaryIP: &hcloud.PrimaryIP{ID: 66},
				Action:    &hcloud.Action{ID: 444},
			}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: PrimaryIPResourceType,
		Properties:   json.RawMessage(`{"name":"pip-1","type":"ipv4","assigneeId":99}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusInProgress {
		t.Errorf("Status: want InProgress, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "66" {
		t.Errorf("NativeID: want 66, got %q", pr.NativeID)
	}
	if pr.RequestID != "444" {
		t.Errorf("RequestID: want 444, got %q", pr.RequestID)
	}
	if captured.AssigneeID == nil || *captured.AssigneeID != 99 {
		t.Errorf("AssigneeID forwarded: want 99, got %+v", captured.AssigneeID)
	}
}

func TestPrimaryIP_Create_InvalidRequest(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing-name", `{"type":"ipv4","location":"nbg1"}`},
		{"missing-type", `{"name":"pip-1","location":"nbg1"}`},
		{"invalid-type", `{"name":"pip-1","type":"ipx","location":"nbg1"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPluginWithClient(fakeAPI{})
			res, err := p.Create(context.Background(), &resource.CreateRequest{
				ResourceType: PrimaryIPResourceType,
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

func TestPrimaryIP_Create_APIError(t *testing.T) {
	api := fakeAPI{primaryIP: fakePrimaryIPClient{
		create: func(context.Context, hcloud.PrimaryIPCreateOpts) (*hcloud.PrimaryIPCreateResult, *hcloud.Response, error) {
			return nil, nil, errors.New("rate limited")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: PrimaryIPResourceType,
		Properties:   json.RawMessage(validPrimaryIPProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", code)
	}
}

// --- Read ------------------------------------------------------------------

func TestPrimaryIP_Read_Existing(t *testing.T) {
	api := fakeAPI{primaryIP: fakePrimaryIPClient{
		getByID: func(_ context.Context, id int64) (*hcloud.PrimaryIP, *hcloud.Response, error) {
			if id != 66 {
				t.Errorf("GetByID id: want 66, got %d", id)
			}
			return samplePrimaryIP(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: PrimaryIPResourceType,
		NativeID:     "66",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != "" {
		t.Errorf("ErrorCode: want empty, got %q", res.ErrorCode)
	}
	var props PrimaryIPProperties
	if err := json.Unmarshal([]byte(res.Properties), &props); err != nil {
		t.Fatalf("invalid properties JSON: %v", err)
	}
	if props.Type != "ipv4" {
		t.Errorf("Type: want ipv4, got %q", props.Type)
	}
	if props.Name != "pip-1" {
		t.Errorf("Name: want pip-1, got %q", props.Name)
	}
	if props.IP != "203.0.113.66" {
		t.Errorf("IP: want 203.0.113.66, got %q", props.IP)
	}
}

func TestPrimaryIP_Read_NotFound(t *testing.T) {
	api := fakeAPI{primaryIP: fakePrimaryIPClient{
		getByID: func(context.Context, int64) (*hcloud.PrimaryIP, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: PrimaryIPResourceType,
		NativeID:     "66",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ErrorCode)
	}
}

func TestPrimaryIP_Read_InvalidID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: PrimaryIPResourceType,
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

func TestPrimaryIP_Update_Success(t *testing.T) {
	var captured hcloud.PrimaryIPUpdateOpts
	api := fakeAPI{primaryIP: fakePrimaryIPClient{
		update: func(_ context.Context, ip *hcloud.PrimaryIP, opts hcloud.PrimaryIPUpdateOpts) (*hcloud.PrimaryIP, *hcloud.Response, error) {
			captured = opts
			return ip, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.PrimaryIP, *hcloud.Response, error) {
			return samplePrimaryIP(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      PrimaryIPResourceType,
		NativeID:          "66",
		DesiredProperties: json.RawMessage(`{"type":"ipv4","labels":{"team":"infra"},"autoDelete":true}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if captured.Labels == nil {
		t.Fatal("expected Labels to be set in update opts")
	}
	if got := (*captured.Labels)["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae injected, got %q", got)
	}
	if got := (*captured.Labels)["team"]; got != "infra" {
		t.Errorf("expected user label team=infra preserved, got %q", got)
	}
	if captured.AutoDelete == nil || *captured.AutoDelete != true {
		t.Errorf("AutoDelete forwarded: want true, got %+v", captured.AutoDelete)
	}
}

func TestPrimaryIP_Update_InvalidID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      PrimaryIPResourceType,
		NativeID:          "bad",
		DesiredProperties: json.RawMessage(validPrimaryIPProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

// --- Delete ----------------------------------------------------------------

func TestPrimaryIP_Delete_Success(t *testing.T) {
	api := fakeAPI{primaryIP: fakePrimaryIPClient{
		getByID: func(context.Context, int64) (*hcloud.PrimaryIP, *hcloud.Response, error) {
			return samplePrimaryIP(), nil, nil
		},
		delete: func(_ context.Context, ip *hcloud.PrimaryIP) (*hcloud.Response, error) {
			if ip.ID != 66 {
				t.Errorf("delete id: want 66, got %d", ip.ID)
			}
			return nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: PrimaryIPResourceType,
		NativeID:     "66",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// PrimaryIP Delete is synchronous (no Action returned by hcloud-go).
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
}

func TestPrimaryIP_Delete_AlreadyGone(t *testing.T) {
	api := fakeAPI{primaryIP: fakePrimaryIPClient{
		getByID: func(context.Context, int64) (*hcloud.PrimaryIP, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: PrimaryIPResourceType,
		NativeID:     "66",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProgressResult.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ProgressResult.ErrorCode)
	}
}

func TestPrimaryIP_Delete_InvalidID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: PrimaryIPResourceType,
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

func TestPrimaryIP_List_Success(t *testing.T) {
	api := fakeAPI{primaryIP: fakePrimaryIPClient{
		all: func(context.Context) ([]*hcloud.PrimaryIP, error) {
			return []*hcloud.PrimaryIP{{ID: 1}, {ID: 2}}, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: PrimaryIPResourceType})
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

func TestPrimaryIP_List_PropagatesError(t *testing.T) {
	// Discovery errors must be surfaced, not hidden as empty lists —
	// otherwise an invalid token or a 5xx during discovery looks like "no
	// resources" to the drift workflow.
	api := fakeAPI{primaryIP: fakePrimaryIPClient{
		all: func(context.Context) ([]*hcloud.PrimaryIP, error) {
			return nil, errors.New("unavailable")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: PrimaryIPResourceType})
	if err == nil {
		t.Fatalf("expected error from List on API failure, got nil (result=%+v)", res)
	}
	if res != nil {
		t.Errorf("expected nil result on error, got %+v", res)
	}
}

// --- Observed-state mapper --------------------------------------------------
//
// TestPrimaryIPFrom_StripsManagedLabel locks down Finding #4 for Primary IPs:
// the synthetic managed_by=formae label is injected on every write path but
// MUST NOT leak into observed state. Other user labels must survive.
func TestPrimaryIPFrom_StripsManagedLabel(t *testing.T) {
	p := &hcloud.PrimaryIP{
		ID:     66,
		Name:   "pip-1",
		Type:   hcloud.PrimaryIPTypeIPv4,
		Labels: map[string]string{"managed_by": "formae", "owner": "x"},
	}
	props := primaryIPFrom(p)
	if _, ok := props.Labels["managed_by"]; ok {
		t.Errorf("expected managed_by stripped from observed state, got %q", props.Labels["managed_by"])
	}
	if got := props.Labels["owner"]; got != "x" {
		t.Errorf("expected user label owner=x preserved, got %q", got)
	}
}

// TestPrimaryIP_Create_AutoDeleteFalseIsPropagated locks down Finding #19 for
// Primary IP: with the field changed to *bool, an explicit `autoDelete: false`
// in the desired payload is now distinguishable from "unset" and is forwarded
// as a non-nil *bool pointing at false. Previously (with a plain bool) the
// zero-value false was indistinguishable from omitted and was silently
// dropped, making the field impossible to set back to false.
func TestPrimaryIP_Create_AutoDeleteFalseIsPropagated(t *testing.T) {
	var captured hcloud.PrimaryIPCreateOpts
	api := fakeAPI{primaryIP: fakePrimaryIPClient{
		create: func(_ context.Context, opts hcloud.PrimaryIPCreateOpts) (*hcloud.PrimaryIPCreateResult, *hcloud.Response, error) {
			captured = opts
			return &hcloud.PrimaryIPCreateResult{PrimaryIP: samplePrimaryIP()}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: PrimaryIPResourceType,
		Properties:   json.RawMessage(`{"name":"pip-1","type":"ipv4","location":"nbg1","autoDelete":false}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", res.ProgressResult.OperationStatus)
	}
	if captured.AutoDelete == nil {
		t.Fatal("expected AutoDelete forwarded as non-nil *bool even when false; got nil (the bug being fixed)")
	}
	if *captured.AutoDelete != false {
		t.Errorf("expected AutoDelete=false forwarded, got true")
	}
}

// TestPrimaryIP_Update_AutoDeleteFalseIsPropagated is the update-path
// counterpart of TestPrimaryIP_Create_AutoDeleteFalseIsPropagated: an
// explicit `autoDelete: false` in the desired payload must reach
// PrimaryIPUpdateOpts.AutoDelete as a non-nil *bool pointing at false.
func TestPrimaryIP_Update_AutoDeleteFalseIsPropagated(t *testing.T) {
	var captured hcloud.PrimaryIPUpdateOpts
	api := fakeAPI{primaryIP: fakePrimaryIPClient{
		update: func(_ context.Context, ip *hcloud.PrimaryIP, opts hcloud.PrimaryIPUpdateOpts) (*hcloud.PrimaryIP, *hcloud.Response, error) {
			captured = opts
			return ip, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.PrimaryIP, *hcloud.Response, error) {
			return samplePrimaryIP(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      PrimaryIPResourceType,
		NativeID:          "66",
		DesiredProperties: json.RawMessage(`{"type":"ipv4","autoDelete":false}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", res.ProgressResult.OperationStatus)
	}
	if captured.AutoDelete == nil {
		t.Fatal("expected AutoDelete forwarded as non-nil *bool even when false; got nil (the bug being fixed)")
	}
	if *captured.AutoDelete != false {
		t.Errorf("expected AutoDelete=false forwarded, got true")
	}
}
