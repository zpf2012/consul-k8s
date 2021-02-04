package consul

import (
	"fmt"

	"github.com/hashicorp/consul-k8s/version"
	capi "github.com/hashicorp/consul/api"
)

// NewClient returns a Consul API client. It adds a required User-Agent
// header that describes the version of consul-k8s making the call.
func NewClient(config *capi.Config) (*capi.Client, error) {
	client, err := capi.NewClient(config)
	if err != nil {
		return nil, err
	}
	client.AddHeader("User-Agent", fmt.Sprintf("consul-k8s/%s", version.GetHumanVersion()))
	return client, nil
}

// DefaultConfig returns a default configuration for the client. By default this
// will pool and reuse idle connections to Consul. If you have a long-lived
// client object, this is the desired behavior and should make the most efficient
// use of the connections to Consul. If you don't reuse a client object, which
// is not recommended, then you may notice idle connections building up over
// time. To avoid this, use the DefaultNonPooledConfig() instead.
func DefaultConfig() *capi.Config {
	return capi.DefaultConfig()
}
