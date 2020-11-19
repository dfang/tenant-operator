package controllers

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"text/template"

	operatorsv1alpha1 "github.com/dfang/tenant-operator/api/v1alpha1"
	"github.com/dfang/tenant-operator/pkg/helper"
	"github.com/markbates/pkger"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
)

func (r *TenantReconciler) desiredDeployment(tenant operatorsv1alpha1.Tenant) (appsv1.Deployment, error) {
	depl := appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      tenant.Name,
			Namespace: tenant.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"tenant": tenant.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"tenant": tenant.Name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "qor",
							// Image: "dfang/qor-demo:develop-20201019.1",
							Image: "dfang/qox:develop-20201117.1",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 7000, Name: "http", Protocol: "TCP"},
								{ContainerPort: 5040, Name: "gowork", Protocol: "TCP"},
							},
							// Resources: *tenant.Spec.Frontend.Resources.DeepCopy(),
							EnvFrom: []corev1.EnvFromSource{
								{
									ConfigMapRef: &corev1.ConfigMapEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "env-config",
										},
									},
								},
								{
									SecretRef: &corev1.SecretEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "db-secret",
										},
									},
								},
							},
							Args: []string{
								"./qor",
								"--debug",
								"start",
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name: "init",
							// Image: "dfang/qor-demo:develop-20201019.1",
							Image: "dfang/qox:develop-20201116.1",
							Args: []string{
								"./qor",
								"migrate",
							},
							EnvFrom: []corev1.EnvFromSource{
								{
									ConfigMapRef: &corev1.ConfigMapEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "env-config",
										},
									},
								},
								{
									SecretRef: &corev1.SecretEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "db-secret",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(&tenant, &depl, r.Scheme); err != nil {
		return depl, err
	}

	return depl, nil
}

func (r *TenantReconciler) desiredService(tenant operatorsv1alpha1.Tenant) (corev1.Service, error) {
	svc := corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: corev1.SchemeGroupVersion.String(), Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qox",
			Namespace: tenant.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, Protocol: "TCP", TargetPort: intstr.FromString("http")},
			},
			Selector: map[string]string{"tenant": tenant.Name},
			Type:     corev1.ServiceTypeClusterIP,
		},
	}

	// always set the controller reference so that we know which object owns this.
	if err := ctrl.SetControllerReference(&tenant, &svc, r.Scheme); err != nil {
		return svc, err
	}

	return svc, nil
}

func (r *TenantReconciler) desiredIngressRoute(tenant operatorsv1alpha1.Tenant) error {
	config := ctrl.GetConfigOrDie()

	data := struct {
		Name        string
		Namespace   string
		Host        string
		ServiceName string
	}{
		Name:        tenant.Spec.CName,
		Namespace:   tenant.Spec.CName,
		ServiceName: tenant.Spec.CName,
		Host:        r.getTenantSubdomain(tenant.Spec.CName),
	}

	yamlContent := renderTemplate("/controllers/templates/ingressRoute.yaml", data)

	_, err := helper.DoSSA(context.Background(), config, yamlContent)
	if err != nil {
		panic(err.Error())
	}

	// don't forget to set owner reference, otherwise infinite reconcile loop

	return nil
}

func (r *TenantReconciler) desiredConfigmap(tenant operatorsv1alpha1.Tenant) error {
	config := ctrl.GetConfigOrDie()
	data := struct {
		Namespace string
	}{
		Namespace: tenant.Spec.CName,
	}

	yamlContent := renderTemplate("/controllers/templates/env-config.yaml", data)

	_, err := helper.DoSSA(context.Background(), config, yamlContent)
	if err != nil {
		panic(err.Error())
	}

	return nil
}

func (r *TenantReconciler) reconcileRedisDeployment(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling redis deployment")

	config := ctrl.GetConfigOrDie()

	data := struct {
		Namespace string
	}{
		Namespace: tenant.Spec.CName,
	}

	yamlContent := renderTemplate("/controllers/templates/redis-deploy.yaml", data)

	_, err := helper.DoSSA(context.Background(), config, yamlContent)
	if err != nil {
		log.Info("err when doSSA: ", err)
		return ctrl.Result{}, err
	}

	// don't forget to set owner reference, otherwise infinite reconcile loop
	return ctrl.Result{}, nil
}

func (r *TenantReconciler) reconcileRedisSvc(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling redis service")
	config := ctrl.GetConfigOrDie()

	data := struct {
		Namespace string
	}{
		Namespace: tenant.Spec.CName,
	}

	yamlContent := renderTemplate("/controllers/templates/redis-svc.yaml", data)

	_, err := helper.DoSSA(context.Background(), config, yamlContent)
	if err != nil {
		log.Info("err when doSSA: ", err)
		return ctrl.Result{}, err
	}

	// don't forget to set owner reference, otherwise infinite reconcile loop

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) createDB(tenant operatorsv1alpha1.Tenant) error {
	// log := r.Log.WithValues("tenant", tenant.Namespace)
	// log.Info("reconciling database")

	db := r.DBConn
	// defer db.Close()

	statement := fmt.Sprintf(`SELECT EXISTS(SELECT datname FROM pg_catalog.pg_database WHERE datname = '%s');`, tenant.Spec.CName)
	row := db.QueryRow(statement)
	var exists bool
	err := row.Scan(&exists)
	check(err)

	if exists {
		return nil
	}

	statement = fmt.Sprintf(`CREATE DATABASE "%s";`, tenant.Spec.CName)
	_, err = db.Exec(statement)
	if err != nil {
		fmt.Println("got error when create database", err.Error())
	} else {
		fmt.Println("Successfully created database..")
	}

	return nil
}

func (r *TenantReconciler) reconcileFinalizers(tenant *operatorsv1alpha1.Tenant) (ctrl.Result, error) {
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
			if err := r.Update(context.Background(), tenant); err != nil {
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
			if err := r.Update(context.Background(), tenant); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *TenantReconciler) createOrUpdateUser(tenant operatorsv1alpha1.Tenant, password string) {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling database user")

	db := r.DBConn

	// defer db.Close()
	stmt := fmt.Sprintf(`CREATE USER "%s" WITH PASSWORD '%s';`, tenant.Spec.CName, password)

	_, err := db.Exec(stmt)
	if err != nil {
		stmt := fmt.Sprintf(`ALTER USER "%s" WITH PASSWORD '%s';`, tenant.Spec.CName, password)
		_, err = db.Exec(stmt)
	} else {
		fmt.Println("Successfully created/updated database user ...")
	}
}

// renderTemplate renderTemplate with data, return yamlContent
func renderTemplate(tpl string, data interface{}) string {
	f, err := pkger.Open(tpl)
	if err != nil {
		panic(err)
	}

	defer f.Close()

	b, err := ioutil.ReadAll(f)
	if err != nil {
		panic(err)
	}

	tmpl, err := template.New(tpl).Parse(string(b))
	if err != nil {
		panic(err)
	}

	buf := new(bytes.Buffer)
	err = tmpl.Execute(buf, data)
	if err != nil {
		panic(err)
	}

	yamlContent := buf.String()

	return yamlContent
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

func check(err error) {
	if err != nil {
		fmt.Println(err)
	}
}

func (r *TenantReconciler) deleteExternalResources(tenant operatorsv1alpha1.Tenant) error {
	//
	// delete any external resources associated with the cronJob
	//
	// Ensure that delete implementation is idempotent and safe to invoke
	// multiple types for same object.

	// remove database and user when a tenant deleted
	if err := r.dropDB(tenant); err != nil {
		return err
	}

	if err := r.dropUser(tenant); err != nil {
		return err
	}

	return nil
}

func (r *TenantReconciler) dropDB(tenant operatorsv1alpha1.Tenant) error {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("finalizing database")

	db := r.DBConn

	// terminate connections first
	// https://stackoverflow.com/questions/17449420/postgresql-unable-to-drop-database-because-of-some-auto-connections-to-db
	stmt := fmt.Sprintf(`SELECT pid, pg_terminate_backend(pid)
            FROM pg_stat_activity
            WHERE datname = '%s' AND pid <> pg_backend_pid();`, tenant.Spec.CName)
	if _, err := db.Exec(stmt); err != nil {
		fmt.Println(err.Error())
		return err
	}

	if _, err := db.Exec(fmt.Sprintf(`DROP DATABASE "%s"`, tenant.Spec.CName)); err != nil {
		fmt.Println(err.Error())
		return err
	}

	fmt.Println("Successfully dropped database..")
	return nil
}

func (r *TenantReconciler) dropUser(tenant operatorsv1alpha1.Tenant) error {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("finalizing database user")

	db := r.DBConn
	// defer db.Close()

	_, err := db.Exec(fmt.Sprintf(`DROP USER "%s";`, tenant.Spec.CName))
	if err != nil {
		// fmt.Println(err.Error())
		fmt.Println(err)
		return err
	}

	fmt.Println("Successfully dropped user..")
	return nil
}

func (r *TenantReconciler) getTenantSubdomain(cname string) string {
	return fmt.Sprintf("%s.%s", cname, r.Domain)
}
