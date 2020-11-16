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
	ctx := context.Background()
	log := r.Log.WithValues("tenant", req.NamespacedName)

	var tenant operatorsv1alpha1.Tenant
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info(tenant.Spec.UUID)
	log.Info(tenant.Spec.CName)

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

	r.ReconcileFinalizers(req)

	log.Info("reconciling database and user")
	pwd := helper.GenRandPassword(12)

	r.createDB(tenant)
	r.createUser(tenant, pwd)

	// r.createSecret(tenant, pwd)

	log.Info("tenant", "replicas count: ", tenant.Spec.Replicas)

	// kubectl describe tenant
	r.recorder.Event(&tenant, corev1.EventTypeNormal, "Reconciled", "Reconciling tenant start")

	// if replicas = 0, set namespace to sleep mode
	// if tenant.Spec.Replicas == 0 {
	_ = r.ScaleNamespace(tenant.Namespace, int(tenant.Spec.Replicas))
	// return ctrl.Result{}, nil
	// }

	// your logic here
	log.Info("reconciling tenant")

	applyOpts := []client.PatchOption{client.ForceOwnership, client.FieldOwner("tenant-controller")}

	secret, err := r.createSecret(tenant, pwd)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = r.Patch(ctx, &secret, client.Apply, applyOpts...)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(&tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling secret finished")

	// server side apply generated yaml
	err = r.desiredIngressRoute(tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(&tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling ingressRoute finished")

	// server side apply generated yaml
	err = r.desiredConfigmap(tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(&tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling configmap finished")

	deployment, err := r.desiredDeployment(tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = r.Patch(ctx, &deployment, client.Apply, applyOpts...)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(&tenant, corev1.EventTypeNormal, "Reconciliation started", "Reconciling deployment finished")

	svc, err := r.desiredService(tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = r.Patch(ctx, &svc, client.Apply, applyOpts...)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(&tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling service finished")

	tenant.Status.URL = fmt.Sprintf("http://%s.jdwl.in", tenant.Spec.CName)
	tenant.Status.Replicas = tenant.Spec.Replicas
	tenant.Status.CName = tenant.Spec.CName

	tenant.Status.Status = "Active"
	if tenant.Status.Replicas == 0 {
		tenant.Status.Status = "Inactive"
	}

	err = r.Status().Update(ctx, &tenant)
	if err != nil {
		fmt.Println(err)
		r.recorder.Event(&tenant, corev1.EventTypeWarning, "Reconciliation status changed", "Reconciling tenant failed")
		return ctrl.Result{}, err
	}

	// https://book-v1.book.kubebuilder.io/beyond_basics/creating_events.html
	r.recorder.Event(&tenant, corev1.EventTypeNormal, "Reconciliation succeed", "Reconciling tenant succeed")

	log.Info("reconciled tenant")

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) createDB(tenant operatorsv1alpha1.Tenant) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling database")

	db := r.DBConn
	db.Ping()

	// defer db.Close()

	_, err := db.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, tenant.Spec.CName))
	if err != nil {
		fmt.Println(err.Error())
	} else {
		fmt.Println("Successfully created database..")
	}
}

func (r *TenantReconciler) createSecret(tenant operatorsv1alpha1.Tenant, password string) (corev1.Secret, error) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling secret")

	data := make(map[string]string)
	data["DBPassword"] = password

	sec := corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: corev1.SchemeGroupVersion.String(), Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "db-secret",
			Namespace: tenant.Namespace,
		},
		StringData: data,
		Type:       "generic",
	}
	if err := ctrl.SetControllerReference(&tenant, &sec, r.Scheme); err != nil {
		return sec, err
	}
	log.Info("reconciled secret")

	return sec, nil
}

func (r *TenantReconciler) createUser(tenant operatorsv1alpha1.Tenant, password string) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling database user")

	db := r.DBConn
	db.Ping()

	// defer db.Close()
	stmt := fmt.Sprintf(`CREATE USER "%s" WITH PASSWORD '%s';`, tenant.Spec.CName, password)
	log.Info(stmt)

	_, err := db.Exec(stmt)
	if err != nil {
		// fmt.Println(err.Error())
		_, err := db.Exec(fmt.Sprintf(`DROP USER "%s";`, tenant.Spec.CName))
		fmt.Println(err)
		_, err = db.Exec(stmt)
	} else {
		fmt.Println("Successfully created user..")
	}
}

func (r *TenantReconciler) ScaleNamespace(ns string, replicas int) error {
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
		//   &source.Kind{Type: &webappv1.Redis{}},
		//   &handler.EnqueueRequestsFromMapFunc{
		//     ToRequests: handler.ToRequestsFunc(r.booksUsingRedis),
		//   }).
		Complete(r)
}

func (r *TenantReconciler) deleteExternalResources(tenant operatorsv1alpha1.Tenant) error {
	//
	// delete any external resources associated with the cronJob
	//
	// Ensure that delete implementation is idempotent and safe to invoke
	// multiple types for same object.

	// remove database and user

	r.dropDB(tenant)
	r.dropUser(tenant)

	return nil
}

// Helper functions to check and remove string from a slice of strings.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

func (r *TenantReconciler) ReconcileFinalizers(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()

	var tenant *operatorsv1alpha1.Tenant
	if err := r.Get(ctx, req.NamespacedName, tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Add finalizer to pre-delete database and user for a tenant
	// https://book.kubebuilder.io/reference/using-finalizers.html
	// name of our custom finalizer
	myFinalizerName := "database.finalizers.jdwl.in"

	// examine DeletionTimestamp to determine if object is under deletion
	if tenant.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !containsString(tenant.ObjectMeta.Finalizers, myFinalizerName) {
			tenant.ObjectMeta.Finalizers = append(tenant.ObjectMeta.Finalizers, myFinalizerName)
			fmt.Println(tenant)
			if err := r.Update(context.Background(), tenant, nil); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if containsString(tenant.ObjectMeta.Finalizers, myFinalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(*tenant); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			tenant.ObjectMeta.Finalizers = removeString(tenant.ObjectMeta.Finalizers, myFinalizerName)
			if err := r.Update(context.Background(), tenant, nil); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) dropDB(tenant operatorsv1alpha1.Tenant) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("finalizing database")

	db := r.DBConn
	_, err := db.Exec(fmt.Sprintf(`DROP DATABASE "%s"`, tenant.Spec.CName))
	if err != nil {
		fmt.Println(err.Error())
	} else {
		fmt.Println("Successfully dropped database..")
	}
}

func (r *TenantReconciler) dropUser(tenant operatorsv1alpha1.Tenant) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("finalizing database user")

	db := r.DBConn
	// defer db.Close()

	_, err := db.Exec(fmt.Sprintf(`DROP USER "%s";`, tenant.Spec.CName))
	if err != nil {
		// fmt.Println(err.Error())
		fmt.Println(err)
	} else {
		fmt.Println("Successfully dropped user..")
	}
}
