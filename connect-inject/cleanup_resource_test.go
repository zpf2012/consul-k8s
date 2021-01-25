package connectinject

import (
	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/consul/sdk/testutil"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"testing"
	"time"
)

var consulTestPod = &corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "consul",
		Namespace: "default",
	},
	Spec: corev1.PodSpec{
		Containers: []corev1.Container{
			corev1.Container{
				Name: "consul",
			},
		},
	},
}

func TestOrphans_Run(t *testing.T) {
	// Pod Does not exist, service exists, service is cleaned up
	t.Parallel()
	var err error
	require := require.New(t)

	// Get a server and client
	server, err := testutil.NewTestServerConfigT(t, nil)
	defer server.Stop()
	require.NoError(err)

	clientConfig := &api.Config{Address: server.HTTPAddr}
	client, err := api.NewClient(clientConfig)
	require.NoError(err)

	// register a service
	server.AddService(t, testServiceNameReg, api.HealthPassing, nil)

	service, _, err := client.Agent().Service(testServiceNameReg, nil)
	require.NoError(err)
	require.NotNil(t, service)

	cleanupResource := CleanupResource{
		Log:                 hclog.Default().Named("cleanupResource"),
		KubernetesClientset: fake.NewSimpleClientset(consulTestPod),
		Client:              client,
		ReconcilePeriod:     1 * time.Second,
	}
	cleanupResource.Reconcile()

	// ensure the service doesnt exist
	services, _, err := client.Catalog().Service(testServiceNameReg, "", nil)
	require.NoError(err)
	require.Nil(t, services)
}
