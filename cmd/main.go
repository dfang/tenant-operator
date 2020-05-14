package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"text/tabwriter"

	haikunator "github.com/atrox/haikunatorgo/v2"
	"github.com/hashicorp/consul/api"
	"github.com/urfave/cli/v2"
	operatorsv1alpha1 "jdwl.in/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	operatorsv1alpha1.AddToScheme(clientgoscheme.Scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = operatorsv1alpha1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	// Get a new client
	client, err := api.NewClient(api.DefaultConfig())
	if err != nil {
		panic(err)
	}

	// Get a handle to the KV API
	kv := client.KV()

	app := &cli.App{
		Name:  "boom",
		Usage: "make an explosive entrance",
		Action: func(c *cli.Context) error {
			// fmt.Println("boom! I say!")
			// cli.ShowAppHelp(c)
			listTenants(kv)
			return nil
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "lang, l",
				Value: "english",
				Usage: "Language for the greeting",
			},
			&cli.StringFlag{
				Name:  "config, c",
				Usage: "Load configuration from `FILE`",
			},
		},
		Commands: []*cli.Command{
			{
				Name:    "list",
				Aliases: []string{"l"},
				Usage:   "list tenants",
				Action: func(c *cli.Context) error {
					listTenants(kv)
					return nil
				},
			},
			{
				Name:    "add",
				Aliases: []string{"a"},
				Usage:   "add a tenant",
				Action: func(c *cli.Context) error {
					addTenant(kv)
					return nil
				},
			},
			{
				Name:    "delete",
				Aliases: []string{"d"},
				Usage:   "delete a tenant",
				Action: func(c *cli.Context) error {
					if c.NArg() > 0 {
						deleteTenant(kv, c.Args().Get(0))
					}
					return nil
				},
			},
			{
				Name:    "purge",
				Aliases: []string{"l"},
				Usage:   "purge tenants",
				Action: func(c *cli.Context) error {
					purgeTenants(kv)
					return nil
				},
			},
		},
	}

	err = app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}

	// haikunator := haikunator.New()
	// haikunator.TokenLength = 9
	// haikunator.TokenHex = true
	// fmt.Println(haikunator.Haikunate())

	// // Lookup the pair
	// pair, _, err := kv.Get("tenants/", nil)
	// if err != nil {
	// 	panic(err)
	// }
	// fmt.Printf("KV: %v %s\n", pair.Key, pair.Value)
}

func addTenant(kv *api.KV) error {
	tenantKey, _ := randomHex(10)
	uuid := "tenants/" + tenantKey + "/uuid"
	cname := "tenants/" + tenantKey + "/cname"

	// PUT a new KV pair
	p := &api.KVPair{Key: uuid, Value: []byte(tenantKey)}
	_, err := kv.Put(p, nil)
	if err != nil {
		fmt.Println(err)
		return err
	}

	haikunator := haikunator.New()
	haikunator.TokenLength = 9
	haikunator.TokenHex = true

	// PUT a new KV pair
	p1 := &api.KVPair{Key: cname, Value: []byte(haikunator.Haikunate())}
	_, err = kv.Put(p1, nil)
	if err != nil {
		fmt.Println(err)
		return err
	}

	_ = operatorsv1alpha1.TenantNamespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "TenantNamespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      string(p1.Value),
			Namespace: "default",
			Labels:    map[string]string{"namespace-owner": "tenant"},
		},
		Spec: operatorsv1alpha1.TenantNamespaceSpec{
			Name: string(p.Value),
		},
	}

	tn := operatorsv1alpha1.Tenant{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "Tenant",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      string(p1.Value),
			Namespace: string(p1.Value),
		},
		Spec: operatorsv1alpha1.TenantSpec{
			UUID:  string(p.Value),
			CName: string(p1.Value),
		},
	}

	cl, err := client.New(config.GetConfigOrDie(), client.Options{})
	if err != nil {
		fmt.Println("failed to create client")
		os.Exit(1)
	}

	fmt.Println("Create namespace")
	createNamespace(string(p1.Value))

	// fmt.Println("Create tenant namespace")
	// err = cl.Create(context.Background(), &ns)
	// if err != nil {
	// 	fmt.Println("failed to create tenant namespace")
	// 	fmt.Println(err)
	// 	os.Exit(1)
	// }

	fmt.Println("Create tenant")
	err = cl.Create(context.Background(), &tn)
	if err != nil {
		fmt.Println("failed to create tenant")
		fmt.Println(err)
		os.Exit(1)
	}

	return nil
}

func deleteTenant(kv *api.KV, uuid string) error {
	// delete tenant namespace
	// remove key from consul
	// 03a90b115da101169870

	prefix := "tenants/" + uuid + "/cname"
	cname, _, err := kv.Get(prefix, nil)
	if err != nil {
		fmt.Println(err)
		return err
	}
	if cname == nil {
		fmt.Printf("tenant %s not exist\n", uuid)
		return nil
	}
	deleteNamespace(string(cname.Value))

	_, err = kv.DeleteTree("tenants/"+uuid+"/", nil)
	fmt.Println("delete key", "tenants/"+uuid)
	if err != nil {
		fmt.Println(err)
		return err
	}

	return nil
}

func purgeTenants(kv *api.KV) error {
	// delete namespaces
	// purge consul keys

	keys, _, err := kv.Keys("tenants/", "/", &api.QueryOptions{})
	// kvPairs, _, err := kv.List("tenants/", &api.QueryOptions{})
	if err != nil {
		fmt.Println(err)
		return err
	}
	for _, v := range keys {
		cname, _, err := kv.Get(v+"cname", nil)
		if err != nil {
			fmt.Println(err)
			return err
		}
		// tenants/03a90b115da101169870/
		fmt.Println("delete namespace ", string(cname.Value))
		deleteNamespace(string(cname.Value))
	}

	cmd := exec.Command("consul", "kv", "delete", "-recurse", "tenants/")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		log.Fatalf("cmd.Run() failed with %s\n", err)
	}
	outStr, errStr := string(stdout.Bytes()), string(stderr.Bytes())
	// fmt.Printf("out:\n%s\nerr:\n%s\n", outStr, errStr)
	if outStr != "" {
		fmt.Printf("\n%s\n", outStr)
	}
	if errStr != "" {
		fmt.Printf("error: \n%s\n", errStr)
	}

	return nil
}

func listTenants(kv *api.KV) {
	keys, _, err := kv.Keys("tenants/", "/", &api.QueryOptions{})
	// kvPairs, _, err := kv.List("tenants/", &api.QueryOptions{})
	if err != nil {
		panic(err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 5, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "UID\t\tCName")
	for _, v := range keys {
		uuid, _, err := kv.Get(v+"uuid", nil)
		if err != nil {
			panic(err)
		}
		cname, _, err := kv.Get(v+"cname", nil)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(w, "%s\t\t%s\n", uuid.Value, cname.Value)
	}
	w.Flush()
}

func randomHex(n int) (string, error) {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func createNamespace(nsName string) error {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		panic(err)
	}
	clientset, err := kubernetes.NewForConfig(config)

	// query namespace by name, if not exist, create it
	_, err = clientset.CoreV1().Namespaces().Get(nsName, metav1.GetOptions{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(), Kind: "Namespace",
		},
	})

	if err != nil {
		nsSpec := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   nsName,
				Labels: map[string]string{"owner": "tenant"},
			},
		}
		ns, err := clientset.CoreV1().Namespaces().Create(nsSpec)
		if err != nil {
			panic(err)
		}
		fmt.Println(ns.Name)
	}

	return nil
}

func deleteNamespace(nsName string) error {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		panic(err)
	}
	clientset, err := kubernetes.NewForConfig(config)

	err = clientset.CoreV1().Namespaces().Delete(nsName, &metav1.DeleteOptions{
		// TODO
		// GracePeriodSeconds: &int64(0),
		// PropagationPolicy:  &metav1.DeletionPropagation.DeletePropagationBackground,
	})
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}
