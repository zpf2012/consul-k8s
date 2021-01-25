package connectinject

import (
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/go-hclog"
	"golang.org/x/net/context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type CleanupResource struct {
	Log                 hclog.Logger
	KubernetesClientset kubernetes.Interface

	Client *api.Client
	// ReconcilePeriod is the period by which reconcile gets called.
	// default to 1 minute.
	ReconcilePeriod time.Duration

	Ctx  context.Context
	lock sync.Mutex
}

// Run is the long-running runloop for periodically running Reconcile.
// It initially reconciles at startup and is then invoked after every
// ReconcilePeriod expires.
func (c *CleanupResource) Run(stopCh <-chan struct{}) {
	err := c.Reconcile()
	if err != nil {
		c.Log.Error("reconcile returned an error", "err", err)
	}

	reconcileTimer := time.NewTimer(c.ReconcilePeriod)
	defer reconcileTimer.Stop()

	for {
		select {
		case <-stopCh:
			c.Log.Info("received stop signal, shutting down")
			return

		case <-reconcileTimer.C:
			if err := c.Reconcile(); err != nil {
				c.Log.Error("reconcile returned an error", "err", err)
			}
			reconcileTimer.Reset(c.ReconcilePeriod)
		}
	}
}

// Delete is not implemented because it is handled by the preStop phase whereby all services
// related to the pod are deregistered which also deregisters health checks.
func (c *CleanupResource) Delete(string) error {
	return nil
}

// Informer starts a sharedindex informer which watches and lists corev1.Pod objects
// which meet the filter of labelInject.
func (c *CleanupResource) Informer() cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc:  nil,
			WatchFunc: nil,
		},
		&corev1.Pod{}, // the target type (Pod)
		0,             // no resync (period of 0)
		cache.Indexers{},
	)
}

// Upsert processes a create or update event.
// Two primary use cases are handled, new pods will get a new consul TTL health check
// registered against their respective agent and service, and updates to pods will have
// this TTL health check updated to reflect the pod's readiness status.
func (c *CleanupResource) Upsert(key string, raw interface{}) error {
	return nil
}

// Reconcile iterates through all Pods with the appropriate label and compares the
// current health check status against that which is stored in Consul and updates
// the consul health check accordingly. If the health check doesn't yet exist it will create it.
func (c *CleanupResource) Reconcile() error {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.Log.Error("starting reconcile")
	var podMap map[string]bool
	var deregList []*api.CatalogService

	// Step 1 : get all known registered services
	servicesList, _, err := c.Client.Catalog().Services(nil)
	if err != nil {
		c.Log.Error("unable to get Consul Services", "error", err)
	}
	// Step 2 : get all Pods with our label
	podList, err := c.KubernetesClientset.CoreV1().Pods(corev1.NamespaceAll).List(c.Ctx,
		metav1.ListOptions{LabelSelector: labelInject})
	if err != nil {
		c.Log.Error("unable to get pods", "err", err)
		return err
	}
	// build a map of pod names
	for _, pod := range podList.Items {
		podMap[pod.Name] = true
	}

	// Step 3 : for each registered service, find the associated pod
	for serviceName := range servicesList {
		c.Log.Error("======= services found:", "svc", serviceName)
		service, _, err := c.Client.Catalog().Service(serviceName, "", nil)
		if err != nil {
			c.Log.Error("unable to get Consul Service", "error", err)
		}
		servicePodName := service[0].ServiceMeta["pod-name"]
		if _, ok := podMap[servicePodName]; !ok {
			c.Log.Error("Service is no longer backed by a pod, marking for deregister")
			deregList = append(deregList, service[0])
		}
	}

	// Step 4 : if the pod no longer exists, deregister the pod
	for _, svc := range deregList {
		c.Log.Error("Deregistering service", "service", svc.ServiceID)
		dereg := &api.CatalogDeregistration{
			Node:       svc.Node,
			Address:    svc.Address,
			Datacenter: svc.Datacenter,
			ServiceID:  svc.ServiceID,
			Namespace:  svc.Namespace,
		}
		_, err := c.Client.Catalog().Deregister(dereg, nil)
		if err != nil {
			c.Log.Error("Unable to deregister service", "service", svc.ServiceID, "error", err)
		}
	}
	c.Log.Debug("finished reconcile")
	return nil
}
