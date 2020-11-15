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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/heptiolabs/healthcheck"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	operatorsv1alpha1 "jdwl.in/operator/api/v1alpha1"
	"jdwl.in/operator/controllers"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = operatorsv1alpha1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		Port:               9443,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "d2e9e7f6.jdwl.in",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	port := envOrDefault("PORT", "9876")
	health := healthcheck.NewHandler()
	health.AddLivenessCheck("goroutine-threshold", healthcheck.GoroutineCountCheck(100))
	webhookURL := fmt.Sprintf("http://localhost:%s", port)
	health.AddLivenessCheck("webhook", healthcheck.HTTPGetCheck(webhookURL, 500*time.Millisecond))
	go http.ListenAndServe("0.0.0.0:8086", health)
	go StartWebhookd(port)

	if err = (&controllers.TenantReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("Tenant"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Tenant")
		os.Exit(1)
	}

	// if err = (&controllers.TenantNamespaceReconciler{
	// 	Client: mgr.GetClient(),
	// 	Log:    ctrl.Log.WithName("controllers").WithName("TenantNamespace"),
	// 	Scheme: mgr.GetScheme(),
	// }).SetupWithManager(mgr); err != nil {
	// 	setupLog.Error(err, "unable to create controller", "controller", "TenantNamespace")
	// 	os.Exit(1)
	// }

	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}

}

// StartWebhookd start webhookd
func StartWebhookd(port string) {
	http.HandleFunc("/", InsertEventHandler)

	log.Info().Msgf("Webhook listens on: 0.0.0.0:%s", port)
	log.Fatal().Msg(http.ListenAndServe(fmt.Sprintf(":%s", port), nil).Error())
}

func InsertEventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		fmt.Fprintln(w, "ok")
	} else if r.Method == "POST" {
		log.Info().Msg("webhook received a request")

		// body, err := httputil.DumpRequest(r, true)
		// if err != nil {
		// 	fmt.Println(err)
		// }
		// fmt.Println(string(body))

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			fail(err)
		}

		fmt.Println(string(body))

		v := TenantCreatedEvent{}
		log.Info().Msg("unmarshaling")
		err = json.Unmarshal(body, &v)
		if err != nil {
			panic(err)
		}

		t := T{
			CName: v.Event.Data.New.Cname,
			UUID:  v.Event.Data.New.UUID,
		}

		fmt.Printf("%#v\n", t)

		log.Info().Msgf("uuid: %s", t.UUID)
		log.Info().Msgf("cname: %s", t.CName)

		CreateTenant(t)

		log.Info().Msgf("created tenant %s with uuid: %s", t.CName, t.UUID)

	} else {
		http.Error(w, "Invalid request method.", 405)
	}
}

func envOrDefault(v, def string) string {
	if os.Getenv(v) != "" {
		return os.Getenv(v)
	}
	return def
}

func fail(err error) {
	log.Fatal().Msg(err.Error())
	os.Exit(-1)
}

// TenantCreatedEvent tenant create in hasura will send a event like this
type TenantCreatedEvent struct {
	Event struct {
		SessionVariables struct {
			XHasuraRole string `json:"x-hasura-role"`
		} `json:"session_variables"`
		Op   string `json:"op"`
		Data struct {
			Old interface{} `json:"old"`
			New struct {
				Cname string `json:"cname"`
				UUID  string `json:"uuid"`
			} `json:"new"`
		} `json:"data"`
		TraceContext struct {
			TraceID uint64 `json:"trace_id"`
			SpanID  uint64 `json:"span_id"`
		} `json:"trace_context"`
	} `json:"event"`
	CreatedAt    time.Time `json:"created_at"`
	ID           string    `json:"id"`
	DeliveryInfo struct {
		MaxRetries   int `json:"max_retries"`
		CurrentRetry int `json:"current_retry"`
	} `json:"delivery_info"`
	Trigger struct {
		Name string `json:"name"`
	} `json:"trigger"`
	Table struct {
		Schema string `json:"schema"`
		Name   string `json:"name"`
	} `json:"table"`
}

// T Tenant
type T struct {
	CName string
	UUID  string
}

// CreateTenant create a tenant when received a hasura insert event
func CreateTenant(t T) error {
	tenantTpl := `
apiVersion: operators.jdwl.in/v1alpha1
kind: Tenant
metadata:
  name: {{ .CName }}
  namespace: {{ .CName }}
spec:
  cname: {{ .CName }}
  replicas: 1
  uuid: {{ .UUID }}
`

	log.Info().Msgf("Create namespace %s", t.CName)
	err := createNamespace(t.CName)
	if err != nil {
		panic(err)
	}

	log.Info().Msgf("Create tenant %s in namspace %s", t.CName, t.CName)
	tpl, err := template.New("tenant").Parse(tenantTpl)
	if err != nil {
		panic(err)
	}

	buf := new(bytes.Buffer)
	// Execute(io.Writer(出力先), <データ>)
	if err = tpl.Execute(buf, t); err != nil {
		panic(err)
	}
	yamlContent := buf.String()
	config := getConfig()

	_, err = doSSA(context.Background(), config, yamlContent)
	if err != nil {
		panic(err.Error())
	}

	return nil
}

func createNamespace(nsName string) error {
	clientset := getClientSet()

	// query namespace by name, if not exist, create it
	_, err := clientset.CoreV1().Namespaces().Get(
		nsName,
		metav1.GetOptions{
			TypeMeta: metav1.TypeMeta{
				APIVersion: corev1.SchemeGroupVersion.String(),
			},
		})

	if err != nil {
		nsSpec := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   nsName,
				Labels: map[string]string{"owner": "tenant"},
			},
		}
		_, err := clientset.CoreV1().Namespaces().Create(nsSpec)
		if err != nil {
			panic(err)
		}
	}

	return nil
}

func getConfig() *rest.Config {
	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			panic(err)
		}
	}
	return config
}

func getClientSet() *kubernetes.Clientset {
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
	if err != nil {
		panic(err)
	}
	return clientset
}

// do server side side apply yaml
func doSSA(ctx context.Context, cfg *rest.Config, yamlContent string) (*unstructured.Unstructured, error) {

	// 1. Prepare a RESTMapper to find GVR
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))

	// 2. Prepare the dynamic client
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	// 3. Decode YAML manifest into unstructured.Unstructured
	var decUnstructured = yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}
	_, gvk, err := decUnstructured.Decode([]byte(yamlContent), nil, obj)
	if err != nil {
		return nil, err
	}

	// 4. Find GVR
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, err
	}

	// 5. Obtain REST interface for the GVR
	var dr dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		// namespaced resources should specify the namespace
		dr = dyn.Resource(mapping.Resource).Namespace(obj.GetNamespace())
	} else {
		// for cluster-wide resources
		dr = dyn.Resource(mapping.Resource)
	}

	// set owner references
	// log.DEBUG("Set owner reference")
	// obj.SetOwnerReferences([]metav1.OwnerReference{
	// 	metav1.OwnerReference{
	// 		Kind:       "Tenant",
	// 		Name:       tenant.Spec.CName,
	// 		UID:        tenant.UID,
	// 		APIVersion: "operators.jdwl.in/v1alpha1",
	// 	},
	// })

	// 6. Marshal object into JSON
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}

	// fmt.Println(string(data))

	// 7. Create or Update the object with SSA
	//     types.ApplyPatchType indicates SSA.
	//     FieldManager specifies the field owner ID.
	unstructuredObj, err := dr.Patch(obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{
		FieldManager: "tenant-controller",
	})
	if err != nil {
		fmt.Println(err)
	}

	return unstructuredObj, err
}
