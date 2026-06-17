// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: a formae resource plugin for Hetzner Cloud.
package main

import "github.com/platform-engineering-labs/formae/pkg/plugin/sdk"

func main() {
	sdk.RunWithManifest(&Plugin{}, sdk.RunConfig{})
}
