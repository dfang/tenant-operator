package controllers

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"text/template"

	operatorsv1alpha1 "github.com/dfang/tenant-operator/api/v1alpha1"
	"github.com/dfang/tenant-operator/pkg/helper"
	"github.com/markbates/pkger"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
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
							Name:  "whoami",
							Image: "containous/whoami:latest",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 80, Name: "http", Protocol: "TCP"},
							},
							// Resources: *tenant.Spec.Frontend.Resources.DeepCopy(),
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

	b := `apiVersion: traefik.containo.us/v1alpha1
kind: IngressRoute
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
  labels:
    owner: tenant
spec:
  entryPoints:
    - web
  routes:
  - match: Host("{{ .Host }}")
    kind: Rule
    services:
    - name: {{ .ServiceName }}
      namespace: {{ .Namespace }}
      port: 80
`

	tmpl, err := template.New("ingressRoute").Parse(string(b))
	if err != nil {
		panic(err)
	}

	buf := new(bytes.Buffer)
	err = tmpl.Execute(buf, data)
	if err != nil {
		panic(err)
	}

	yamlContent := buf.String()
	_, err = helper.DoSSA(context.Background(), config, yamlContent)
	if err != nil {
		panic(err.Error())
	}

	// // don't forget to set owner reference, otherwise infinite reconcile loop
	// if err := ctrl.SetControllerReference(&tenant, unstructuredObj, r.Scheme); err != nil {
	// 	fmt.Println(err)
	// 	return err
	// }

	// metav1.OwnerReference{
	//   Kind: "Tenant",
	//   Name: "",
	//   UID:
	// }

	// dr.SetOwnerReferences(metav1.OwnerReference{
	//   Controller:
	//   Name:
	// })

	log.Info("reconciled ingressRoute")

	return nil
}

func (r *TenantReconciler) desiredConfigmap(tenant operatorsv1alpha1.Tenant) error {
	log := r.Log.WithValues("tenant", tenant.Namespace)
	log.Info("reconciling configmap")

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

	f, err := pkger.Open("/controllers/templates/env-config.yaml")
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := ioutil.ReadAll(f)
	if err != nil {
		return err
	}

	tmpl, err := template.New("configmap").Parse(string(b))
	if err != nil {
		panic(err)
	}

	data := struct {
		Namespace string
	}{
		Namespace: tenant.Spec.CName,
	}

	buf := new(bytes.Buffer)
	err = tmpl.Execute(buf, data)
	if err != nil {
		panic(err)
	}

	yamlContent := buf.String()
	_, err = helper.DoSSA(context.Background(), config, yamlContent)
	if err != nil {
		panic(err.Error())
	}

	log.Info("reconciled configmap")

	return nil
}
