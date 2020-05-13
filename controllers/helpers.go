package controllers

import (
	operatorsv1alpha1 "jdwl.in/operator/api/v1alpha1"
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

	return depl, nil
}

func (r *TenantReconciler) desiredService(tenant operatorsv1alpha1.Tenant) (corev1.Service, error) {
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

	return svc, nil
}

func (r *TenantReconciler) desiredIngressRoute(tenant operatorsv1alpha1.Tenant) error {

	return nil
}
