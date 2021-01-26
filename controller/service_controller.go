package controller

import (
	"context"

	"github.com/go-logr/logr"
	connectinject "github.com/hashicorp/consul-k8s/connect-inject"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// todo
type ServiceController struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func (r *ServiceController) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	var svc corev1.Service

	err := r.Client.Get(context.Background(), req.NamespacedName, &svc)
	if err != nil {
		panic(err)
	}
	r.Log.Info("retrieved service from kube", "svc", svc)

	// get endpoints
	var endpoints corev1.Endpoints
	err = r.Client.Get(context.Background(), req.NamespacedName, &endpoints)
	if err != nil {
		panic(err)
	}

	for _, subset := range endpoints.Subsets {
		for _, address := range subset.Addresses {
			if address.TargetRef.Kind == "Pod" {
				var pod corev1.Pod
				objectKey := types.NamespacedName{Name: address.TargetRef.Name, Namespace: address.TargetRef.Namespace}
				err = r.Client.Get(context.Background(), objectKey, &pod)
				if err != nil {
					panic(err)
				}

				if _, ok := pod.ObjectMeta.Annotations[connectinject.AnnotationInject]; ok {
					r.Log.Info("found service with connect pod annotations", "service", req.NamespacedName, "pod", pod.Name)
				}
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *ServiceController) Logger(name types.NamespacedName) logr.Logger {
	return r.Log.WithValues("request", name)
}

func (r *ServiceController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		Complete(r)
}
