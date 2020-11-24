package helper

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	operatorsv1alpha1 "github.com/dfang/tenant-operator/api/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// controller runtime
// ctrl.GetConfigOrDie()

// GetClientSet Get a typed clientset
func GetClientSet() *kubernetes.Clientset {
	cfg := ctrl.GetConfigOrDie()
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		panic(err)
	}
	return clientset
}

// GetClientOrDie return controller runtime's client.Client or die
// https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client#Client
func GetClientOrDie() client.Client {
	cl, err := client.New(config.GetConfigOrDie(), client.Options{})
	if err != nil {
		fmt.Println("failed to create client")
		os.Exit(1)
	}
	return cl
}

// CreateNamespace CreateNamespace by name
func CreateNamespace(nsName string) error {
	clientset := GetClientSet()

	// query namespace by name, if not exist, create it
	_, err := clientset.CoreV1().Namespaces().Get(
		context.TODO(),
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
		_, err := clientset.CoreV1().Namespaces().Create(context.TODO(), nsSpec, metav1.CreateOptions{})
		if err != nil {
			panic(err)
		}
	}

	return nil
}

// CreateNamespaceIfNotExist create namespace if not exist
func CreateNamespaceIfNotExist(nsName string) error {
	// query namespace by name, if not exist, create it
	ns := &corev1.Namespace{}
	err := GetClientOrDie().Get(context.Background(), client.ObjectKey{
		Name: nsName,
	}, ns)

	if err != nil {
		ns.Name = nsName
		if err := GetClientOrDie().Create(context.Background(), ns); err != nil {
			return err
		}
		fmt.Println("namespace created ")
	}

	return nil
}

// DeleteNamespaceIfExist delete namespace if exists
func DeleteNamespaceIfExist(nsName string) error {
	// query namespace by name, if not exist, create it
	ns := &corev1.Namespace{}
	if err := GetClientOrDie().Get(context.Background(), client.ObjectKey{Name: nsName}, ns); err != nil {
		return err
	}

	if err := GetClientOrDie().Delete(context.Background(), ns); err != nil {
		return err
	}

	return nil
}

// DoSSA do server side side apply yaml
func DoSSA(ctx context.Context, cfg *rest.Config, yamlContent string) (*unstructured.Unstructured, error) {

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

	// 6. Marshal object into JSON
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}

	// fmt.Println(string(data))

	// 7. Create or Update the object with SSA
	//     types.ApplyPatchType indicates SSA.
	//     FieldManager specifies the field owner ID.
	unstructuredObj, err := dr.Patch(context.TODO(), obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{
		FieldManager: "tenant-controller",
	})
	if err != nil {
		fmt.Println(err)
	}

	return unstructuredObj, err
}

// GenRandPassword Generate Random Password
// https://yourbasic.org/golang/generate-random-string/
func GenRandPassword(n int) string {
	rand.Seed(time.Now().UnixNano())
	digits := "0123456789"
	specials := "~=+%^*/()[]{}/!@#$?|"
	all := "ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"abcdefghijklmnopqrstuvwxyz" +
		digits + specials
	length := n
	buf := make([]byte, length)
	buf[0] = digits[rand.Intn(len(digits))]
	buf[1] = specials[rand.Intn(len(specials))]
	for i := 2; i < length; i++ {
		buf[i] = all[rand.Intn(len(all))]
	}
	rand.Shuffle(len(buf), func(i, j int) {
		buf[i], buf[j] = buf[j], buf[i]
	})
	str := string(buf)

	return str
}

// GetConn get a db connection
func GetConn(host, port, user, password, dbname string) *sql.DB {
	psqlInfo := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)
	// fmt.Println(psqlInfo)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		panic(err)
	}

	err = db.Ping()
	if err != nil {
		panic(err)
	}

	return db
}

// DropDB drop db when remove a tenant
func DropDB(db *sql.DB, dbName string) {
	defer db.Close()

	_, err := db.Exec(fmt.Sprintf(`DROP DATABASE "%s"`, dbName))
	if err != nil {
		fmt.Println(err.Error())
	} else {
		fmt.Println("Successfully dropped database..")
	}
}

// DropUser drop user when remove a tenant
func DropUser(db *sql.DB, userName string) {
	defer db.Close()

	_, err := db.Exec(fmt.Sprintf(`DROP USER "%s"`, userName))
	if err != nil {
		fmt.Println(err.Error())
	} else {
		fmt.Println("Successfully dropped user..")
	}
}

// CreateTenant create a tenant when received a hasura insert event
func CreateTenant(name, namespace, uuid string) error {
	// log.Info().Msgf("Create namespace %s", t.CName)

	// t1 := &unstructured.Unstructured{}
	// t1.SetGroupVersionKind(schema.GroupVersionKind{
	// 	Group:   operatorsv1alpha1.GroupVersion.Group,
	// 	Version: operatorsv1alpha1.GroupVersion.Version,
	// 	Kind:    "Tenant",
	// })

	// create a tenant CR
	// apiVersion: operators.jdwl.in/v1alpha1
	// kind: Tenant
	// metadata:
	//   name: {{ .CName }}
	//   namespace: {{ .CName }}
	// spec:
	//   cname: {{ .CName }}
	//   replicas: 1
	//   uuid: {{ .UUID }}

	err := CreateNamespaceIfNotExist(namespace)
	if err != nil {
		panic(err)
	}

	var t1 operatorsv1alpha1.Tenant
	t1.TypeMeta = metav1.TypeMeta{
		APIVersion: operatorsv1alpha1.GroupVersion.String(),
		Kind:       "Tenant",
	}
	t1.ObjectMeta = metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
	}
	t1.Spec = operatorsv1alpha1.TenantSpec{
		UUID:     uuid,
		CName:    name,
		Replicas: 1,
	}

	cl := GetClientOrDie()
	if err = cl.Create(context.TODO(), &t1); err != nil {
		return nil
	}
	// Create(ctx context.Context, obj runtime.Object, opts ...CreateOption) error

	return nil
}

// // EmbedTemplate EmbedTemplate
// // pkger.Open() 不支持字符串变量，只支持字符串
// func EmbedTemplate(tpl string) (string, error) {
// 	f, err := pkger.Open(tpl)
// 	if err != nil {
// 		return "", err
// 	}
// 	defer f.Close()

// 	info, err := f.Stat()
// 	if err != nil {
// 		return "", err
// 	}

// 	fmt.Println("Name: ", info.Name())
// 	fmt.Println("Size: ", info.Size())
// 	fmt.Println("Mode: ", info.Mode())
// 	fmt.Println("ModTime: ", info.ModTime())

// 	if b, err := ioutil.ReadAll(f); err != nil {
// 		return "", err
// 	} else {
// 		return string(b), nil
// 	}
// }
