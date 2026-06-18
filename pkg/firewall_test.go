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

// fakeFirewallClient implements hcloud.IFirewallClient. The methods the suite
// exercises dispatch to function fields (nil field => zero response); the
// remaining methods are inherited from the nil-embedded interface.
type fakeFirewallClient struct {
	hcloud.IFirewallClient

	create   func(context.Context, hcloud.FirewallCreateOpts) (hcloud.FirewallCreateResult, *hcloud.Response, error)
	getByID  func(context.Context, int64) (*hcloud.Firewall, *hcloud.Response, error)
	update   func(context.Context, *hcloud.Firewall, hcloud.FirewallUpdateOpts) (*hcloud.Firewall, *hcloud.Response, error)
	setRules func(context.Context, *hcloud.Firewall, hcloud.FirewallSetRulesOpts) ([]*hcloud.Action, *hcloud.Response, error)
	delete   func(context.Context, *hcloud.Firewall) (*hcloud.Response, error)
	all      func(context.Context) ([]*hcloud.Firewall, error)
}

func (f fakeFirewallClient) Create(ctx context.Context, opts hcloud.FirewallCreateOpts) (hcloud.FirewallCreateResult, *hcloud.Response, error) {
	if f.create == nil {
		return hcloud.FirewallCreateResult{}, nil, nil
	}
	return f.create(ctx, opts)
}

func (f fakeFirewallClient) GetByID(ctx context.Context, id int64) (*hcloud.Firewall, *hcloud.Response, error) {
	if f.getByID == nil {
		return nil, nil, nil
	}
	return f.getByID(ctx, id)
}

func (f fakeFirewallClient) Update(ctx context.Context, fw *hcloud.Firewall, opts hcloud.FirewallUpdateOpts) (*hcloud.Firewall, *hcloud.Response, error) {
	if f.update == nil {
		return nil, nil, nil
	}
	return f.update(ctx, fw, opts)
}

func (f fakeFirewallClient) SetRules(ctx context.Context, fw *hcloud.Firewall, opts hcloud.FirewallSetRulesOpts) ([]*hcloud.Action, *hcloud.Response, error) {
	if f.setRules == nil {
		return nil, nil, nil
	}
	return f.setRules(ctx, fw, opts)
}

func (f fakeFirewallClient) Delete(ctx context.Context, fw *hcloud.Firewall) (*hcloud.Response, error) {
	if f.delete == nil {
		return nil, nil
	}
	return f.delete(ctx, fw)
}

func (f fakeFirewallClient) All(ctx context.Context) ([]*hcloud.Firewall, error) {
	if f.all == nil {
		return nil, nil
	}
	return f.all(ctx)
}

var _ hcloud.IFirewallClient = fakeFirewallClient{}

const validFirewallProps = `{"name":"web-fw","rules":[{"direction":"in","protocol":"tcp","sourceIps":["0.0.0.0/0"],"port":"443"}]}`

func sampleFirewall() *hcloud.Firewall {
	_, srcNet, _ := net.ParseCIDR("0.0.0.0/0")
	port := "443"
	return &hcloud.Firewall{
		ID:     99,
		Name:   "web-fw",
		Labels: map[string]string{"managed_by": "formae"},
		Rules: []hcloud.FirewallRule{{
			Direction: hcloud.FirewallRuleDirectionIn,
			Protocol:  hcloud.FirewallRuleProtocolTCP,
			SourceIPs: []net.IPNet{*srcNet},
			Port:      &port,
		}},
	}
}

// --- Create ----------------------------------------------------------------

func TestFirewall_Create_Success(t *testing.T) {
	var captured hcloud.FirewallCreateOpts
	api := fakeAPI{firewall: fakeFirewallClient{
		create: func(_ context.Context, opts hcloud.FirewallCreateOpts) (hcloud.FirewallCreateResult, *hcloud.Response, error) {
			captured = opts
			// No ApplyTo → no actions → synchronous create.
			return hcloud.FirewallCreateResult{Firewall: sampleFirewall()}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: FirewallResourceType,
		Properties:   json.RawMessage(validFirewallProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Create with no ApplyTo is synchronous — expect Success.
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "99" {
		t.Errorf("NativeID: want 99, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae label injected, got %q", got)
	}
	if captured.Name != "web-fw" {
		t.Errorf("expected name forwarded, got %q", captured.Name)
	}
	if len(captured.Rules) != 1 {
		t.Fatalf("expected 1 rule forwarded, got %d", len(captured.Rules))
	}
	if captured.Rules[0].Direction != hcloud.FirewallRuleDirectionIn {
		t.Errorf("expected direction forwarded, got %q", captured.Rules[0].Direction)
	}
	if captured.Rules[0].Protocol != hcloud.FirewallRuleProtocolTCP {
		t.Errorf("expected protocol forwarded, got %q", captured.Rules[0].Protocol)
	}
	if captured.Rules[0].Port == nil || *captured.Rules[0].Port != "443" {
		t.Errorf("expected port forwarded, got %+v", captured.Rules[0].Port)
	}
}

func TestFirewall_Create_WithActions_ReturnsSuccess(t *testing.T) {
	api := fakeAPI{firewall: fakeFirewallClient{
		create: func(context.Context, hcloud.FirewallCreateOpts) (hcloud.FirewallCreateResult, *hcloud.Response, error) {
			return hcloud.FirewallCreateResult{
				Firewall: sampleFirewall(),
				Actions:  []*hcloud.Action{{ID: 444}},
			}, nil, nil
		},
		getByID: func(_ context.Context, id int64) (*hcloud.Firewall, *hcloud.Response, error) {
			if id != 99 {
				t.Errorf("GetByID id: want 99, got %d", id)
			}
			return sampleFirewall(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: FirewallResourceType,
		Properties:   json.RawMessage(validFirewallProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Create returns Success even when hcloud yields Actions: without ApplyTo
	// the actions are no-ops, and returning InProgress drops ResourceProperties
	// on the agent's Success transition (losing `rules` from inventory).
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status: want Success, got %q", pr.OperationStatus)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
	if pr.NativeID != "99" {
		t.Errorf("NativeID: want 99, got %q", pr.NativeID)
	}
	// ResourceProperties must be populated so the post-create inventory state
	// includes `rules` for the conformance Verify step.
	if len(pr.ResourceProperties) == 0 {
		t.Fatal("expected ResourceProperties to be populated on Success")
	}
	var props FirewallProperties
	if err := json.Unmarshal(pr.ResourceProperties, &props); err != nil {
		t.Fatalf("invalid ResourceProperties JSON: %v", err)
	}
	if len(props.Rules) != 1 {
		t.Errorf("expected ResourceProperties to carry 1 rule, got %d", len(props.Rules))
	}
}

func TestFirewall_Create_MissingName(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: FirewallResourceType,
		Properties:   json.RawMessage(`{"rules":[]}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

func TestFirewall_Create_PortOnNonTcpUdp(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"port-on-icmp", `{"name":"fw","rules":[{"direction":"in","protocol":"icmp","port":"80"}]}`},
		{"port-on-esp", `{"name":"fw","rules":[{"direction":"in","protocol":"esp","port":"80"}]}`},
		{"port-on-gre", `{"name":"fw","rules":[{"direction":"in","protocol":"gre","port":"80"}]}`},
		{"bad-direction", `{"name":"fw","rules":[{"direction":"sideways","protocol":"tcp"}]}`},
		{"bad-protocol", `{"name":"fw","rules":[{"direction":"in","protocol":"unknown"}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPluginWithClient(fakeAPI{})
			res, err := p.Create(context.Background(), &resource.CreateRequest{
				ResourceType: FirewallResourceType,
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

func TestFirewall_Create_APIError(t *testing.T) {
	api := fakeAPI{firewall: fakeFirewallClient{
		create: func(context.Context, hcloud.FirewallCreateOpts) (hcloud.FirewallCreateResult, *hcloud.Response, error) {
			return hcloud.FirewallCreateResult{}, nil, errors.New("rate limited")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: FirewallResourceType,
		Properties:   json.RawMessage(validFirewallProps),
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

func TestFirewall_Read_Existing(t *testing.T) {
	api := fakeAPI{firewall: fakeFirewallClient{
		getByID: func(_ context.Context, id int64) (*hcloud.Firewall, *hcloud.Response, error) {
			if id != 99 {
				t.Errorf("GetByID id: want 99, got %d", id)
			}
			return sampleFirewall(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: FirewallResourceType,
		NativeID:     "99",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != "" {
		t.Errorf("ErrorCode: want empty, got %q", res.ErrorCode)
	}
	var props FirewallProperties
	if err := json.Unmarshal([]byte(res.Properties), &props); err != nil {
		t.Fatalf("invalid properties JSON: %v", err)
	}
	if props.Name != "web-fw" {
		t.Errorf("Name: want web-fw, got %q", props.Name)
	}
	if len(props.Rules) != 1 {
		t.Fatalf("Rules len: want 1, got %d", len(props.Rules))
	}
	if props.Rules[0].Direction != "in" {
		t.Errorf("Rule[0] Direction: want in, got %q", props.Rules[0].Direction)
	}
	if props.Rules[0].Protocol != "tcp" {
		t.Errorf("Rule[0] Protocol: want tcp, got %q", props.Rules[0].Protocol)
	}
	if props.Rules[0].Port != "443" {
		t.Errorf("Rule[0] Port: want 443, got %q", props.Rules[0].Port)
	}
}

func TestFirewall_Read_NotFound(t *testing.T) {
	api := fakeAPI{firewall: fakeFirewallClient{
		getByID: func(context.Context, int64) (*hcloud.Firewall, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: FirewallResourceType,
		NativeID:     "99",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ErrorCode)
	}
}

func TestFirewall_Read_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: FirewallResourceType,
		NativeID:     "not-a-number",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", res.ErrorCode)
	}
}

func TestFirewall_Read_APIError(t *testing.T) {
	api := fakeAPI{firewall: fakeFirewallClient{
		getByID: func(context.Context, int64) (*hcloud.Firewall, *hcloud.Response, error) {
			return nil, nil, errors.New("boom")
		},
	}}
	p := newPluginWithClient(api)
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: FirewallResourceType,
		NativeID:     "99",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", res.ErrorCode)
	}
}

// --- Update ----------------------------------------------------------------

func TestFirewall_Update_NameAndLabels(t *testing.T) {
	var capturedUpdate hcloud.FirewallUpdateOpts
	var setRulesCalled bool
	api := fakeAPI{firewall: fakeFirewallClient{
		update: func(_ context.Context, fw *hcloud.Firewall, opts hcloud.FirewallUpdateOpts) (*hcloud.Firewall, *hcloud.Response, error) {
			capturedUpdate = opts
			return fw, nil, nil
		},
		setRules: func(_ context.Context, fw *hcloud.Firewall, opts hcloud.FirewallSetRulesOpts) ([]*hcloud.Action, *hcloud.Response, error) {
			setRulesCalled = true
			// No actions returned when rules don't change applications.
			return nil, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.Firewall, *hcloud.Response, error) {
			return sampleFirewall(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      FirewallResourceType,
		NativeID:          "99",
		DesiredProperties: json.RawMessage(`{"name":"web-fw-2","labels":{"team":"infra"},"rules":[{"direction":"in","protocol":"tcp","sourceIps":["0.0.0.0/0"],"port":"443"}]}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "99" {
		t.Errorf("NativeID: want 99, got %q", pr.NativeID)
	}
	if capturedUpdate.Name != "web-fw-2" {
		t.Errorf("update not forwarded: want web-fw-2, got %q", capturedUpdate.Name)
	}
	if capturedUpdate.Labels == nil {
		t.Fatal("expected Labels to be set in update opts")
	}
	if got := capturedUpdate.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae label injected, got %q", got)
	}
	if !setRulesCalled {
		t.Error("expected SetRules to be called when rules are in desired state")
	}
}

func TestFirewall_Update_RulesAsync(t *testing.T) {
	api := fakeAPI{firewall: fakeFirewallClient{
		update: func(_ context.Context, fw *hcloud.Firewall, _ hcloud.FirewallUpdateOpts) (*hcloud.Firewall, *hcloud.Response, error) {
			return fw, nil, nil
		},
		setRules: func(_ context.Context, _ *hcloud.Firewall, _ hcloud.FirewallSetRulesOpts) ([]*hcloud.Action, *hcloud.Response, error) {
			return []*hcloud.Action{{ID: 555}}, nil, nil
		},
		getByID: func(_ context.Context, id int64) (*hcloud.Firewall, *hcloud.Response, error) {
			// Read-back after SetRules returns a firewall carrying the desired
			// rules so the InProgress response carries ResourceProperties.
			return &hcloud.Firewall{
				ID:   id,
				Name: "web-fw",
				Rules: []hcloud.FirewallRule{{
					Direction: hcloud.FirewallRuleDirectionOut,
					Protocol:  hcloud.FirewallRuleProtocolUDP,
				}},
			}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      FirewallResourceType,
		NativeID:          "99",
		DesiredProperties: json.RawMessage(`{"name":"web-fw","rules":[{"direction":"out","protocol":"udp"}]}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// SetRules returning actions → InProgress with the first action ID.
	if pr.OperationStatus != resource.OperationStatusInProgress {
		t.Errorf("Status: want InProgress, got %q", pr.OperationStatus)
	}
	if pr.RequestID != "555" {
		t.Errorf("RequestID: want 555, got %q", pr.RequestID)
	}
	// The InProgress response must carry ResourceProperties so the agent's
	// eventual Success transition preserves them — otherwise the post-update
	// inventory state loses `rules`.
	if len(pr.ResourceProperties) == 0 {
		t.Fatal("expected ResourceProperties to be populated on InProgress")
	}
	var props FirewallProperties
	if err := json.Unmarshal(pr.ResourceProperties, &props); err != nil {
		t.Fatalf("invalid ResourceProperties JSON: %v", err)
	}
	if len(props.Rules) != 1 {
		t.Fatalf("expected ResourceProperties to carry 1 rule, got %d", len(props.Rules))
	}
	if props.Rules[0].Direction != "out" || props.Rules[0].Protocol != "udp" {
		t.Errorf("rule mismatch: want out/udp, got direction=%q protocol=%q", props.Rules[0].Direction, props.Rules[0].Protocol)
	}
}

// TestFirewall_Update_Rules_InProgress_ReadBackFails_OmitsProperties verifies
// that when the post-SetRules read-back errors, the handler still returns
// InProgress with the action ID but omits ResourceProperties (the graceful
// fallback) rather than hard-failing the update.
func TestFirewall_Update_Rules_InProgress_ReadBackFails_OmitsProperties(t *testing.T) {
	api := fakeAPI{firewall: fakeFirewallClient{
		update: func(_ context.Context, fw *hcloud.Firewall, _ hcloud.FirewallUpdateOpts) (*hcloud.Firewall, *hcloud.Response, error) {
			return fw, nil, nil
		},
		setRules: func(_ context.Context, _ *hcloud.Firewall, _ hcloud.FirewallSetRulesOpts) ([]*hcloud.Action, *hcloud.Response, error) {
			return []*hcloud.Action{{ID: 777}}, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.Firewall, *hcloud.Response, error) {
			return nil, nil, errors.New("transient")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      FirewallResourceType,
		NativeID:          "99",
		DesiredProperties: json.RawMessage(`{"name":"web-fw","rules":[{"direction":"in","protocol":"tcp","sourceIps":["0.0.0.0/0"],"port":"80"}]}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// The read-back failure must NOT change the InProgress status or action ID.
	if pr.OperationStatus != resource.OperationStatusInProgress {
		t.Errorf("Status: want InProgress, got %q", pr.OperationStatus)
	}
	if pr.RequestID != "777" {
		t.Errorf("RequestID: want 777, got %q", pr.RequestID)
	}
	// ResourceProperties omitted on read-back failure (graceful fallback).
	if len(pr.ResourceProperties) != 0 {
		t.Errorf("expected ResourceProperties to be empty on read-back failure, got %q", string(pr.ResourceProperties))
	}
}

func TestFirewall_Update_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      FirewallResourceType,
		NativeID:          "oops",
		DesiredProperties: json.RawMessage(validFirewallProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

func TestFirewall_Update_InvalidRules(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      FirewallResourceType,
		NativeID:          "99",
		DesiredProperties: json.RawMessage(`{"name":"fw","rules":[{"direction":"in","protocol":"icmp","port":"80"}]}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

// --- Delete ----------------------------------------------------------------

func TestFirewall_Delete_Success(t *testing.T) {
	api := fakeAPI{firewall: fakeFirewallClient{
		getByID: func(context.Context, int64) (*hcloud.Firewall, *hcloud.Response, error) {
			return sampleFirewall(), nil, nil
		},
		delete: func(_ context.Context, fw *hcloud.Firewall) (*hcloud.Response, error) {
			if fw.ID != 99 {
				t.Errorf("delete id: want 99, got %d", fw.ID)
			}
			return nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: FirewallResourceType,
		NativeID:     "99",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// Delete is synchronous — expect Success, not InProgress.
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "99" {
		t.Errorf("NativeID: want 99, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
}

func TestFirewall_Delete_AlreadyGone(t *testing.T) {
	api := fakeAPI{firewall: fakeFirewallClient{
		getByID: func(context.Context, int64) (*hcloud.Firewall, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: FirewallResourceType,
		NativeID:     "99",
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

func TestFirewall_Delete_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: FirewallResourceType,
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

func TestFirewall_List_Success(t *testing.T) {
	api := fakeAPI{firewall: fakeFirewallClient{
		all: func(context.Context) ([]*hcloud.Firewall, error) {
			return []*hcloud.Firewall{
				{ID: 10},
				{ID: 20},
			}, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: FirewallResourceType})
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

func TestFirewall_List_PropagatesError(t *testing.T) {
	// Discovery errors must be surfaced, not hidden as empty lists —
	// otherwise an invalid token or a 5xx during discovery looks like "no
	// resources" to the drift workflow.
	api := fakeAPI{firewall: fakeFirewallClient{
		all: func(context.Context) ([]*hcloud.Firewall, error) {
			return nil, errors.New("unavailable")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: FirewallResourceType})
	if err == nil {
		t.Fatalf("expected error from List on API failure, got nil (result=%+v)", res)
	}
	if res != nil {
		t.Errorf("expected nil result on error, got %+v", res)
	}
}
