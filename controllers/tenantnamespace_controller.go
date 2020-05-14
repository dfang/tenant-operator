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

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	operatorsv1alpha1 "jdwl.in/operator/api/v1alpha1"
)

// TenantNamespaceReconciler reconciles a TenantNamespace object
type TenantNamespaceReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=operators.jdwl.in,resources=tenantnamespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operators.jdwl.in,resources=tenantnamespaces/status,verbs=get;update;patch

func (r *TenantNamespaceReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	log := r.Log.WithValues("tenantnamespace", req.NamespacedName)

	// your logic here
	log.Info("reconciling tenantnamespace")

	// Fetch the TenantNamespace instance
	instance := &operatorsv1alpha1.TenantNamespace{}
	err := r.Get(context.TODO(), req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Fetch namespace list
	nsList := &corev1.NamespaceList{}
	err = r.List(context.TODO(), nsList, &client.ListOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}

	// Fetch tenant list
	tenantList := &operatorsv1alpha1.TenantList{}
	err = r.List(context.TODO(), tenantList, &client.ListOptions{})
	if err != nil {
		return reconcile.Result{}, err
	}

	// tenantName, requireNamespacePrefix := findTenant(instance.Namespace, tenantList)

	// In case namespace already exists
	// tenantNsName := tenantutil.GetTenantNamespaceName(requireNamespacePrefix, instance)
	tenantNsName := instance.Spec.Name

	found := false
	for _, each := range nsList.Items {
		if each.Name == tenantNsName {
			found = true
			break
		}
	}

	// In case a new namespace needs to be created
	if !found {
		tenantNs := &corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				APIVersion: corev1.SchemeGroupVersion.String(),
				Kind:       "Namespace",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:   tenantNsName,
				Labels: map[string]string{"namespace-for-tenant": "true"},
				// Annotations: map[string]string{
				// 	TenantAdminNamespaceAnnotation: instance.Namespace,
				// },
				// OwnerReferences: []metav1.OwnerReference{expectedOwnerRef},
			},
		}
		if err = r.Client.Create(context.TODO(), tenantNs); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *TenantNamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.TenantNamespace{}).
		Complete(r)
}

func findTenant(adminNsName string, tList *operatorsv1alpha1.TenantList) (string, bool) {
	// Find the tenant that owns the adminNs
	requireNamespacePrefix := false
	var tenantName string
	// for _, each := range tList.Items {
	// 	if each.Spec.TenantAdminNamespaceName == adminNsName {
	// 		requireNamespacePrefix = each.Spec.RequireNamespacePrefix
	// 		tenantName = each.Name
	// 		break
	// 	}
	// }
	return tenantName, requireNamespacePrefix
}
