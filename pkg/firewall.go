// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: HETZNER::Security::Firewall handler.
//
// Signature notes (verified in hcloud-go v2.43.0):
//   - IFirewallClient.Create returns (FirewallCreateResult, *Response, error).
//     FirewallCreateResult.Actions may be non-empty even for rules-only
//     creates (no ApplyTo); those actions are effectively no-ops. The handler
//     treats create as synchronous and reports Success with a read-back of
//     the full firewall so the post-create inventory state carries `rules`.
//     Returning InProgress would lose ResourceProperties on the agent's
//     Status→Success transition.
//   - IFirewallClient.Update only carries name and labels. Rules are mutated
//     via SetRules, which returns []*Action and is therefore async; the
//     handler reports InProgress with the first Action ID after a SetRules
//     call.
//   - IFirewallClient.Delete returns (*Response, error). Delete is synchronous.
//
// Applying firewalls to servers (ApplyTo / AppliedTo) is out of scope.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// FirewallResourceType is the formae resource type for a Hetzner Cloud firewall.
const FirewallResourceType = "HETZNER::Security::Firewall"

func init() {
	register(FirewallResourceType, firewallHandler{})
}

// FirewallRuleProperties is the JSON shape of a single firewall rule. Port is
// only valid when Protocol is tcp or udp.
type FirewallRuleProperties struct {
	Direction      string   `json:"direction"`
	Protocol       string   `json:"protocol"`
	SourceIPs      []string `json:"sourceIps,omitempty"`
	DestinationIPs []string `json:"destinationIps,omitempty"`
	Port           string   `json:"port,omitempty"`
}

// FirewallProperties is the desired/observed state of an hcloud firewall.
type FirewallProperties struct {
	Name   string                   `json:"name"`
	Labels map[string]string        `json:"labels,omitempty"`
	Rules  []FirewallRuleProperties `json:"rules,omitempty"`

	// Observed outputs:
	ID int64 `json:"id,omitempty"`
}

func parseFirewallProperties(data json.RawMessage) (*FirewallProperties, error) {
	var p FirewallProperties
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid firewall properties: %w", err)
	}
	if p.Name == "" {
		return nil, errors.New("firewall properties missing 'name'")
	}
	if err := validateFirewallRules(p.Rules); err != nil {
		return nil, err
	}
	return &p, nil
}

// validateFirewallRules enforces the hcloud API constraints:
//   - direction must be "in" or "out"
//   - protocol must be one of tcp/udp/icmp/esp/gre
//   - port may only be set when protocol is tcp or udp
//
// parseFirewallProperties calls this at the gate so create/update return
// InvalidRequest rather than letting hcloud reject the request.
func validateFirewallRules(rules []FirewallRuleProperties) error {
	for _, r := range rules {
		if r.Direction != "in" && r.Direction != "out" {
			return fmt.Errorf("firewall rule direction must be %q or %q (got %q)", "in", "out", r.Direction)
		}
		switch r.Protocol {
		case "tcp", "udp", "icmp", "esp", "gre":
		default:
			return fmt.Errorf("firewall rule protocol must be one of tcp/udp/icmp/esp/gre (got %q)", r.Protocol)
		}
		if r.Port != "" && r.Protocol != "tcp" && r.Protocol != "udp" {
			return fmt.Errorf("firewall rule 'port' is only valid for tcp/udp protocols (rule protocol %q)", r.Protocol)
		}
	}
	return nil
}

// firewallRulesToHcloud maps the JSON rule shape to hcloud.FirewallRule,
// parsing each source/destination IP as a CIDR. The caller is expected to
// have validated the rules first via validateFirewallRules.
func firewallRulesToHcloud(rules []FirewallRuleProperties) ([]hcloud.FirewallRule, error) {
	out := make([]hcloud.FirewallRule, 0, len(rules))
	for _, r := range rules {
		rule := hcloud.FirewallRule{
			Direction: hcloud.FirewallRuleDirection(r.Direction),
			Protocol:  hcloud.FirewallRuleProtocol(r.Protocol),
		}
		for _, ip := range r.SourceIPs {
			_, n, err := net.ParseCIDR(ip)
			if err != nil {
				return nil, fmt.Errorf("invalid source ip %q: %w", ip, err)
			}
			rule.SourceIPs = append(rule.SourceIPs, *n)
		}
		for _, ip := range r.DestinationIPs {
			_, n, err := net.ParseCIDR(ip)
			if err != nil {
				return nil, fmt.Errorf("invalid destination ip %q: %w", ip, err)
			}
			rule.DestinationIPs = append(rule.DestinationIPs, *n)
		}
		if r.Port != "" {
			port := r.Port
			rule.Port = &port
		}
		out = append(out, rule)
	}
	return out, nil
}

// firewallHandler implements resourceHandler for HETZNER::Security::Firewall.
type firewallHandler struct{}

func (firewallHandler) create(ctx context.Context, c hcloudAPI, req *resource.CreateRequest) (*resource.CreateResult, error) {
	props, err := parseFirewallProperties(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	rules, err := firewallRulesToHcloud(props.Rules)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	labels := mergeManagedLabels(props.Labels)

	opts := hcloud.FirewallCreateOpts{
		Name:   props.Name,
		Labels: labels,
		Rules:  rules,
	}

	res, _, err := c.Firewall().Create(ctx, opts)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), mapHcloudError(err))}, nil
	}
	// The create response's Firewall object does not reliably carry Rules
	// (hcloud-go returns a partial object on create). The conformance harness
	// compares the create-result properties against the desired state BEFORE
	// issuing a Sync/Read, so fetch the full firewall to guarantee rules are
	// present in the initial inventory state. Falls back to the create response
	// if the GetByID fails (e.g. eventual consistency) rather than hard-failing.
	fw, _, getErr := c.Firewall().GetByID(ctx, res.Firewall.ID)
	if getErr != nil || fw == nil {
		fw = res.Firewall
	}
	// hcloud may return Actions even for rules-only creates (no ApplyTo);
	// those actions are effectively no-ops for our purposes. Returning
	// InProgress would hand the action ID to the agent for Status polling,
	// but the agent's eventual Success transition does not preserve the
	// ResourceProperties set on the InProgress response — so the post-create
	// inventory state would be missing `rules` and the conformance Verify
	// step fails. Treat the create as synchronous: report Success with the
	// read-back properties. Any drift is reconciled on the next Sync.
	b, _ := json.Marshal(firewallFrom(fw))
	pr := progress(resource.OperationCreate, resource.OperationStatusSuccess, strconv.FormatInt(res.Firewall.ID, 10), "")
	pr.ResourceProperties = b
	return &resource.CreateResult{ProgressResult: pr}, nil
}

func (firewallHandler) read(ctx context.Context, c hcloudAPI, req *resource.ReadRequest) (*resource.ReadResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInvalidRequest}, nil
	}
	fw, _, err := c.Firewall().GetByID(ctx, id)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: mapHcloudError(err)}, nil
	}
	if fw == nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeNotFound}, nil
	}
	props := firewallFrom(fw)
	b, _ := json.Marshal(props)
	return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(b)}, nil
}

// update changes mutable fields. Name and labels flow through
// IFirewallClient.Update (synchronous); rules flow through SetRules, which
// returns Actions and is therefore async. Applying to servers is out of
// scope.
func (firewallHandler) update(ctx context.Context, c hcloudAPI, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	var desired FirewallProperties
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("invalid firewall properties: %v", err), resource.OperationErrorCodeInvalidRequest)}, nil
	}
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	if err := validateFirewallRules(desired.Rules); err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	opts := hcloud.FirewallUpdateOpts{}
	if desired.Name != "" {
		opts.Name = desired.Name
	}
	if desired.Labels != nil {
		opts.Labels = mergeManagedLabels(desired.Labels)
	}
	if opts.Name != "" || opts.Labels != nil {
		if _, _, err := c.Firewall().Update(ctx, &hcloud.Firewall{ID: id}, opts); err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
		}
	}

	// SetRules is an async operation (returns []*Action). When rules are
	// declared in the desired state, issue SetRules and report InProgress
	// with the first Action ID. An empty/nil rules list clears all rules.
	if desired.Rules != nil {
		rules, err := firewallRulesToHcloud(desired.Rules)
		if err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
		}
		actions, _, err := c.Firewall().SetRules(ctx, &hcloud.Firewall{ID: id}, hcloud.FirewallSetRulesOpts{Rules: rules})
		if err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
		}
		if len(actions) > 0 {
			pr := progress(
				resource.OperationUpdate, resource.OperationStatusInProgress,
				req.NativeID, strconv.FormatInt(actions[0].ID, 10),
			)
			// Best-effort read-back so the InProgress response carries
			// ResourceProperties — the agent's InProgress→Success transition
			// does not preserve them otherwise, which would drop `rules` from
			// the post-update inventory state. Falls back gracefully on error
			// (mirrors the create pattern above) rather than hard-failing.
			fw, _, getErr := c.Firewall().GetByID(ctx, id)
			if getErr == nil && fw != nil {
				b, _ := json.Marshal(firewallFrom(fw))
				pr.ResourceProperties = b
			}
			return &resource.UpdateResult{ProgressResult: pr}, nil
		}
	}

	fw, _, err := c.Firewall().GetByID(ctx, id)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("read-back failed: %v", err), mapHcloudError(err))}, nil
	}
	if fw == nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "firewall not found", resource.OperationErrorCodeNotFound)}, nil
	}
	b, _ := json.Marshal(firewallFrom(fw))
	pr := progress(resource.OperationUpdate, resource.OperationStatusSuccess, req.NativeID, "")
	pr.ResourceProperties = b
	return &resource.UpdateResult{ProgressResult: pr}, nil
}

func (firewallHandler) delete(ctx context.Context, c hcloudAPI, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	fw, _, err := c.Firewall().GetByID(ctx, id)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	if fw == nil {
		pr := progress(resource.OperationDelete, resource.OperationStatusFailure, req.NativeID, "")
		pr.ErrorCode = resource.OperationErrorCodeNotFound
		return &resource.DeleteResult{ProgressResult: pr}, nil
	}
	// IFirewallClient.Delete is synchronous — no Action to poll.
	if _, err := c.Firewall().Delete(ctx, fw); err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	return &resource.DeleteResult{ProgressResult: progress(
		resource.OperationDelete, resource.OperationStatusSuccess,
		req.NativeID, "",
	)}, nil
}

func (firewallHandler) list(ctx context.Context, c hcloudAPI, _ *resource.ListRequest) (*resource.ListResult, error) {
	fws, err := c.Firewall().All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(fws))
	for _, fw := range fws {
		ids = append(ids, strconv.FormatInt(fw.ID, 10))
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func firewallFrom(fw *hcloud.Firewall) FirewallProperties {
	props := FirewallProperties{
		Name:   fw.Name,
		Labels: stripManagedLabel(fw.Labels),
		ID:     fw.ID,
	}
	props.Rules = make([]FirewallRuleProperties, 0, len(fw.Rules))
	for _, r := range fw.Rules {
		rule := FirewallRuleProperties{
			Direction: string(r.Direction),
			Protocol:  string(r.Protocol),
		}
		for _, ip := range r.SourceIPs {
			rule.SourceIPs = append(rule.SourceIPs, ip.String())
		}
		for _, ip := range r.DestinationIPs {
			rule.DestinationIPs = append(rule.DestinationIPs, ip.String())
		}
		if r.Port != nil {
			rule.Port = *r.Port
		}
		props.Rules = append(props.Rules, rule)
	}
	return props
}
