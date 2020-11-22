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
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	// _ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	operatorsv1alpha1 "github.com/dfang/tenant-operator/api/v1alpha1"
	"github.com/dfang/tenant-operator/controllers"
	"github.com/dfang/tenant-operator/pkg/helper"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	// DBConn DB Connection
	DBConn *sql.DB

	logLevelEventHandler http.Handler
	atom                 uberzap.AtomicLevel
)

var (
	host     = envOrDefault("TENANTS_DB_HOST", "localhost")
	port     = envOrDefault("TENANTS_DB_PORT", "5432")
	user     = envOrDefault("TENANTS_DB_USER", "postgres")
	password = envOrDefault("TENANTS_DB_PASSWORD", "localhost")
	dbname   = envOrDefault("TENANTS_DB_NAME", "tenants")
	// domain subdmaon for a tenant is http://cname.{{.DOAMIN}}
	domain = envOrDefault("TENANTS_DOMAIN", "jdwl.in")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = operatorsv1alpha1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var logLevel int
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&logLevel, "V", 0, "Log Level(debug info warn error dpanic panic fatal, from -1 to 5), info(0) is defaut, for more, https://pkg.go.dev/go.uber.org/zap/zapcore#Level")
	flag.Parse()

	conn := helper.GetConn(host, port, user, password, dbname)
	atom = uberzap.NewAtomicLevelAt(zapcore.DebugLevel)
	var level zapcore.Level
	if logLevel >= -1 && logLevel <= 5 {
		level = zapcore.Level(int8(logLevel))
	}
	atom.SetLevel(level)

	// ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.Level(level)))
	logger := zap.New(zap.UseDevMode(true), zap.Level(atom))
	ctrl.SetLogger(logger)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		HealthProbeBindAddress: ":8081",
		Port:                   9443,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "mgr.jdwl.in",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	port := envOrDefault("PORT", "9876")
	go StartWebhookd(port)

	if err = (&controllers.TenantReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("Tenant").V(3),
		Scheme: mgr.GetScheme(),
		// DBConn DB Connection
		DBConn: conn,
		// Tenant Domain
		Domain: domain,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Tenant")
		os.Exit(1)
	}

	// health check
	// https://kubernetes.io/docs/reference/using-api/health-checks/

	// Readyness probe :8081/healthz
	err = mgr.AddReadyzCheck("readiness", healthz.Ping)
	if err != nil {
		logger.V(5).Info("unable add a readiness check", "ready", err)
	}

	// Liveness probe :8081/readyz
	err = mgr.AddHealthzCheck("liveness", healthz.Ping)
	if err != nil {
		logger.V(5).Info("unable add a health check", "healthz", err)
	}

	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// StartWebhookd start webhookd
// dynamic logging level
// curlie PUT http://localhost:9876/log_level level=debug
// curl -X PUT -d '{"level": "info"}' http://localhost:9876/log_level
func StartWebhookd(port string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", InsertEventHandleFunc)
	// mux.Handle("/log_level", logLevelEventHandler)
	mux.Handle("/log_level", atom)
	setupLog.Info(fmt.Sprintf("Webhookd listens on: 0.0.0.0:%s", port))
	setupLog.Info(http.ListenAndServe(fmt.Sprintf(":%s", port), mux).Error())
}

// InsertEventHandleFunc handle tenants table insert event
func InsertEventHandleFunc(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		fmt.Fprintln(w, "ok")
	} else if r.Method == "POST" {
		setupLog.Info("webhook received a request")

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
		err = json.Unmarshal(body, &v)
		if err != nil {
			panic(err)
		}

		t := T{
			CName: v.Event.Data.New.Cname,
			UUID:  v.Event.Data.New.UUID,
		}

		fmt.Printf("%#v\n", t)

		// log.Info().Msgf("uuid: %s", t.UUID)
		// log.Info().Msgf("cname: %s", t.CName)

		_ = helper.CreateTenant(t.CName, t.CName, t.UUID)

		// log.Info().Msgf("created tenant %s with uuid: %s", t.CName, t.UUID)

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
	// log.Fatal().Msg(err.Error())
	os.Exit(-1)
}

// T Tenant
// the name and namespace are CName, they are the same
type T struct {
	CName string
	UUID  string
}
