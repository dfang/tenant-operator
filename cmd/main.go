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
	"strconv"

	_ "github.com/lib/pq"
	"github.com/olekukonko/tablewriter"

	haikunator "github.com/atrox/haikunatorgo/v2"
	operatorsv1alpha1 "github.com/dfang/tenant-operator/api/v1alpha1"
	"github.com/dfang/tenant-operator/pkg/helper"
	"github.com/hashicorp/consul/api"
	"github.com/urfave/cli/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	scheme = runtime.NewScheme()
)

var (
	host     = envOrDefault("TENANTS_DB_HOST", "localhost")
	port     = envOrDefault("TENANTS_DB_PORT", "5432")
	user     = envOrDefault("TENANTS_DB_USER", "postgres")
	password = envOrDefault("TENANTS_DB_PASSWORD", "localhost")
	dbname   = envOrDefault("TENANTS_DB_NAME", "tenants")
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
		Name:  "tenant",
		Usage: "make an explosive entrance",
		Action: func(c *cli.Context) error {
			// fmt.Println("boom! I say!")
			// cli.ShowAppHelp(c)
			// fmt.Println(c.Args())
			listTenants(kv, c)
			return nil
		},
		Flags: []cli.Flag{
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
				// SkipFlagParsing: true,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "o",
						Usage: "output (-o uuid,cname)",
					},
				},
				Action: func(c *cli.Context) error {
					// fmt.Println(c.String("o"))
					listTenants(kv, c)
					return nil
				},
			},
			{
				Name:    "add",
				Aliases: []string{"a"},
				Usage:   "add a tenant",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "cname",
						Usage: "cname for tenant",
					},
					&cli.StringFlag{
						Name:  "uuid",
						Usage: "uuid for tenant",
					},
					&cli.IntFlag{
						Name:     "replicas",
						Required: false,
						Value:    1,
						Usage:    "replicas for tenant",
					},
					&cli.BoolFlag{
						Name:     "dry-run",
						Required: false,
						Value:    false,
						Usage:    "dry-run (ouput yaml to os.stdout)",
					},
				},
				Action: func(c *cli.Context) error {
					// if c.NArg() > 0 {
					// }
					// fmt.Println(c.Args())
					// fmt.Println(c.String("cname"))
					// fmt.Println(c.String("uuid"))
					addTenant(kv, c)
					return nil
				},
			},
			{
				Name:    "update",
				Aliases: []string{"u"},
				Usage:   "update a tenant cname or replicas",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "cname",
						Required: true,
						Usage:    "cname for tenant",
					},
					&cli.StringFlag{
						Name:     "uuid",
						Required: true,
						Usage:    "uuid for tenant",
					},
					&cli.IntFlag{
						Name:     "replicas",
						Required: false,
						Value:    1,
						Usage:    "replicas for tenant",
					},
				},
				Action: func(c *cli.Context) error {
					updateTenant(kv, c)
					return nil
				},
			},
			{
				Name:    "scale",
				Aliases: []string{"s"},
				Usage:   "scale replicas of deployment for a tenant by `uuid`",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "u",
						Required: true,
						Usage:    "uuid for tenant",
					},
					&cli.StringFlag{
						Name:     "n",
						Required: true,
						Usage:    "replicas count",
					},
				},
				Action: func(c *cli.Context) error {
					// if c.NArg() > 0 {
					// 	// deleteTenant(kv, c.Args().Get(0))
					// }
					// if c.String("u") == "" {
					// 	cli.ShowSubcommandHelp(c)
					// 	return nil
					// }
					scaleTenant(kv, c)
					return nil
				},
			},
			{
				Name:    "sleep",
				Aliases: []string{"sleep"},
				Usage:   "sleep a tenant by `uuid`",
				Flags:   []cli.Flag{},
				Action: func(c *cli.Context) error {
					sleepTenant(kv, c)
					return nil
				},
			},
			{
				Name:    "wakeup",
				Aliases: []string{"wakeup"},
				Usage:   "wakeup a tenant by `uuid`",
				Flags:   []cli.Flag{},
				Action: func(c *cli.Context) error {
					wakeupTenant(kv, c)
					return nil
				},
			},
			{
				Name:    "delete",
				Aliases: []string{"d"},
				Usage:   "delete a tenant by `cname`",
				Action: func(c *cli.Context) error {
					deleteTenant(kv, c)
					return nil
				},
			},
			{
				Name:    "purge",
				Aliases: []string{"c"},
				Usage:   "purge all tenants",
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

func addTenant(kv *api.KV, c *cli.Context) error {
	replicas := c.Int("replicas")
	dryRun := c.Bool("dry-run")
	tenantKey := c.String("uuid")
	cnameV := c.String("cname")

	if tenantKey == "" {
		tenantKey, _ = randomHex(10)
	}

	if c.String("cname") == "" {
		haikunator := haikunator.New()
		haikunator.TokenLength = 9
		haikunator.TokenHex = true
		cnameV = haikunator.Haikunate()
	}

	t := operatorsv1alpha1.Tenant{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "Tenant",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cnameV,
			Namespace: cnameV,
		},
		Spec: operatorsv1alpha1.TenantSpec{
			UUID:     tenantKey,
			CName:    cnameV,
			Replicas: int32(replicas),
		},
	}

	if dryRun {
		// https://miminar.fedorapeople.org/_preview/openshift-enterprise/registry-redeploy/go_client/serializing_and_deserializing.html
		// Create a YAML serializer.  JSON is a subset of YAML, so is supported too.
		ysz := json.NewYAMLSerializer(json.DefaultMetaFactory, clientgoscheme.Scheme, clientgoscheme.Scheme)

		// Encode the object to YAML.
		err := ysz.Encode(&t, os.Stdout)
		if err != nil {
			fmt.Println(err)
		}
		return nil
	}

	// client from controller runtime
	cl, err := client.New(config.GetConfigOrDie(), client.Options{})
	if err != nil {
		fmt.Println("failed to create client")
		os.Exit(1)
	}

	if err := helper.CreateNamespaceIfNotExist(cnameV); err != nil {
		fmt.Println("failed to create namespace")
		os.Exit(1)
	}

	fmt.Printf("Created tenant, uuid: %s, cname: %s, replicas: %d\n", tenantKey, cnameV, replicas)
	err = cl.Create(context.Background(), &t)
	if err != nil {
		fmt.Println("failed to create tenant", err)
		os.Exit(1)
	}

	return nil
}

func deleteTenant(kv *api.KV, c *cli.Context) {
	// delete tenant namespace
	// remove key from consul

	if c.NArg() == 0 {
		cli.ShowSubcommandHelp(c)
		os.Exit(0)
	}

	cl := helper.GetClientOrDie()
	cname := c.Args().Get(0)

	ns := &corev1.Namespace{}
	err := cl.Get(context.Background(), client.ObjectKey{
		Name: cname,
	}, ns)

	// fmt.Println(err)
	// fmt.Println(ns)

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	} else {
		// err == nil 时， ns 存在
		if err = helper.DeleteNamespaceIfExist(cname); err != nil {
			log.Panic(err)
		}
	}
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
		_ = helper.DeleteNamespaceIfExist(string(cname.Value))
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

func listTenants(kv *api.KV, c *cli.Context) {
	data := make([][]string, 10, 10)
	tl := &operatorsv1alpha1.TenantList{}

	newListOpts := &client.ListOptions{
		Limit: 100,
		// FieldSelector: f.AsSelector(),
	}
	m := client.MatchingFields{}
	m.ApplyToList(newListOpts)

	_ = helper.GetClientOrDie().List(context.Background(), tl, newListOpts)
	// cl is a created client.Client
	// _ = helper.GetClientOrDie().List(context.Background(), tl, &client.ListOptions{
	// 	// LabelSelector: labels.SelectorFromSet(map[string]string{"app": "sample"}),
	// 	// FieldSelector: fields.Nothing(),
	// 	FieldSelector: f.AsSelector(),
	// })

	for _, t := range tl.Items {
		s := []string{t.Spec.UUID, t.Spec.CName, t.Status.URL, t.Status.Status, strconv.FormatInt(int64(t.Status.Replicas), 10)}
		data = append(data, s)
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"UUID", "CName", "URL", "Status", "Replicas"})
	table.SetAutoFormatHeaders(true)
	table.AppendBulk(data) // Add Bulk Data
	// table.SetCenterSeparator("")
	// table.SetRowSeparator("")
	// table.SetColumnSeparator("")
	// table.SetBorder(false)
	table.Render()
}

func scaleTenant(kv *api.KV, c *cli.Context) {
	if c.String("u") == "" {
		cli.ShowSubcommandHelp(c)
		return
	}

	var replicas int
	uuid := c.String("u")
	if c.String("n") == "" {
		replicas = 0
	} else {
		i, err := strconv.Atoi(c.String("n"))
		if err != nil {
			panic(err)
		}
		replicas = i
	}

	prefix := "tenants/" + uuid + "/cname"
	ns, _, err := kv.Get(prefix, nil)
	if err != nil {
		fmt.Println(err)
		return
	}

	if c.String("u") != "" && c.String("n") != "" {
		ns := string(ns.Value)
		ScaleNamespace(ns, replicas)
	}
}

func sleepTenant(kv *api.KV, c *cli.Context) {
	if c.NArg() == 0 {
		cli.ShowSubcommandHelp(c)
		return
	}

	fmt.Println("sleep tenant", c.Args().Get(0))

	uuid := c.Args().Get(0)
	prefix := "tenants/" + uuid + "/cname"
	ns, _, err := kv.Get(prefix, nil)
	if err != nil {
		fmt.Println(err)
		return
	}

	// client from controller runtime
	cl, err := client.New(config.GetConfigOrDie(), client.Options{})
	if err != nil {
		fmt.Println("failed to create client")
		os.Exit(1)
	}

	t := &operatorsv1alpha1.Tenant{}
	err = cl.Get(context.Background(), client.ObjectKey{
		Namespace: string(ns.Value),
		Name:      string(ns.Value),
	}, t)
	if err != nil {
		fmt.Println("failed to get tenant")
		fmt.Println(err)
		os.Exit(1)
	}

	t.Spec.Replicas = int32(0)

	err = cl.Update(context.Background(), t)
	if err != nil {
		fmt.Println("failed to update tenant")
		fmt.Println(err)
		os.Exit(1)
	}

}

func wakeupTenant(kv *api.KV, c *cli.Context) {
	if c.NArg() == 0 {
		cli.ShowSubcommandHelp(c)
		return
	}

	fmt.Println("wakeup tenant", c.Args().Get(0))

	uuid := c.Args().Get(0)
	prefix := "tenants/" + uuid + "/cname"
	ns, _, err := kv.Get(prefix, nil)
	if err != nil {
		fmt.Println(err)
		return
	}

	// client from controller runtime
	cl, err := client.New(config.GetConfigOrDie(), client.Options{})
	if err != nil {
		fmt.Println("failed to create client")
		os.Exit(1)
	}

	t := &operatorsv1alpha1.Tenant{}
	err = cl.Get(context.Background(), client.ObjectKey{
		Namespace: string(ns.Value),
		Name:      string(ns.Value),
	}, t)
	if err != nil {
		fmt.Println("failed to get tenant")
		fmt.Println(err)
		os.Exit(1)
	}

	t.Spec.Replicas = int32(1)

	err = cl.Update(context.Background(), t)
	if err != nil {
		fmt.Println("failed to update tenant")
		fmt.Println(err)
		os.Exit(1)
	}

}

func updateTenant(kv *api.KV, c *cli.Context) error {
	uuid := c.String("uuid")
	cname := c.String("cname")
	replicas := c.Int("replicas")

	// PUT a KV pair
	if err := putKey(kv, "tenants/"+uuid+"/cname", cname); err != nil {
		return err
	}

	// PUT a KV pair
	if err := putKey(kv, "tenants/"+uuid+"/replicas", strconv.Itoa(replicas)); err != nil {
		return err
	}

	return nil
}

func randomHex(n int) (string, error) {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// ScaleNamespace scale replicas to n for deployments in a namespace
func ScaleNamespace(ns string, replicas int) {
	clientset := getClientSet()

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
			log.Fatal(err)
		}
		sc.Spec.Replicas = int32(replicas)

		scale, err := clientset.AppsV1().
			Deployments(item.Namespace).
			UpdateScale(context.TODO(), item.Name, sc, metav1.UpdateOptions{})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Set namespace %s replicas to %d\n", item.Name, scale.Spec.Replicas)
	}
}

func getClientSet() *kubernetes.Clientset {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		panic(err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}
	return clientset
}

func putKey(kv *api.KV, k, v string) error {
	// p2 := &api.KVPair{Key: "tenants/" + uuid + "/replicas", Value: []byte(strconv.Itoa(replicas))}
	p2 := &api.KVPair{Key: k, Value: []byte(v)}
	_, err := kv.Put(p2, nil)
	if err != nil {
		fmt.Println(err)
		return err
	}
	return nil
}

func envOrDefault(v, def string) string {
	if os.Getenv(v) != "" {
		return os.Getenv(v)
	}
	return def
}
