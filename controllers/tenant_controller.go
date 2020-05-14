/*
Copyright 2020 jdwl.in.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"
	operatorsv1alpha1 "jdwl.in/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TenantReconciler reconciles a Tenant object
type TenantReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=operators.jdwl.in,resources=tenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operators.jdwl.in,resources=tenants/status,verbs=get;update;patch

// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=list;watch;get;patch
// +kubebuilder:rbac:groups=core,resources=services,verbs=list;watch;get;patch

func (r *TenantReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("tenant", req.NamespacedName)

	var tenant operatorsv1alpha1.Tenant
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// goal: get namespace status, if it's been terminating, just return, no more reconciling
	// TODO
	// make this controller run in cluster and out of cluster of cluster (make run)
	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			panic(err)
		}
	}
	clientset, err := kubernetes.NewForConfig(config)

	// query namespace by name, if not exist, create it
	ns, err := clientset.CoreV1().Namespaces().Get(tenant.Namespace, metav1.GetOptions{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(), Kind: "Namespace",
		},
	})

	if ns.Status.Phase == corev1.NamespaceTerminating {
		return ctrl.Result{}, nil
	}

	// your logic here
	log.Info("reconciling tenant")

	// nsSpec := &corev1.Namespace{
	// 	ObjectMeta: metav1.ObjectMeta{
	// 		Name: tenant.Spec.UUID,
	// 		Labels: map[string]string{
	// 			"namespace": tenant.Spec.UUID,
	// 		},
	// 	},
	// }
	// found := false
	// // Fetch namespace list
	// nsList := &corev1.NamespaceList{}
	// err := r.List(context.TODO(), nsList, &client.ListOptions{})
	// if err != nil {
	// 	return ctrl.Result{}, err
	// }

	// for _, v := range nsList.Items {
	// 	fmt.Println(v.Name)
	// 	if v.Name == nsSpec.Name {
	// 		found = true
	// 	}
	// }

	// if !found {
	// 	if err := r.Client.Create(ctx, nsSpec); err != nil {
	// 		fmt.Println("failed to create namespace")
	// 		fmt.Println(err)
	// 		os.Exit(1)
	// 	}
	// }

	// tenantNs := &corev1.Namespace{
	// 	TypeMeta: metav1.TypeMeta{
	// 		APIVersion: corev1.SchemeGroupVersion.String(),
	// 		Kind:       "Namespace",
	// 	},
	// 	ObjectMeta: metav1.ObjectMeta{
	// 		Name: tenantNsName,
	// 		Annotations: map[string]string{
	// 			TenantAdminNamespaceAnnotation: instance.Namespace,
	// 		},
	// 		OwnerReferences: []metav1.OwnerReference{expectedOwnerRef},
	// 	},
	// }
	// if err = r.Client.Create(context.TODO(), tenantNs); err != nil {
	// 	return reconcile.Result{}, err
	// }

	// client.
	// nsSpec := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tenant.ObjectMeta.Namespace}}
	// // if ns, err := clientset.Core().Namespaces().Create(nsSpec); err != nil {
	// if ns, err := corev1.Namespaces().Create(nsSpec); err != nil {
	// 	log.Error(err, "unable to create namespace")
	// 	return ctrl.Result{}, err
	// }

	// var tenants operatorsv1alpha1.Tenant
	// if err := r.List(ctx, &tenants, client.InNamespace(req.Namespace)); err != nil {
	// 	log.Error(err, "unable to list tenants")
	// 	return ctrl.Result{}, err
	// }

	deployment, err := r.desiredDeployment(tenant)
	if err != nil {
		return ctrl.Result{}, err
	}

	applyOpts := []client.PatchOption{client.ForceOwnership, client.FieldOwner("tenant-controller")}
	err = r.Patch(ctx, &deployment, client.Apply, applyOpts...)
	if err != nil {
		return ctrl.Result{}, err
	}

	svc, err := r.desiredService(tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = r.Patch(ctx, &svc, client.Apply, applyOpts...)
	if err != nil {
		return ctrl.Result{}, err
	}

	// server side apply generated yaml
	err = r.desiredIngressRoute(tenant)
	if err != nil {
		return ctrl.Result{}, err
	}

	log.Info(tenant.Spec.UUID)
	log.Info(tenant.Spec.CName)

	tenant.Status.URL = fmt.Sprintf("http://%s.jdwl.in", tenant.Spec.CName)
	err = r.Status().Update(ctx, &tenant)
	if err != nil {
		fmt.Println(err)
		return ctrl.Result{}, err
	}

	log.Info("reconciled tenant")

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mgr.GetFieldIndexer().IndexField(
		&operatorsv1alpha1.Tenant{}, ".spec.UUID",
		func(obj runtime.Object) []string {
			uuid := obj.(*operatorsv1alpha1.Tenant).Spec.UUID
			if uuid == "" {
				return nil
			}
			return []string{uuid}
		})

	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.Tenant{}).
		Owns(&appsv1.Deployment{}).
		// Watches(
		//   &source.Kind{Type: &webappv1.Redis{}},
		//   &handler.EnqueueRequestsFromMapFunc{
		//     ToRequests: handler.ToRequestsFunc(r.booksUsingRedis),
		//   }).
		Complete(r)
}
