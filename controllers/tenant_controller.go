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
	"database/sql"
	"fmt"
	"log"

	operatorsv1alpha1 "github.com/dfang/tenant-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dfang/tenant-operator/pkg/helper"

	_ "github.com/lib/pq"
)

// TenantReconciler reconciles a Tenant object
type TenantReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	recorder record.EventRecorder

	DBConn *sql.DB
}

// +kubebuilder:rbac:groups=operators.jdwl.in,resources=tenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operators.jdwl.in,resources=tenants/status,verbs=get;update;patch

// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=list;watch;get;patch
// +kubebuilder:rbac:groups=core,resources=services,verbs=list;watch;get;patch

// Reconcile Reconcile Tenant
func (r *TenantReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("tenant", req)

	// Fetch the tenant from the cache
	tenant := &operatorsv1alpha1.Tenant{}
	if err := r.Client.Get(context.TODO(), req.NamespacedName, tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Set the label if it is missing
	if tenant.Labels == nil {
		tenant.Labels = map[string]string{}
	}

	tenant.Labels["hello"] = "world"

	log.Info(tenant.Spec.UUID)
	log.Info(tenant.Spec.CName)

	log.Info("Reconcile FINALIZERS")
	if result, err := r.reconcileFinalizers(tenant); err != nil {
		return result, err
	}

	// goal: get namespace status, if it's been terminating, just return, no more reconciling
	clientset := helper.GetClientSet()
	ns, err := clientset.CoreV1().Namespaces().Get(context.TODO(), tenant.Namespace, metav1.GetOptions{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(), Kind: "Namespace",
		},
	})

	if ns.Status.Phase == corev1.NamespaceTerminating {
		return ctrl.Result{}, nil
	}

	log.Info("tenant", "replicas count: ", tenant.Spec.Replicas)

	// // kubectl describe tenant
	// r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciled", "Reconciling tenant start")

	// // if replicas = 0, set namespace to sleep mode
	// // if tenant.Spec.Replicas == 0 {
	// _ = r.scaleNamespace(tenant.Namespace, int(tenant.Spec.Replicas))
	// // return ctrl.Result{}, nil
	// // }

	// your logic here
	log.Info("reconciling tenant")

	if result, err := r.reconcileDB(tenant); err != nil {
		return result, err
	}

	if result, err := r.reconcileSecret(tenant); err != nil {
		return result, err
	}

	if result, err := r.reconcileConfigmap(tenant); err != nil {
		return result, err
	}

	if result, err := r.reconcileIngressRoute(tenant); err != nil {
		return result, err
	}

	if result, err := r.reconcileDeployment(tenant); err != nil {
		return result, err
	}

	if result, err := r.reconcileService(tenant); err != nil {
		return result, err
	}

	_, err = r.updateStatus(tenant)
	if err != nil {
		return ctrl.Result{}, err
	}

	// https://book-v1.book.kubebuilder.io/beyond_basics/creating_events.html
	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation succeed", "Reconciling tenant succeed")
	log.Info("reconciled tenant")

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileDeployment(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	applyOpts := []client.PatchOption{client.ForceOwnership, client.FieldOwner("tenant-controller")}
	deployment, err := r.desiredDeployment(tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = r.Patch(context.TODO(), &deployment, client.Apply, applyOpts...)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation started", "Reconciling deployment finished")

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileService(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	applyOpts := []client.PatchOption{client.ForceOwnership, client.FieldOwner("tenant-controller")}
	svc, err := r.desiredService(*tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = r.Patch(context.TODO(), &svc, client.Apply, applyOpts...)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling service finished")

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileConfigmap(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	// server side apply generated yaml
	err := r.desiredConfigmap(*tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling configmap finished")

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileIngressRoute(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	// server side apply generated yaml
	err := r.desiredIngressRoute(*tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling ingressRoute finished")

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileSecret(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	password := helper.GenRandPassword(12)
	applyOpts := []client.PatchOption{client.ForceOwnership, client.FieldOwner("tenant-controller")}
	// r.createUser(*tenant, pwd)
	secret, err := r.desiredSecret(*tenant, password)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = r.Patch(context.TODO(), &secret, client.Apply, applyOpts...)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling secret finished")

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileDB(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	// log.Info("reconciling database and user")
	err := r.createDB(*tenant)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) updateStatus(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	tenant.Status.URL = fmt.Sprintf("http://%s.jdwl.in", tenant.Spec.CName)
	tenant.Status.Replicas = tenant.Spec.Replicas
	tenant.Status.CName = tenant.Spec.CName

	tenant.Status.Status = "Active"
	if tenant.Status.Replicas == 0 {
		tenant.Status.Status = "Inactive"
	}

	err := r.Status().Update(context.TODO(), tenant)
	if err != nil {
		fmt.Println(err)
		r.recorder.Event(tenant, corev1.EventTypeWarning, "Reconciliation status changed", "Reconciling tenant failed")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) scaleNamespace(ns string, replicas int) error {
	clientset := helper.GetClientSet()
	options := metav1.ListOptions{
		// LabelSelector: "app=<APPNAME>",
	}

	// list deployments
	deployList, _ := clientset.AppsV1().Deployments(ns).List(context.TODO(), options)
	// fmt.Println("list deployments")
	// for _, item := range (*deployList).Items {
	// 	fmt.Println(item.Name)
	// 	fmt.Println(item.Namespace)
	// 	fmt.Println(item.Status)
	// }

	// scale replicas to zero for a given namespace
	for _, item := range (*deployList).Items {
		sc, err := clientset.AppsV1().
			Deployments(item.Namespace).
			GetScale(context.TODO(), item.Name, metav1.GetOptions{})
		if err != nil {
			log.Fatal(err)
		}
		sc.Spec.Replicas = int32(replicas)

		scale, err := clientset.AppsV1().
			Deployments(item.Namespace).
			UpdateScale(context.TODO(), item.Name, sc, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
		fmt.Printf("Set namespace %s replicas to %d\n", item.Name, scale.Spec.Replicas)
	}
	return nil
}

func (r *TenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mgr.GetFieldIndexer().IndexField(
		context.TODO(),
		&operatorsv1alpha1.Tenant{}, ".spec.UUID",
		func(obj runtime.Object) []string {
			uuid := obj.(*operatorsv1alpha1.Tenant).Spec.UUID
			if uuid == "" {
				return nil
			}
			return []string{uuid}
		})

	// add this line
	r.recorder = mgr.GetEventRecorderFor("Tenant")

	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.Tenant{}).
		Owns(&appsv1.Deployment{}).
		// Watches(
		//   &source.Kind{Type: &operatorsv1alpha1.Tenant{}},
		//   &handler.EnqueueRequestsFromMapFunc{
		//     ToRequests: handler.ToRequestsFunc(r.booksUsingRedis),
		//   }).
		Complete(r)
}
