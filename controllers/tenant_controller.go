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

const (
	host     = "localhost"
	port     = 5432
	user     = "postgres"
	password = "^[Nd}6Ka_c,A0-ti}1l:iAQN"
	dbname   = "tenants"
)

// TenantReconciler reconciles a Tenant object
type TenantReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	recorder record.EventRecorder
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

	log.Info("reconciling database and user")
	createDB(tenant)
	createUser(tenant)

	log.Info("tenant", "replicas count: ", tenant.Spec.Replicas)

	// kubectl describe tenant
	r.recorder.Event(&tenant, corev1.EventTypeNormal, "Reconciled", "Reconciling tenant start")

	// if replicas = 0, set namespace to sleep mode
	// if tenant.Spec.Replicas == 0 {
	_ = r.ScaleNamespace(tenant.Namespace, int(tenant.Spec.Replicas))
	// return ctrl.Result{}, nil
	// }

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

	applyOpts := []client.PatchOption{client.ForceOwnership, client.FieldOwner("tenant-controller")}

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

	// server side apply generated yaml
	err = r.desiredIngressRoute(tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(&tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling ingressRoute finished")

	log.Info(tenant.Spec.UUID)
	log.Info(tenant.Spec.CName)

	// server side apply generated yaml
	err = r.desiredConfigmap(tenant)
	if err != nil {
		return ctrl.Result{}, err
	}

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

func createDB(tenant operatorsv1alpha1.Tenant) {
	db := getDB()
	defer db.Close()

	_, err := db.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, tenant.Spec.CName))
	if err != nil {
		fmt.Println(err.Error())
	} else {
		fmt.Println("Successfully created database..")
	}
}

func createUser(tenant operatorsv1alpha1.Tenant) {
	db := getDB()
	defer db.Close()

	_, err := db.Exec(fmt.Sprintf(`CREATE USER "%s" WITH PASSWORD '%s';`, tenant.Spec.CName, "xxxxx"))
	if err != nil {
		fmt.Println(err.Error())
	} else {
		fmt.Println("Successfully created database..")
	}
}

func getDB() *sql.DB {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)
	fmt.Println(psqlInfo)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		panic(err)
	}

	err = db.Ping()
	if err != nil {
		panic(err)
	}

	return db
}

func (r *TenantReconciler) ScaleNamespace(ns string, replicas int) error {
	clientset := helper.GetClientSet()
	options := metav1.ListOptions{
		// LabelSelector: "app=<APPNAME>",
	}

	// list deployments
	deployList, _ := clientset.AppsV1().Deployments(ns).List(context.TODO(), options)
	fmt.Println("list deployments")
	for _, item := range (*deployList).Items {
		fmt.Println(item.Name)
		fmt.Println(item.Namespace)
		fmt.Println(item.Status)
	}

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
			// log.Fatal(err)
			fmt.Println(err)
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
