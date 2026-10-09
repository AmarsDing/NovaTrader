package data

import (
	"server/conf"

	"github.com/go-kratos/kratos/contrib/registry/discovery/v2"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewDiscovery, NewNatsCoreRepo)

func NewDiscovery(conf *conf.Discovery) registry.Discovery {
	return discovery.New(&discovery.Config{
		Nodes:  conf.Nodes,
		Env:    conf.Env,
		Region: conf.Region,
		Zone:   conf.Zone,
		Host:   conf.Host,
	})
}
