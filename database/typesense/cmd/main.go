package main

import (
	"log"
	"os"

	"github.com/openbao/openbao-plugins/database/typesense"
	"github.com/openbao/openbao/api/v2"
	dbplugin "github.com/openbao/openbao/sdk/v2/database/dbplugin/v5"
)

func main() {
	apiClientMeta := &api.PluginAPIClientMeta{}
	flags := apiClientMeta.FlagSet()
	flags.Parse(os.Args[1:])

	if err := Run(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func Run() error {
	// ServeMultiplex directly takes the factory function in the v5 interface
	dbplugin.ServeMultiplex(typesense.New)
	return nil
}
