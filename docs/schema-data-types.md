# Schema data type examples

This page is intentionally repetitive. It is for editing Pkl without relying
on syntax highlighting or editor completion. Copy a block, change the import
and names, then run `make pkl-eval`.

The examples use this repo's conventions:

- Resource schemas live under `schema/pkl/<category>/<type>.pkl`.
- Conformance fixtures live under `testdata/<type>.pkl`.
- Resource schemas import `@formae/formae.pkl` and `../hcloud.pkl`.
- Fixtures amend `@formae/forma.pkl`, import one hcloud resource module, and
  reuse `testdata/config/vars.pkl`.
- Field hints use `@hcloud.FieldHint`.
- Resource hints use `@hcloud.ResourceHint`.

## Complete schema file skeleton

Use this when adding a new resource schema under `schema/pkl/<category>/`.

```pkl
// SPDX-License-Identifier: Apache-2.0

module hcloud.category.example

import "@formae/formae.pkl"
import "../hcloud.pkl"

const type = "HETZNER::Category::Example"

open class ExampleResolvable extends formae.Resolvable {
    hidden type = module.type

    hidden id: ExampleResolvable = (this) {
        property = "id"
    }
}

@hcloud.ResourceHint {
    type = module.type
    identifier = "id"
    portable = true
}
open class Example extends formae.Resource {
    @hcloud.FieldHint {
        required = true
    }
    name: String

    @hcloud.FieldHint { hasProviderDefault = true }
    labels: Mapping<String, String>?

    @hcloud.FieldHint { hasProviderDefault = true }
    id: Int?

    local parent = this

    hidden res: ExampleResolvable = new {
        label = parent.label
        stack = parent.stack?.label
    }
}
```

## Complete fixture file skeleton

Use this when adding `testdata/<type>.pkl`.

```pkl
// SPDX-License-Identifier: Apache-2.0

amends "@formae/forma.pkl"

import "@hcloud/category/example.pkl"

import "./config/vars.pkl" as v

forma {
  v.stack
  v.target

  new example.Example {
    label = "example-\(v.testRunID)"
    name = "formae-conf-example-\(v.testRunID)"
    labels = new Mapping {
      ["owner"] = "formae-conformance-test"
    }
  }
}
```

## String

Schema field, required string:

```pkl
@hcloud.FieldHint {
    required = true
}
name: String
```

Schema field, optional string:

```pkl
@hcloud.FieldHint
description: String?
```

Schema field, create-only string:

```pkl
@hcloud.FieldHint {
    createOnly = true
}
location: String?
```

Schema field, required and create-only string:

```pkl
@hcloud.FieldHint {
    required = true
    createOnly = true
}
image: String
```

Fixture value, literal string:

```pkl
name = "formae-conf-server-\(v.testRunID)"
```

Fixture value, string interpolation:

```pkl
label = "server-\(v.testRunID)"
```

Fixture value, environment override with fallback:

```pkl
serverType = read?("env:HCLOUD_SERVER_TYPE") ?? "cx22"
```

Fixture value, optional string omitted:

```pkl
new server.Server {
  label = "server-\(v.testRunID)"
  name = "formae-conf-server-\(v.testRunID)"
  serverType = "cx22"
  image = "ubuntu-24.04"
}
```

Fixture value, optional string set:

```pkl
new server.Server {
  label = "server-\(v.testRunID)"
  name = "formae-conf-server-\(v.testRunID)"
  serverType = "cx22"
  image = "ubuntu-24.04"
  location = "fsn1"
}
```

## Int

Schema field, optional provider output:

```pkl
@hcloud.FieldHint { hasProviderDefault = true }
id: Int?
```

Schema field, required integer:

```pkl
listenPort: Int
```

Schema field, optional integer:

```pkl
serverId: Int?
```

Fixture value, integer literal:

```pkl
listenPort = 443
```

Fixture value, integer in a nested object:

```pkl
healthCheck = new {
  protocol = "http"
  port = 8080
}
```

Fixture value, list of integers:

```pkl
http = new {
  certificateIds = new Listing {
    123456
    789012
  }
}
```

## Boolean

Schema field, optional boolean:

```pkl
usePrivateIP: Boolean?
```

Schema field, optional provider-defaulted boolean:

```pkl
@hcloud.FieldHint { hasProviderDefault = true }
proxyProtocol: Boolean?
```

Fixture value, explicit true:

```pkl
usePrivateIP = true
```

Fixture value, explicit false:

```pkl
proxyProtocol = false
```

Fixture value, several nested booleans:

```pkl
http = new {
  redirectHTTP = false
  stickySessions = false
}
```

Fixture value, health check TLS disabled explicitly:

```pkl
healthCheck = new {
  protocol = "http"
  http = new {
    path = "/health"
    tls = false
  }
}
```

## Float

Float fields are provider outputs in this plugin. They are useful in schema
read-back, but conformance fixtures normally omit them.

Schema field, optional provider output:

```pkl
@hcloud.FieldHint { hasProviderDefault = true }
imageSize: Float?
```

Schema field, another provider output:

```pkl
@hcloud.FieldHint { hasProviderDefault = true }
diskSize: Float?
```

Example read-back value shape:

```pkl
imageSize = 2.5
```

Example read-back value shape:

```pkl
diskSize = 20.0
```

## Enum strings

Pkl represents this plugin's enums as string literal unions.

Schema field, simple enum:

```pkl
type: ("ipv4"|"ipv6")
```

Schema field, firewall direction:

```pkl
direction: ("in"|"out")
```

Schema field, firewall protocol:

```pkl
protocol: ("tcp"|"udp"|"icmp"|"esp"|"gre")
```

Schema field, load balancer service protocol:

```pkl
protocol: ("tcp"|"http"|"https")
```

Schema field, load balancer health check protocol:

```pkl
protocol: ("tcp"|"http"|"https"|"grpc")
```

Fixture value, enum string:

```pkl
type = "ipv4"
```

Fixture value, firewall rule enum strings:

```pkl
new {
  direction = "in"
  protocol = "tcp"
  port = "22"
}
```

Fixture value, load balancer service enum:

```pkl
new {
  protocol = "https"
  listenPort = 443
  destinationPort = 443
}
```

## Mapping<String, String>

Use mappings for labels and any other string-to-string object.

Schema field:

```pkl
@hcloud.FieldHint { hasProviderDefault = true }
labels: Mapping<String, String>?
```

Fixture value, one label:

```pkl
labels = new Mapping {
  ["owner"] = "formae-conformance-test"
}
```

Fixture value, multiple labels:

```pkl
labels = new Mapping {
  ["owner"] = "formae-conformance-test"
  ["environment"] = "dev"
  ["run"] = v.testRunID
}
```

Fixture value, label key containing punctuation:

```pkl
labels = new Mapping {
  ["team/name"] = "platform"
  ["cost-center"] = "infra"
}
```

Fixture value, omit labels:

```pkl
new ssh_key.SSHKey {
  label = "ssh-key-\(v.testRunID)"
  name = "formae-conf-ssh-key-\(v.testRunID)"
  publicKey = v.publicSSHKey
}
```

Do not set `managed_by`. The plugin reserves that label key and writes
`managed_by=formae` itself.

## Listing<String>

Schema field:

```pkl
@hcloud.FieldHint {
    createOnly = true
}
sshKeys: Listing<String>?
```

Fixture value, one string:

```pkl
sshKeys = new Listing {
  "formae-conf-ssh-key-\(v.testRunID)"
}
```

Fixture value, multiple strings:

```pkl
sshKeys = new Listing {
  "formae-conf-admin-\(v.testRunID)"
  "formae-conf-ci-\(v.testRunID)"
}
```

Fixture value, IP CIDR strings:

```pkl
sourceIps = new Listing {
  "0.0.0.0/0"
  "::/0"
}
```

Fixture value, status code strings:

```pkl
statusCodes = new Listing {
  "200"
  "204"
}
```

## Listing<Int>

Schema field:

```pkl
certificateIds: Listing<Int>?
```

Fixture value:

```pkl
certificateIds = new Listing {
  123456
  789012
}
```

Fixture value in an HTTPS service:

```pkl
services = new Listing {
  new {
    protocol = "https"
    listenPort = 443
    destinationPort = 443
    http = new {
      certificateIds = new Listing {
        123456
      }
    }
  }
}
```

## Listing<NestedClass>

Use `new Listing { new { ... } }` for nested objects such as firewall rules,
load balancer services, and load balancer targets.

Schema field:

```pkl
@hcloud.FieldHint
rules: Listing<FirewallRule>?
```

Fixture value, one nested object:

```pkl
rules = new Listing {
  new {
    direction = "in"
    protocol = "tcp"
    port = "22"
    sourceIps = new Listing {
      "0.0.0.0/0"
      "::/0"
    }
  }
}
```

Fixture value, multiple nested objects:

```pkl
rules = new Listing {
  new {
    direction = "in"
    protocol = "tcp"
    port = "22"
    sourceIps = new Listing {
      "0.0.0.0/0"
      "::/0"
    }
  }
  new {
    direction = "out"
    protocol = "icmp"
    destinationIps = new Listing {
      "0.0.0.0/0"
      "::/0"
    }
  }
}
```

Fixture value, empty list:

```pkl
rules = new Listing {}
```

## Nested class

Schema declaration:

```pkl
class FirewallRule {
    direction: ("in"|"out")
    protocol: ("tcp"|"udp"|"icmp"|"esp"|"gre")
    sourceIps: Listing<String>?
    destinationIps: Listing<String>?
    port: String?
}
```

Schema field using that class:

```pkl
@hcloud.FieldHint
rules: Listing<FirewallRule>?
```

Fixture object:

```pkl
new {
  direction = "in"
  protocol = "tcp"
  port = "443"
}
```

Fixture object with nested lists:

```pkl
new {
  direction = "in"
  protocol = "tcp"
  port = "443"
  sourceIps = new Listing {
    "198.51.100.0/24"
  }
}
```

## Optional fields

In schema, `?` means the field may be omitted.

Optional string:

```pkl
location: String?
```

Optional integer:

```pkl
serverId: Int?
```

Optional boolean:

```pkl
usePrivateIP: Boolean?
```

Optional list:

```pkl
targets: Listing<LoadBalancerTarget>?
```

Optional mapping:

```pkl
labels: Mapping<String, String>?
```

Optional nested object:

```pkl
http: LoadBalancerServiceHTTP?
```

Fixture, omitted optional field:

```pkl
new floating_ip.FloatingIP {
  label = "floating-ip-\(v.testRunID)"
  type = "ipv4"
  homeLocation = v.location
}
```

Fixture, set optional field:

```pkl
new floating_ip.FloatingIP {
  label = "floating-ip-\(v.testRunID)"
  type = "ipv4"
  homeLocation = v.location
  description = "temporary conformance floating ip"
}
```

Important update behavior: this plugin treats omitted optional string fields
as "leave unchanged". Do not rely on an empty string to clear provider state.

## Required fields

Schema field:

```pkl
@hcloud.FieldHint {
    required = true
}
name: String
```

Schema field, required and create-only:

```pkl
@hcloud.FieldHint {
    required = true
    createOnly = true
}
serverType: String
```

Fixture, all required fields present:

```pkl
new server.Server {
  label = "server-\(v.testRunID)"
  name = "formae-conf-server-\(v.testRunID)"
  serverType = "cx22"
  image = "ubuntu-24.04"
}
```

## Create-only fields

Use `createOnly = true` when the hcloud API cannot update a field after
creation, or when this plugin intentionally does not implement the update.

Schema field, optional create-only:

```pkl
@hcloud.FieldHint {
    createOnly = true
}
location: String?
```

Schema field, required create-only:

```pkl
@hcloud.FieldHint {
    required = true
    createOnly = true
}
loadBalancerType: String
```

Schema field, create-only with provider default:

```pkl
@hcloud.FieldHint {
    createOnly = true
    hasProviderDefault = true
}
algorithm: String?
```

## Provider-default fields

Use `hasProviderDefault = true` when hcloud may return a value that the user
did not write in desired state.

Provider output integer:

```pkl
@hcloud.FieldHint { hasProviderDefault = true }
id: Int?
```

Provider output string:

```pkl
@hcloud.FieldHint { hasProviderDefault = true }
ipv4: String?
```

Provider labels:

```pkl
@hcloud.FieldHint { hasProviderDefault = true }
labels: Mapping<String, String>?
```

Provider-default nested list:

```pkl
@hcloud.FieldHint { hasProviderDefault = true }
services: Listing<LoadBalancerService>?
```

Provider-default nested boolean:

```pkl
@hcloud.FieldHint { hasProviderDefault = true }
redirectHTTP: Boolean?
```

Note: formae's nested provider-default propagation is not documented. For
load balancer services, this repo marks the top-level `services` field as
provider-defaulted so hcloud read-back defaults inside services do not produce
spurious diffs.

## Resource hint

Basic resource hint:

```pkl
@hcloud.ResourceHint {
    type = module.type
    identifier = "id"
    portable = true
}
open class SSHKey extends formae.Resource {
    // fields go here
}
```

Non-discoverable resource:

```pkl
@hcloud.ResourceHint {
    type = module.type
    identifier = "id"
    portable = true
    discoverable = false
}
open class Certificate extends formae.Resource {
    // fields go here
}
```

Non-portable resource:

```pkl
@hcloud.ResourceHint {
    type = module.type
    identifier = "id"
    portable = false
}
open class Image extends formae.Resource {
    // fields go here
}
```

## Resolvable output

Use this when another resource needs to reference an observed field such as
`id`, `ipv4`, or `ipv6`.

Resolvable with one output:

```pkl
open class VolumeResolvable extends formae.Resolvable {
    hidden type = module.type

    hidden id: VolumeResolvable = (this) {
        property = "id"
    }
}
```

Resolvable with several outputs:

```pkl
open class LoadBalancerResolvable extends formae.Resolvable {
    hidden type = module.type

    hidden id: LoadBalancerResolvable = (this) {
        property = "id"
    }

    hidden ipv4: LoadBalancerResolvable = (this) {
        property = "ipv4"
    }

    hidden ipv6: LoadBalancerResolvable = (this) {
        property = "ipv6"
    }
}
```

Resource-local `res` object:

```pkl
local parent = this

hidden res: LoadBalancerResolvable = new {
    label = parent.label
    stack = parent.stack?.label
}
```

## Duration strings

Pkl `Duration` is not used in this plugin's JSON path. Duration-like fields
are strings parsed by Go's `time.ParseDuration`.

Schema field:

```pkl
timeoutIdle: String?
```

Fixture value, seconds:

```pkl
timeoutIdle = "30s"
```

Fixture value, minutes:

```pkl
interval = "1m"
```

Fixture value, combined units:

```pkl
cookieLifetime = "1h30m"
```

Fixture value, health check durations:

```pkl
healthCheck = new {
  protocol = "http"
  interval = "15s"
  timeout = "10s"
  retries = 3
}
```

## Load balancer services

Schema field:

```pkl
@hcloud.FieldHint { hasProviderDefault = true }
services: Listing<LoadBalancerService>?
```

TCP service:

```pkl
services = new Listing {
  new {
    protocol = "tcp"
    listenPort = 443
    destinationPort = 443
  }
}
```

TCP service with proxy protocol explicitly disabled:

```pkl
services = new Listing {
  new {
    protocol = "tcp"
    listenPort = 443
    destinationPort = 443
    proxyProtocol = false
  }
}
```

HTTP service:

```pkl
services = new Listing {
  new {
    protocol = "http"
    listenPort = 80
    destinationPort = 8080
    http = new {
      stickySessions = false
      timeoutIdle = "30s"
    }
  }
}
```

HTTPS service:

```pkl
services = new Listing {
  new {
    protocol = "https"
    listenPort = 443
    destinationPort = 8443
    http = new {
      certificateIds = new Listing {
        123456
      }
      redirectHTTP = false
      stickySessions = false
      timeoutIdle = "30s"
    }
  }
}
```

Service with HTTP health check:

```pkl
services = new Listing {
  new {
    protocol = "http"
    listenPort = 80
    destinationPort = 8080
    healthCheck = new {
      protocol = "http"
      port = 8080
      interval = "15s"
      timeout = "10s"
      retries = 3
      http = new {
        path = "/health"
        statusCodes = new Listing {
          "200"
          "204"
        }
        tls = false
      }
    }
  }
}
```

Multiple services:

```pkl
services = new Listing {
  new {
    protocol = "http"
    listenPort = 80
    destinationPort = 8080
  }
  new {
    protocol = "https"
    listenPort = 443
    destinationPort = 8443
    http = new {
      certificateIds = new Listing {
        123456
      }
    }
  }
}
```

## Load balancer targets

Schema field:

```pkl
@hcloud.FieldHint
targets: Listing<LoadBalancerTarget>?
```

Server target:

```pkl
targets = new Listing {
  new {
    type = "server"
    serverId = 123456
  }
}
```

Server target over private IP:

```pkl
targets = new Listing {
  new {
    type = "server"
    serverId = 123456
    usePrivateIP = true
  }
}
```

Label-selector target:

```pkl
targets = new Listing {
  new {
    type = "label_selector"
    selector = "role=web"
  }
}
```

IP target:

```pkl
targets = new Listing {
  new {
    type = "ip"
    ip = "203.0.113.10"
  }
}
```

Multiple targets:

```pkl
targets = new Listing {
  new {
    type = "server"
    serverId = 123456
  }
  new {
    type = "label_selector"
    selector = "role=web"
  }
  new {
    type = "ip"
    ip = "203.0.113.10"
  }
}
```

Do not set `usePrivateIP` on an `ip` target. The plugin rejects that
combination before calling hcloud.

## Firewall rules

TCP ingress:

```pkl
rules = new Listing {
  new {
    direction = "in"
    protocol = "tcp"
    port = "22"
    sourceIps = new Listing {
      "0.0.0.0/0"
      "::/0"
    }
  }
}
```

UDP ingress:

```pkl
rules = new Listing {
  new {
    direction = "in"
    protocol = "udp"
    port = "53"
    sourceIps = new Listing {
      "0.0.0.0/0"
      "::/0"
    }
  }
}
```

ICMP ingress:

```pkl
rules = new Listing {
  new {
    direction = "in"
    protocol = "icmp"
    sourceIps = new Listing {
      "0.0.0.0/0"
      "::/0"
    }
  }
}
```

Outbound rule:

```pkl
rules = new Listing {
  new {
    direction = "out"
    protocol = "tcp"
    port = "443"
    destinationIps = new Listing {
      "0.0.0.0/0"
      "::/0"
    }
  }
}
```

Multiple rules:

```pkl
rules = new Listing {
  new {
    direction = "in"
    protocol = "tcp"
    port = "22"
    sourceIps = new Listing {
      "0.0.0.0/0"
      "::/0"
    }
  }
  new {
    direction = "in"
    protocol = "icmp"
    sourceIps = new Listing {
      "0.0.0.0/0"
      "::/0"
    }
  }
  new {
    direction = "out"
    protocol = "tcp"
    port = "443"
    destinationIps = new Listing {
      "0.0.0.0/0"
      "::/0"
    }
  }
}
```

## Full copy-paste resource examples

Server:

```pkl
new server.Server {
  label = "server-\(v.testRunID)"
  name = "formae-conf-server-\(v.testRunID)"
  serverType = read?("env:HCLOUD_SERVER_TYPE") ?? "cx22"
  image = read?("env:HCLOUD_SERVER_IMAGE") ?? "ubuntu-24.04"
  location = v.location
  labels = new Mapping {
    ["owner"] = "formae-conformance-test"
  }
}
```

SSH key:

```pkl
new ssh_key.SSHKey {
  label = "ssh-key-\(v.testRunID)"
  name = "formae-conf-ssh-key-\(v.testRunID)"
  publicKey = v.publicSSHKey
  labels = new Mapping {
    ["owner"] = "formae-conformance-test"
  }
}
```

Network:

```pkl
new network.Network {
  label = "network-\(v.testRunID)"
  name = "formae-conf-network-\(v.testRunID)"
  ipRange = "10.0.0.0/16"
  labels = new Mapping {
    ["owner"] = "formae-conformance-test"
  }
}
```

Volume:

```pkl
new volume.Volume {
  label = "volume-\(v.testRunID)"
  name = "formae-conf-volume-\(v.testRunID)"
  size = 10
  location = v.location
  labels = new Mapping {
    ["owner"] = "formae-conformance-test"
  }
}
```

Firewall:

```pkl
new firewall.Firewall {
  label = "firewall-\(v.testRunID)"
  name = "formae-conf-firewall-\(v.testRunID)"
  rules = new Listing {
    new {
      direction = "in"
      protocol = "tcp"
      port = "22"
      sourceIps = new Listing {
        "0.0.0.0/0"
        "::/0"
      }
    }
  }
  labels = new Mapping {
    ["owner"] = "formae-conformance-test"
  }
}
```

Load balancer:

```pkl
new load_balancer.LoadBalancer {
  label = "load-balancer-\(v.testRunID)"
  name = "formae-conf-load-balancer-\(v.testRunID)"
  loadBalancerType = read?("env:HCLOUD_LB_TYPE") ?? "lb11"
  location = v.location
  services = new Listing {
    new {
      protocol = "tcp"
      listenPort = 443
      destinationPort = 443
      proxyProtocol = false
    }
  }
  labels = new Mapping {
    ["owner"] = "formae-conformance-test"
  }
}
```

Primary IP:

```pkl
new primary_ip.PrimaryIP {
  label = "primary-ip-\(v.testRunID)"
  name = "formae-conf-primary-ip-\(v.testRunID)"
  type = "ipv4"
  location = v.location
  autoDelete = false
  labels = new Mapping {
    ["owner"] = "formae-conformance-test"
  }
}
```

Floating IP:

```pkl
new floating_ip.FloatingIP {
  label = "floating-ip-\(v.testRunID)"
  type = "ipv4"
  description = "formae conformance test floating ip"
  homeLocation = v.location
  labels = new Mapping {
    ["owner"] = "formae-conformance-test"
  }
}
```

Placement group:

```pkl
new placement_group.PlacementGroup {
  label = "placement-group-\(v.testRunID)"
  name = "formae-conf-placement-group-\(v.testRunID)"
  type = "spread"
  labels = new Mapping {
    ["owner"] = "formae-conformance-test"
  }
}
```

Image:

```pkl
new image.Image {
  label = "image-\(v.testRunID)"
  server = 123456
  description = "formae conformance test image"
  type = "snapshot"
  labels = new Mapping {
    ["owner"] = "formae-conformance-test"
  }
}
```
