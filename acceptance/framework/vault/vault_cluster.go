package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/helm"
	terratestk8s "github.com/gruntwork-io/terratest/modules/k8s"
	terratestLogger "github.com/gruntwork-io/terratest/modules/logger"
	"github.com/hashicorp/consul-k8s/acceptance/framework/config"
	"github.com/hashicorp/consul-k8s/acceptance/framework/environment"
	"github.com/hashicorp/consul-k8s/acceptance/framework/helpers"
	"github.com/hashicorp/consul-k8s/acceptance/framework/k8s"
	"github.com/hashicorp/consul-k8s/acceptance/framework/logger"
	"github.com/hashicorp/consul/sdk/testutil/retry"
	"k8s.io/apimachinery/pkg/api/errors"
	// https://github.com/hashicorp/vault-examples/tree/main/go
	vapi "github.com/hashicorp/vault/api"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

//var suite testsuite.Suite

// Cluster represents a consul cluster object
type Cluster interface {
	Create(t *testing.T)
	Bootstrap(t *testing.T, ctx environment.TestContext)
	Destroy(t *testing.T)
	SetupVaultClient(t *testing.T) *vapi.Client
	VaultClient(t *testing.T) *vapi.Client
}

const (
	VaultReleaseName = "vault"
	vaultNS          = "default"
)

// VaultCluster
type VaultCluster struct {
	ctx       environment.TestContext
	namespace string

	vaultHelmOptions *helm.Options
	vaultReleaseName string
	vaultClient      *vapi.Client
	rootToken        string

	kubectlOptions *terratestk8s.KubectlOptions
	values         map[string]string

	kubernetesClient kubernetes.Interface
	kubeConfig       string
	kubeContext      string

	noCleanupOnFailure bool
	debugDirectory     string
	logger             terratestLogger.TestLogger
}

func NewHelmCluster(
	t *testing.T,
	helmValues map[string]string,
	ctx environment.TestContext,
	cfg *config.TestConfig,
	releaseName string,
) Cluster {

	logger := terratestLogger.New(logger.TestLogger{})

	kopts := ctx.KubectlOptions(t)
	kopts.Namespace = vaultNS

	vaultHelmOpts := &helm.Options{
		SetValues:      defaultVaultValues(),
		KubectlOptions: kopts,
		Logger:         logger,
	}

	return &VaultCluster{
		ctx:                ctx,
		vaultHelmOptions:   vaultHelmOpts,
		kubectlOptions:     kopts,
		namespace:          cfg.KubeNamespace,
		values:             helmValues,
		kubernetesClient:   ctx.KubernetesClient(t),
		kubeConfig:         cfg.Kubeconfig,
		kubeContext:        cfg.KubeContext,
		noCleanupOnFailure: cfg.NoCleanupOnFailure,
		debugDirectory:     cfg.DebugDirectory,
		logger:             logger,
		vaultReleaseName:   releaseName,
	}
}

func (v *VaultCluster) VaultClient(t *testing.T) *vapi.Client { return v.vaultClient }

// checkForPriorInstallations checks if there is an existing Helm release
// for this Helm chart already installed. If there is, it fails the tests.
func (v *VaultCluster) checkForPriorVaultInstallations(t *testing.T) {
	t.Helper()

	v.logger.Logf(t, "===== entering checkforpriorvaultinstallations()")
	var helmListOutput string
	// Check if there's an existing cluster and fail if there is one.
	// We may need to retry since this is the first command run once the Kube
	// cluster is created and sometimes the API server returns errors.
	retry.RunWith(&retry.Counter{Wait: 1 * time.Second, Count: 3}, t, func(r *retry.R) {
		var err error
		// NOTE: It's okay to pass in `t` to RunHelmCommandAndGetOutputE despite being in a retry
		// because we're using RunHelmCommandAndGetOutputE (not RunHelmCommandAndGetOutput) so the `t` won't
		// get used to fail the test, just for logging.
		helmListOutput, err = helm.RunHelmCommandAndGetOutputE(t, v.vaultHelmOptions, "list", "--output", "json")
		require.NoError(r, err)
	})

	var installedReleases []map[string]string

	err := json.Unmarshal([]byte(helmListOutput), &installedReleases)
	require.NoError(t, err, "unmarshalling %q", helmListOutput)

	for _, r := range installedReleases {
		require.NotContains(t, r["chart"], "vault", fmt.Sprintf("detected an existing installation of Vault %s, release name: %s", r["chart"], r["name"]))
	}
	/*
		// TODO: is this necessary for vault?
		// Wait for all pods in the "default" namespace to exit. A previous
		// release may not be listed by Helm but its pods may still be terminating.
		retry.RunWith(&retry.Counter{Wait: 1 * time.Second, Count: 60}, t, func(r *retry.R) {
			consulPods, err := v.kubernetesClient.CoreV1().Pods(v.vaultHelmOptions.KubectlOptions.Namespace).List(context.Background(), metav1.ListOptions{})
			require.NoError(r, err)
			if len(consulPods.Items) > 0 {
				var podNames []string
				for _, p := range consulPods.Items {
					podNames = append(podNames, p.Name)
				}
				r.Errorf("pods from previous installation still running: %s", strings.Join(podNames, ", "))
			}
		})
	*/
}

func (v *VaultCluster) SetupVaultClient(t *testing.T) *vapi.Client {
	t.Helper()

	config := vapi.DefaultConfig()
	localPort := terratestk8s.GetAvailablePort(t)
	remotePort := 8200 // use non-secure by default

	serverPod := fmt.Sprintf("%s-0", v.vaultReleaseName)
	tunnel := terratestk8s.NewTunnelWithLogger(
		v.vaultHelmOptions.KubectlOptions,
		terratestk8s.ResourceTypePod,
		serverPod,
		localPort,
		remotePort,
		v.logger)

	// Retry creating the port forward since it can fail occasionally.
	retry.RunWith(&retry.Counter{Wait: 1 * time.Second, Count: 3}, t, func(r *retry.R) {
		// NOTE: It's okay to pass in `t` to ForwardPortE despite being in a retry
		// because we're using ForwardPortE (not ForwardPort) so the `t` won't
		// get used to fail the test, just for logging.
		require.NoError(r, tunnel.ForwardPortE(t))
	})

	t.Cleanup(func() {
		tunnel.Close()
	})

	config.Address = fmt.Sprintf("http://127.0.0.1:%d", localPort)
	consulClient, err := vapi.NewClient(config)
	require.NoError(t, err)

	return consulClient
}

// Bootstrap runs Init, Unseals the Vault installation, setups up the Auth methods and enables the Secrets Engines
func (v *VaultCluster) Bootstrap(t *testing.T, ctx environment.TestContext) {

	v.vaultClient = v.SetupVaultClient(t)

	// Init the Vault Cluster and store the rootToken
	initResp, err := v.vaultClient.Sys().Init(&vapi.InitRequest{
		SecretShares:      1,
		SecretThreshold:   1,
		StoredShares:      1,
		PGPKeys:           nil,
		RecoveryShares:    0,
		RecoveryThreshold: 0,
		RecoveryPGPKeys:   nil,
		RootTokenPGPKey:   "",
	})
	if err != nil {
		t.Fatal("unable to init Vault cluster", "err", err)
	}
	v.rootToken = initResp.RootToken
	v.vaultClient.SetToken(v.rootToken)

	// Unseal the Vault Cluster using the Unseal Keys from Init()
	sealResp, err := v.vaultClient.Sys().Unseal(initResp.KeysB64[0])
	if err != nil {
		t.Fatal("unable to init Vault cluster", "err", err)
	}
	require.Equal(t, false, sealResp.Sealed)

	// Enable the KV-V2 Secrets engine
	err = v.vaultClient.Sys().Mount("consul", &vapi.MountInput{
		Type:   "kv-v2",
		Config: vapi.MountConfigInput{},
	})
	if err != nil {
		t.Fatal("unable to mount kv-v2 secrets engine", "err", err)
	}

	// Enable Kube Auth
	err = v.vaultClient.Sys().EnableAuthWithOptions("kubernetes", &vapi.EnableAuthOptions{
		Type:                  "kubernetes",
		Description:           "",
		Config:                vapi.MountConfigInput{},
		Local:                 false,
		SealWrap:              false,
		ExternalEntropyAccess: false,
		Options:               nil,
		PluginName:            "",
	})
	if err != nil {
		t.Fatal("unable to enable kube auth", "err", err)
	}
	/*
		// get the vault agent injector svc IP
		svc, err := v.kubernetesClient.CoreV1().Services(v.vaultHelmOptions.KubectlOptions.Namespace).Get(context.Background(), "vault-agent-injector-svc", metav1.GetOptions{})
		require.NoError(t, err)
		injectorServiceIP := svc.Spec.ClusterIP

		// fetch the serviceaccount token for the vault agent injector
		svcaccount, err := v.kubernetesClient.CoreV1().ServiceAccounts(v.vaultHelmOptions.KubectlOptions.Namespace).Get(context.Background(), "vault-agent-injector", metav1.GetOptions{})
		require.NoError(t, err)
		secret, err := v.kubernetesClient.CoreV1().Secrets(v.vaultHelmOptions.KubectlOptions.Namespace).Get(context.Background(), svcaccount.Secrets[0].Name, metav1.GetOptions{})
		require.NoError(t, err)

		b64token := base64.StdEncoding.EncodeToString(secret.Data["token"])
		cmdString := fmt.Sprintf("VAULT_TOKEN=%s vault write auth/kubernetes/config token_reviewer_jwt=\"%s\" kubernetes_host=\"https://%s:443\" kubernetes_ca_cert=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt", v.rootToken, b64token, injectorServiceIP)
	*/
	// We need to kubectl exec this one on the vault server:
	cmdString := fmt.Sprintf("VAULT_TOKEN=%s vault write auth/kubernetes/config token_reviewer_jwt=\"$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)\" kubernetes_host=\"https://${KUBERNETES_PORT_443_TCP_ADDR}:443\" kubernetes_ca_cert=@/var/run/secrets/kubernetes.io/serviceaccount/ca.crt", v.rootToken)

	v.logger.Logf(t, "updating vault kube auth config")
	time.Sleep(time.Second * 30)
	k8s.RunKubectl(t, ctx.KubectlOptions(t), "exec", "-i", "vault-0", "--", "sh", "-c", cmdString)

	// TODO: Do we need this policy, because we will have to have root token (or some other priv'd) in our client
	// TODO: already in order to create policies and roles.

	// Create an access policy for Secrets Management, so we can create them later
	/*
		devpolicyRule := `{ "policy": "path "consul/data/*" { capabilities = ["create", "update", "read"] } }`
		err = v.vaultClient.Sys().PutPolicy("dev-root-policy", devpolicyRule)
		if err != nil {
			t.Fatal("unable to create secret mgmt policy", "err", err)
		}
	*/
}

func (v *VaultCluster) Create(t *testing.T) {
	t.Helper()
	v.logger.Logf(t, "===== entering Create()")

	// Make sure we delete the cluster if we receive an interrupt signal and
	// register cleanup so that we delete the cluster when test finishes.
	helpers.Cleanup(t, v.noCleanupOnFailure, func() {
		v.Destroy(t)
	})

	// Fail if there are any existing installations of the Helm chart.
	v.checkForPriorVaultInstallations(t)

	// step 1: install Vault
	helm.Install(t, v.vaultHelmOptions, "hashicorp/vault", v.vaultReleaseName)
	// NOTE: If we do not use dev-mode the vault pods will not be Ready until they are unsealed
	helpers.WaitForAllPodsToBeReady(t, v.kubernetesClient, v.vaultHelmOptions.KubectlOptions.Namespace, fmt.Sprintf("release=%s", v.vaultReleaseName))
	time.Sleep(30 * time.Second)
}

func (v *VaultCluster) Destroy(t *testing.T) {
	v.logger.Logf(t, "===== entering Destroy()")
	t.Helper()

	k8s.WritePodsDebugInfoIfFailed(t, v.kubectlOptions, v.debugDirectory, "release="+v.vaultReleaseName)

	// Ignore the error returned by the helm delete here so that we can
	// always idempotently clean up resources in the cluster.
	_ = helm.DeleteE(t, v.vaultHelmOptions, v.vaultReleaseName, false)
	// Delete PVCs.
	err := v.kubernetesClient.CoreV1().PersistentVolumeClaims(v.vaultHelmOptions.KubectlOptions.Namespace).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: "name=" + v.vaultReleaseName})
	require.NoError(t, err)

	// Delete any secrets that have v.releaseName in their name.
	secrets, err := v.kubernetesClient.CoreV1().Secrets(v.vaultHelmOptions.KubectlOptions.Namespace).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	for _, secret := range secrets.Items {
		if strings.Contains(secret.Name, v.vaultReleaseName) {
			err := v.kubernetesClient.CoreV1().Secrets(v.vaultHelmOptions.KubectlOptions.Namespace).Delete(context.Background(), secret.Name, metav1.DeleteOptions{})
			if !errors.IsNotFound(err) {
				require.NoError(t, err)
			}
		}
	}
}

/*
func (v *VaultCluster) SetupVaultClient(t *testing.T) *vapi.Client {
	t.Helper()
	config := vapi.DefaultConfig()

	client, err := vapi.NewClient(config)
	if err != nil {
		t.Fatalf("Failed to initialize Vault client: %v", err)
	}
	// TODO: TLS/ACLs need to be sorted

	// Read the service-account token from the path where the token's Kubernetes Secret is mounted.
	// By default, Kubernetes will mount this to /var/run/secrets/kubernetes.io/serviceaccount/token
	// but an administrator may have configured it to be mounted elsewhere.
	jwt, err := os.ReadFile(" /var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		v.logger.Logf(t, "unable to read file containing service account token: %w", err)
	}

	params := map[string]interface{}{
		"jwt":  string(jwt),
		"role": "dev-role-k8s", // the name of the role in Vault that was created with this app's Kubernetes service account bound to it
	}

	// log in to Vault's Kubernetes auth method
	resp, err := client.Logical().Write("auth/kubernetes/login", params)
	if err != nil {
		v.logger.Logf(t, "unable to log in with Kubernetes auth: %w", err)
	}
	if resp == nil || resp.Auth == nil || resp.Autv.ClientToken == "" {
		v.logger.Logf(t, "login response did not return client token")
	}

	// now you will use the resulting Vault token for making all future calls to Vault
	client.SetToken(resp.Autv.ClientToken)

	return client
}
*/

func defaultVaultValues() map[string]string {
	values := map[string]string{
		"server.replicas":        "1",
		"server.bootstrapExpect": "1",
		"ui.enabled":             "true",
		//"server.dev.enabled":     "true", // TODO: is it 'true' or true?
		"injector.enabled": "true",
		"global.enabled":   "true",
	}
	return values
}
