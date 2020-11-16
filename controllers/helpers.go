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
	log := r.Log.WithValues("tenant", tenant.Namespace)

	log.Info("reconciling deployment")

	depl := appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      tenant.Name,
			Namespace: tenant.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			// Replicas: tenant.Spec.Frontend.Replicas, // won't be nil because defaulting
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
							Image: "dfang/qox:develop-20201116.1",
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

	log.Info("reconciled deployment")

	return depl, nil
}

func (r *TenantReconciler) desiredService(tenant operatorsv1alpha1.Tenant) (corev1.Service, error) {
	log := r.Log.WithValues("tenant", tenant.Namespace)

	log.Info("reconciling service")

	svc := corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: corev1.SchemeGroupVersion.String(), Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      tenant.Name,
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

	log.Info("reconciled service")

	return svc, nil
}

func (r *TenantReconciler) desiredIngressRoute(tenant operatorsv1alpha1.Tenant) error {
	log := r.Log.WithValues("tenant", tenant.Namespace)

	config := helper.GetConfig()

	log.Info("reconciling ingressRoute")

	data := struct {
		Name        string
		Namespace   string
		Host        string
		ServiceName string
	}{
		Name:        tenant.Spec.UUID,
		Namespace:   tenant.Spec.CName,
		ServiceName: tenant.Spec.CName,
		Host:        fmt.Sprintf("%s.jdwl.in", tenant.Spec.CName),
	}

	yamlContent := renderTemplate("/controllers/templates/ingressRoute.yaml", data)

	_, err := helper.DoSSA(context.Background(), config, yamlContent)
	if err != nil {
		panic(err.Error())
	}

	// don't forget to set owner reference, otherwise infinite reconcile loop

	log.Info("reconciled ingressRoute")

	return nil
}

func (r *TenantReconciler) desiredConfigmap(tenant operatorsv1alpha1.Tenant) error {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling configmap")

	config := helper.GetConfig()

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

	log.Info("reconciled configmap")

	return nil
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
