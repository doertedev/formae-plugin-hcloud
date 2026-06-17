// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: HETZNER::Compute::Server (hcloud_server) handler.
//
// This file is the template for every future resource file: declare a
// properties struct, a parser, an observed-state mapper, a handler type
// implementing resourceHandler, and an init() that calls register. No
// shared file needs to change to add a new resource.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ServerResourceType is the formae resource type for a Hetzner Cloud server.
const ServerResourceType = "HETZNER::Compute::Server"

func init() {
	register(ServerResourceType, serverHandler{})
}

// ServerProperties is the desired/observed state of an hcloud_server.
type ServerProperties struct {
	Name       string            `json:"name"`
	ServerType string            `json:"serverType"`
	Image      string            `json:"image"`
	Location   string            `json:"location,omitempty"`
	SSHKeys    []string          `json:"sshKeys,omitempty"`
	UserData   string            `json:"userData,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`

	// Observed outputs:
	ID   int64  `json:"id,omitempty"`
	IPv4 string `json:"ipv4,omitempty"`
}

func parseServerProperties(data json.RawMessage) (*ServerProperties, error) {
	var p ServerProperties
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid server properties: %w", err)
	}
	if p.Name == "" {
		return nil, errors.New("server properties missing 'name'")
	}
	if p.ServerType == "" {
		return nil, errors.New("server properties missing 'serverType'")
	}
	if p.Image == "" {
		return nil, errors.New("server properties missing 'image'")
	}
	return &p, nil
}

// serverHandler implements resourceHandler for HETZNER::Compute::Server.
// It carries no state; the receiver type exists only to satisfy the
// resourceHandler interface and to give future fields (e.g. a feature
// flag) somewhere to live.
type serverHandler struct{}

func (serverHandler) create(ctx context.Context, c hcloudAPI, req *resource.CreateRequest) (*resource.CreateResult, error) {
	props, err := parseServerProperties(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	sshKeys := make([]*hcloud.SSHKey, 0, len(props.SSHKeys))
	for _, k := range props.SSHKeys {
		sshKeys = append(sshKeys, &hcloud.SSHKey{Name: k})
	}
	labels := mergeManagedLabels(props.Labels)

	opts := hcloud.ServerCreateOpts{
		Name:       props.Name,
		ServerType: &hcloud.ServerType{Name: props.ServerType},
		Image:      &hcloud.Image{Name: props.Image},
		SSHKeys:    sshKeys,
		UserData:   props.UserData,
		Labels:     labels,
	}
	if props.Location != "" {
		opts.Location = &hcloud.Location{Name: props.Location}
	}

	res, _, err := c.Server().Create(ctx, opts)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), mapHcloudError(err))}, nil
	}
	// ⚠️ DO NOT ADD A GetByID CALL HERE. ⚠️
	//
	// The formae agent's PluginOperator gives the Create RPC a ~40 second
	// budget (after which it declares the operator MissingInAction and fails
	// the command). The hcloud client is bounded to ~21s worst-case per call
	// (see plugin.go getClient: 10s HTTP timeout, 1 retry, 1s backoff), so a
	// SECOND call (GetByID) after Create pushes the total to ~42s — PAST the
	// 40s budget — and the command fails every time. This was the recurring
	// server Create hang. Return InProgress from the SINGLE Create call.
	//
	// InProgress carries BOTH identifiers so the agent can address the
	// resource immediately AND poll provisioning to completion:
	//   - nativeID = res.Server.ID (assigned the instant Create returns; the
	//     agent preserves it across the InProgress→Success transition — its
	//     "Setting NativeID from progress" log confirms it reads every
	//     progress result's NativeID).
	//   - requestID = res.Action.ID (the agent polls Status with this until
	//     the provisioning action completes).
	//
	// ResourceProperties come from the create-response Server. The only field
	// that may be absent is IPv4 (still allocating), and it is marked
	// hasProviderDefault in the schema, so the conformance Verify step
	// tolerates its absence. The next Sync issues a Read that populates IPv4.
	b, _ := json.Marshal(serverPropertiesFrom(res.Server))
	if res.Action != nil {
		pr := progress(
			resource.OperationCreate, resource.OperationStatusInProgress,
			strconv.FormatInt(res.Server.ID, 10), // nativeID — set immediately
			strconv.FormatInt(res.Action.ID, 10), // requestID — for Status polling
		)
		pr.ResourceProperties = b
		return &resource.CreateResult{ProgressResult: pr}, nil
	}
	// Defensive fallback: no Action means hcloud treated the create as
	// synchronous (rare). Report Success with the server ID.
	pr := progress(resource.OperationCreate, resource.OperationStatusSuccess, strconv.FormatInt(res.Server.ID, 10), "")
	pr.ResourceProperties = b
	return &resource.CreateResult{ProgressResult: pr}, nil
}

func (serverHandler) read(ctx context.Context, c hcloudAPI, req *resource.ReadRequest) (*resource.ReadResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInvalidRequest}, nil
	}
	server, _, err := c.Server().GetByID(ctx, id)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: mapHcloudError(err)}, nil
	}
	if server == nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeNotFound}, nil
	}
	props := serverPropertiesFrom(server)
	b, _ := json.Marshal(props)
	return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(b)}, nil
}

// update changes mutable fields (name, labels). Most server attributes
// (server_type, image, location) require a server rebuild in hcloud and are
// not supported here.
func (serverHandler) update(ctx context.Context, c hcloudAPI, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	desired, err := parseServerProperties(req.DesiredProperties)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}

	// Build a single ServerUpdateOpts covering every mutable field with a
	// change, then issue one PUT. A combined call keeps the API footprint
	// minimal and is atomic from formae's perspective. hcloud-go only sends
	// Labels when non-nil, so callers that omit labels leave existing labels
	// untouched.
	opts := hcloud.ServerUpdateOpts{}
	if desired.Name != "" {
		opts.Name = desired.Name
	}
	if desired.Labels != nil {
		// ServerUpdateOpts.Labels is a desired-state replace on the wire
		// (PUT /servers/:id), so build the full label set here. The shared
		// helper copies the caller's map and re-asserts the
		// managed_by=formae invariant injected by Create.
		opts.Labels = mergeManagedLabels(desired.Labels)
	}
	if opts.Name != "" || opts.Labels != nil {
		if _, _, err := c.Server().Update(ctx, &hcloud.Server{ID: id}, opts); err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
		}
	}
	server, _, err := c.Server().GetByID(ctx, id)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("read-back failed: %v", err), mapHcloudError(err))}, nil
	}
	if server == nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "server not found", resource.OperationErrorCodeNotFound)}, nil
	}
	b, _ := json.Marshal(serverPropertiesFrom(server))
	pr := progress(resource.OperationUpdate, resource.OperationStatusSuccess, req.NativeID, "")
	pr.ResourceProperties = b
	return &resource.UpdateResult{ProgressResult: pr}, nil
}

func (serverHandler) delete(ctx context.Context, c hcloudAPI, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	server, _, err := c.Server().GetByID(ctx, id)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	if server == nil {
		// Already gone: agent treats NotFound on Delete as success.
		pr := progress(resource.OperationDelete, resource.OperationStatusFailure, req.NativeID, "")
		pr.ErrorCode = resource.OperationErrorCodeNotFound
		return &resource.DeleteResult{ProgressResult: pr}, nil
	}
	res, _, err := c.Server().DeleteWithResult(ctx, server)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	// hcloud delete returns an action; poll via Status.
	return &resource.DeleteResult{ProgressResult: progress(
		resource.OperationDelete, resource.OperationStatusInProgress,
		req.NativeID, strconv.FormatInt(res.Action.ID, 10),
	)}, nil
}

func (serverHandler) list(ctx context.Context, c hcloudAPI, _ *resource.ListRequest) (*resource.ListResult, error) {
	servers, err := c.Server().All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(servers))
	for _, s := range servers {
		ids = append(ids, strconv.FormatInt(s.ID, 10))
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func serverPropertiesFrom(s *hcloud.Server) ServerProperties {
	props := ServerProperties{
		Name:   s.Name,
		Labels: stripManagedLabel(s.Labels),
		ID:     s.ID,
	}
	if s.ServerType != nil {
		props.ServerType = s.ServerType.Name
	}
	if s.Image != nil {
		props.Image = s.Image.Name
	}
	if s.Location != nil {
		props.Location = s.Location.Name
	}
	if s.PublicNet.IPv4.IP != nil {
		props.IPv4 = s.PublicNet.IPv4.IP.String()
	}
	return props
}
