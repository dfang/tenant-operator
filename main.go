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
	"time"

	"github.com/heptiolabs/healthcheck"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	operatorsv1alpha1 "github.com/dfang/tenant-operator/api/v1alpha1"
	"github.com/dfang/tenant-operator/controllers"
	"github.com/dfang/tenant-operator/pkg/helper"
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

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})

	port := envOrDefault("PORT", "9876")
	go StartWebhookd(port)
	go StartHealthCheck(port)

	if err = (&controllers.TenantReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("Tenant"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Tenant")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}

}

// StartHealthCheck start webhookd
func StartHealthCheck(port string) {
	health := healthcheck.NewHandler()
	health.AddLivenessCheck("goroutine-threshold", healthcheck.GoroutineCountCheck(100))
	webhookURL := fmt.Sprintf("http://localhost:%s", port)
	health.AddLivenessCheck("webhook", healthcheck.HTTPGetCheck(webhookURL, 500*time.Millisecond))
	setupLog.Info("HealthCheck listens on: 0.0.0.0:8086")
	log.Fatal().Msg(http.ListenAndServe("0.0.0.0:8086", health).Error())
}

// StartWebhookd start webhookd
func StartWebhookd(port string) {
	http.HandleFunc("/", InsertEventHandler)

	setupLog.Info(fmt.Sprintf("Webhookd listens on: 0.0.0.0:%s", port))
	log.Fatal().Msg(http.ListenAndServe(fmt.Sprintf(":%s", port), nil).Error())
}

// InsertEventHandler handle tenants table insert event
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
	err := helper.CreateNamespace(t.CName)
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
	config := helper.GetConfig()

	_, err = helper.DoSSA(context.Background(), config, yamlContent)
	if err != nil {
		panic(err.Error())
	}

	return nil
}
