// Copyright (c) 2017-2019, AT&T Intellectual Property. All rights reserved.
//
// Copyright (c) 2017 by Brocade Communications Systems, Inc.
// All rights reserved.
//
// SPDX-License-Identifier: GPL-2.0-only

// provisiond - legacy provisioning daemon for configd.

package main

import (
	"github.com/danos/provisiond"
	"github.com/danos/vci"
)

func main() {
	state := provisiond.NewState()
	config := provisiond.NewConfig(state)
	rpcs := provisiond.NewRPCMap()
	comp := vci.NewComponent("net.vyatta.vci.config.provisiond")
	model := comp.Model("net.vyatta.vci.config.provisiond.v1").
		Config(config).
		State(state)
	for moduleName, moduleRpcs := range rpcs {
		model.RPC(moduleName, moduleRpcs)
	}
	comp.Run()

	comp.Wait()
}
