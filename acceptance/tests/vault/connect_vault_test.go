package vault

import (
	"testing"
	"time"

	"github.com/hashicorp/consul-k8s/acceptance/framework/consul"
	"github.com/hashicorp/consul-k8s/acceptance/framework/logger"
	"github.com/hashicorp/consul-k8s/acceptance/framework/vault"
	"github.com/stretchr/testify/require"
)

// Installs Vault, bootstraps it with secrets, policies, and kube auth method
// then creates a gossip encryption secret and uses this to bootstrap Consul
func TestVaultConsulGossipEncryptionKeyIntegration(t *testing.T) {
	cfg := suite.Config()
	ctx := suite.Environment().DefaultContext(t)

	vaultReleaseName := "vault" // helpers.RandomName()
	logger.Log(t, "Entering NewHelmCluster")
	vaultCluster := vault.NewHelmCluster(t, nil, ctx, cfg, vaultReleaseName)
	logger.Log(t, "Entering Create")
	vaultCluster.Create(t)
	// Vault is now installed in the cluster

	logger.Log(t, "Entering Bootstrap")
	vaultCluster.Bootstrap(t, ctx)
	logger.Log(t, "Finished Bootstrap")

	vaultClient := vaultCluster.VaultClient(t)

	logger.Log(t, "Creating the policies and secrets")

	/*
	   vault kv put secret/consul/gossip gossip='3R7oLrdpkk2V0Y7yHLizyxXeS2RtaVuy07DkU15Lhws='

	   vault policy write consul-gossip - <<EOF
	   path "secret/data/consul/gossip" {
	     capabilities = ["read"]
	   }
	   EOF

	   # - server needs a copy of the gossip key
	   vault write auth/kubernetes/role/consul-server \
	           bound_service_account_names=consul-consul-server \
	           bound_service_account_namespaces=default \
	           policies=consul-gossip \
	           ttl=24h

	   # - client needs a copy of the gossip key
	   vault write auth/kubernetes/role/consul-client \
	           bound_service_account_names=consul-consul-client \
	           bound_service_account_namespaces=default \
	           policies=consul-gossip \
	           ttl=24h

	*/
	var err error
	// Create the Vault Policy for the gossip key
	logger.Log(t, "Creating the gossip policy")
	rules := `
path "consul/data/secret/gossip" {
  capabilities = ["read"]
}`
	err = vaultClient.Sys().PutPolicy("consul-gossip", rules)
	require.NoError(t, err)

	// create the auth roles for consul-server + consul-client
	logger.Log(t, "Creating the gossip auth roles")
	params := map[string]interface{}{
		"bound_service_account_names":      "consul-consul-client",
		"bound_service_account_namespaces": "default",
		"policies":                         "consul-gossip",
		"ttl":                              "24h",
	}
	_, err = vaultClient.Logical().Write("auth/kubernetes/role/consul-client", params)
	require.NoError(t, err)

	params = map[string]interface{}{
		"bound_service_account_names":      "consul-consul-server",
		"bound_service_account_namespaces": "default",
		"policies":                         "consul-gossip",
		"ttl":                              "24h",
	}
	_, err = vaultClient.Logical().Write("auth/kubernetes/role/consul-server", params)
	require.NoError(t, err)

	// Create the gossip key
	logger.Log(t, "Creating the gossip secret")
	params = map[string]interface{}{
		"data": map[string]interface{}{
			"gossip": "3R7oLrdpkk2V0Y7yHLizyxXeS2RtaVuy07DkU15Lhws=",
		},
	}
	_, err = vaultClient.Logical().Write("consul/data/secret/gossip", params)
	require.NoError(t, err)

	time.Sleep(time.Second * 30)

	consulHelmValues := map[string]string{
		"server.enabled":  "true",
		"server.replicas": "1",
		"global.imageK8S": "kschoche/consul-k8s-hashiconf",

		"connectInject.enabled": "true",

		"secretsBackend.vault.enabled":          "true",
		"secretsBackend.vault.consulServerRole": "consul-server",
		"secretsBackend.vault.consulclientRole": "consul-client",

		"global.tls.enabled":                 "true",
		"global.gossipEncryption.secretName": "consul/data/secret/gossip",
		"global.gossipEncryption.secretKey":  ".Data.data.gossip",
	}
	consulReleaseName := "consul" //helpers.RandomName()
	logger.Log(t, "Installing Consul")
	consulCluster := consul.NewHelmCluster(t, consulHelmValues, ctx, cfg, consulReleaseName)

	consulCluster.Create(t)

	time.Sleep(time.Second * 600)

	// Do some shit
	// maybe rotate a thing
}
