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
	"time"

	operatorsv1alpha1 "github.com/dfang/tenant-operator/api/v1alpha1"
	"github.com/dfang/tenant-operator/pkg/helper"
	"github.com/go-logr/logr"
	_ "github.com/lib/pq"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// TenantReconciler reconciles a Tenant object
type TenantReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	recorder record.EventRecorder

	DBConn *sql.DB
	Domain string
}

// +kubebuilder:rbac:groups=operators.jdwl.in,resources=tenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operators.jdwl.in,resources=tenants/status,verbs=get;update;patch

// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=list;watch;get;patch
// +kubebuilder:rbac:groups=core,resources=services,verbs=list;watch;get;patch

// Reconcile Reconcile Tenant
func (r *TenantReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("tenant", req.Namespace)

	// Fetch the tenant from the cache
	tenant := &operatorsv1alpha1.Tenant{}
	if err := r.Client.Get(context.TODO(), req.NamespacedName, tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if result, err := r.reconcileFinalizers(tenant); err != nil {
		log.Error(err, "failed to reconcile finalizers")
		return result, err
	}

	// goal: get namespace status
	// if it's been terminating, just return, no more reconciling
	// that means if namespace is been terminating, no need to concile deploy,svc etc
	clientset := helper.GetClientSet()
	ns, _ := clientset.CoreV1().Namespaces().Get(context.TODO(), tenant.Namespace, metav1.GetOptions{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(), Kind: "Namespace",
		},
	})

	if ns.Status.Phase == corev1.NamespaceTerminating {
		return ctrl.Result{}, nil
	}

	// maybe add labels when create a tenant
	// no need to reconcile every loop
	if result, err := r.reconcileNamespace(tenant); err != nil {
		log.Error(err, "failed to reconcile namespace label")
		return result, err
	}

	if result, err := r.reconcileDB(tenant); err != nil {
		log.Error(err, "failed to reconcile db")
		return result, err
	}

	if result, err := r.reconcileSecret(tenant); err != nil {
		log.Error(err, "failed to reconcile secret")
		return result, err
	}

	if result, err := r.reconcileConfigmap(tenant); err != nil {
		log.Error(err, "failed to reconcile configmap")
		return result, err
	}

	if result, err := r.reconcileIngressRoute(tenant); err != nil {
		log.Error(err, "failed to reconcile ingressRoute")
		return result, err
	}

	if result, err := r.reconcileService(tenant); err != nil {
		log.Error(err, "failed to reconcile service")
		return result, err
	}

	if _, err := r.reconcileDeployment(tenant); err != nil {
		log.Error(err, "failed to reconcile deployment")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	if result, err := r.reconcileRedis(tenant); err != nil {
		log.Error(err, "failed to reconcile redis")
		return result, err
	}

	if result, err := r.updateStatus(tenant); err != nil {
		log.Error(err, "failed to update tenant status")
		return result, err
	}

	// https://book-v1.book.kubebuilder.io/beyond_basics/creating_events.html
	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation succeed", "Reconciling tenant succeed")

	log.Info("reconciliation done !!!")
	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileDeployment(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling qox deployment")

	deploy, err := r.desiredDeployment(*tenant)

	// ysz := json.NewYAMLSerializer(json.DefaultMetaFactory, nil, nil)
	// if err = ysz.Encode(&deploy, os.Stdout); err != nil {
	// 	panic(err)
	// }

	if err != nil {
		return ctrl.Result{}, err
	}

	// Note: use controller runtime MergeFrom here to avoid no-idempotency problem
	// https://github.com/kubernetes-sigs/controller-runtime/blob/master/pkg/client/patch.go
	// for more see question asked in slack #kubebuilder channel
	// https://stackoverflow.com/questions/57712941/what-is-the-proper-way-to-patch-an-object-with-controller-runtime

	// option 1 has reconcile dead loop problem
	// option 2 has problem when creating deployment, if deploy is there it's ok
	// option 3 is so far so good

	// option 1
	// applyOpts := []client.PatchOption{client.ForceOwnership, client.FieldOwner("tenant-controller")}
	// err = r.Client.Patch(context.TODO(), &deployment, client.Apply, applyOpts...)

	// option 2
	// err = r.Client.Patch(context.TODO(), &deployment, client.MergeFrom(&deployment), &client.PatchOptions{FieldManager: "tenant-controller"})

	// option 3
	op, err := controllerutil.CreateOrUpdate(context.TODO(), r.Client, &deploy, func() error {
		// Deployment selector is immutable so we set this value only if
		// a new object is going to be created
		if deploy.ObjectMeta.CreationTimestamp.IsZero() {
			deploy.Spec.Selector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"tenant": tenant.Name},
			}
		}

		return nil
	})

	if err != nil {
		log.Error(err, "deployment reconcile failed")
	} else {
		log.Info("deployment reconciled", "operation", op)
	}

	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation started", "Reconciling deployment finished")
	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileService(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling qox service")

	applyOpts := []client.PatchOption{client.ForceOwnership, client.FieldOwner("tenant-controller")}
	svc, err := r.desiredService(*tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = r.Patch(context.TODO(), &svc, client.Apply, applyOpts...)
	if err != nil {
		return ctrl.Result{}, err
	}
	// r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling service finished")

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileConfigmap(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling qox configmap")

	// server side apply generated yaml
	err := r.desiredConfigmap(*tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling configmap finished")

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileIngressRoute(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling qox ingressRoute")

	// server side apply generated yaml
	err := r.desiredIngressRoute(*tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling ingressRoute finished")

	return ctrl.Result{}, nil
}

// https://github.com/kubernetes/apimachinery/issues/109
// note: currently you can't server side apply two resource in one yaml
// server side apply is still beta in k8s 1.18
func (r *TenantReconciler) reconcileRedis(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	if r, err := r.reconcileRedisDeployment(tenant); err != nil {
		return r, err
	}

	if r, err := r.reconcileRedisSvc(tenant); err != nil {
		return r, err
	}

	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling redis finished")

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileSecret(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling qox secret")

	password := helper.GenRandPassword(12)
	applyOpts := []client.PatchOption{client.ForceOwnership, client.FieldOwner("tenant-controller")}
	// applyOpts := []client.UpdateOption{
	//   FieldManager
	// }
	//   client.ForceOwnership, client.FieldOwner("tenant-controller")}
	// secret, err := r.desiredSecret(*tenant, password)
	data := make(map[string]string)
	data["DBPassword"] = password

	secretName := client.ObjectKey{
		Name:      "db-secret",
		Namespace: tenant.Namespace,
	}
	existingSecret := &corev1.Secret{}
	err := helper.GetClientOrDie().Get(context.Background(), secretName, existingSecret)

	if err != nil {
		existingSecret.Type = "generic"
		existingSecret.StringData = data
		existingSecret.TypeMeta = metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Secret",
		}
		existingSecret.ObjectMeta = metav1.ObjectMeta{
			Name:      "db-secret",
			Namespace: tenant.Namespace,
		}

		if err := ctrl.SetControllerReference(tenant, existingSecret, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		r.createOrUpdateUser(*tenant, password)
		err = r.Patch(context.TODO(), existingSecret, client.Apply, applyOpts...)
		if err != nil {
			fmt.Println("Patch")
			return ctrl.Result{}, err
		}
	}

	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling secret finished")
	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileDB(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling qox database")

	err := r.createDB(*tenant)
	if err != nil {
		return ctrl.Result{}, err
	}

	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling tenant database finished")
	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileFinalizers(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	// Add finalizer to pre-delete database and user for a tenant
	// https://book.kubebuilder.io/reference/using-finalizers.html

	// name of our custom finalizer
	dbFinalizer := "database.finalizers.jdwl.in"

	// examine DeletionTimestamp to determine if object is under deletion
	if tenant.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !controllerutil.ContainsFinalizer(tenant, dbFinalizer) {
			controllerutil.AddFinalizer(tenant, dbFinalizer)
			if err := r.Update(context.TODO(), tenant); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(tenant, dbFinalizer) {
			// our finalizer is present, so lets handle any external dependency
			// ここで外部リソースを削除する
			if err := r.deleteExternalResources(*tenant); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}
			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(tenant, dbFinalizer)
			if err := r.Update(context.TODO(), tenant); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) updateStatus(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("update tenant status")

	tenant.Status.URL = fmt.Sprintf("http://%s", r.getTenantSubdomain(tenant.Spec.CName))
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

	r.recorder.Event(tenant, corev1.EventTypeNormal, "Reconciliation status changed", "Reconciling update status finished")
	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileNamespace(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("set label owner=tenant for tenant namespace")
	ns := &corev1.Namespace{}
	err := r.Client.Get(context.Background(), client.ObjectKey{
		Name: tenant.Spec.CName,
	}, ns)

	labels := fmt.Sprintf(`{"metadata":{"labels":{"owner": "tenant", "uuid": "%s"}}}`, tenant.Spec.UUID)
	patch := []byte(labels)
	if err == nil {
		_ = r.Client.Patch(context.TODO(), ns, client.RawPatch(types.StrategicMergePatchType, patch))
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
			// log.Fatal(err)
			fmt.Println(err)
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
