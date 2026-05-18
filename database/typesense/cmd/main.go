// Copyright (c) 2026 OpenBao a Series of LF Projects, LLC
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"

	"github.com/openbao/openbao-plugins/database/typesense"
	"github.com/openbao/openbao/api/v2"
	dbplugin "github.com/openbao/openbao/sdk/v2/database/dbplugin/v5"
)

func main() {
	apiClientMeta := &api.PluginAPIClientMeta{}
	flags := apiClientMeta.FlagSet()
	flags.Parse(os.Args[1:])

	// ServeMultiplex directly takes the factory function in the v5 interface
	dbplugin.ServeMultiplex(typesense.New)
}
