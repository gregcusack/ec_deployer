package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/gregcusack/ec_deployer/structs"
	"io/ioutil"
	apiv1 "k8s.io/api/core/v1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"log"
	"os"
	"path/filepath"
	"strconv"
)



func main() {

	args := os.Args[1:]

	if len(args) != 1 {
		log.Fatal("Pls pass in deploy file via: ./main .go <json-file>")
	}

	jsonFile, err := os.Open(args[0])
	if err != nil {
		log.Println(err)
	}
	defer jsonFile.Close()

	byteVal, _ := ioutil.ReadAll(jsonFile)

	//var dcDefs DcDefs
	var dcDefs structs.DeploymentDefinition

	json.Unmarshal(byteVal, &dcDefs)

	clientset := configK8()
	podClient := clientset.CoreV1().Pods(apiv1.NamespaceDefault)

	//Generate Pod Defs and Deploy Pods
	for i := 0; i < len(dcDefs.DCDefs[0].PodNames); i++ {
		pod := createPodDefinition(&dcDefs.DCDefs[0].PodNames[i], &dcDefs.DCDefs[0].Images[i], &dcDefs)
		result, err := podClient.Create(context.TODO(), pod, metav1.CreateOptions{})

		if err != nil {
			panic(err)
		}
		fmt.Println("Pod created successfully: " + result.GetObjectMeta().GetName())
	}

}
func configK8() *kubernetes.Clientset {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	return clientset
}

func createPodDefinition(podName *string, appImage *string, depDef *structs.DeploymentDefinition) *core.Pod {
	specs := depDef.DCDefs[0].Specs
	return &core.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      *podName,
			Namespace: "default",
			Labels: map[string]string{
				"name": *podName,
			},
		},
		Spec: core.PodSpec{
			Containers: []core.Container{
				{
					Name:            *podName,
					Image:           *appImage,
					ImagePullPolicy: core.PullIfNotPresent,
					Resources: apiv1.ResourceRequirements {
						Requests: apiv1.ResourceList {
							"memory": resource.MustParse(strconv.Itoa(specs.Mem) + "Mi"),
							"cpu": resource.MustParse(strconv.Itoa(specs.CPU) + "m"),
						},
						Limits: apiv1.ResourceList {
							"memory": resource.MustParse(strconv.Itoa(specs.Mem) + "Mi"),
							"cpu": resource.MustParse(strconv.Itoa(specs.CPU) + "m"),
						},

					},

				},
			},
		},
	}
}