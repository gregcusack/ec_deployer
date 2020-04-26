package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Maziyar-Na/EC-Agent/msg"
	"github.com/golang/protobuf/proto"
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
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const AGENT_PORT = ":4445"
const BUFFSIZE = 2048

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
	//podClient := clientset.CoreV1().Pods(apiv1.NamespaceDefault)

	//Generate Pod Defs and Deploy Pods
	for i := 0; i < len(dcDefs.DCDefs[0].PodNames); i++ {
		pod := createPodDefinition(&dcDefs.DCDefs[0].PodNames[i], &dcDefs.DCDefs[0].Images[i], &dcDefs)
		//result, err := podClient.Create(context.TODO(), pod, metav1.CreateOptions{})
		//
		//if err != nil {
		//	panic(err)
		//}
		//fmt.Println("Pod created successfully: " + result.GetObjectMeta().GetName())

		//Get Nodes from pod names
		podObj, _ := clientset.CoreV1().Pods(apiv1.NamespaceDefault).Get(context.TODO(), pod.Name, metav1.GetOptions{})

		nodeObj,_ := clientset.CoreV1().Nodes().Get(context.TODO(), podObj.Spec.NodeName, metav1.GetOptions{})
		nodeIP := nodeObj.Status.Addresses[0].Address
		fmt.Println("Node Name: " + podObj.Spec.NodeName + ", Node ip: " + nodeIP)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for {
			//out := podObj.Status.Phase
			pObj, _ := clientset.CoreV1().Pods(apiv1.NamespaceDefault).Get(context.TODO(), pod.Name, metav1.GetOptions{})
			out := pObj.Status.Phase
			//fmt.Println(out)
			if string(out) == "Running" || ctx.Err() != nil {
				fmt.Println("Pod Status: " + string(out))
				break
			}
		}

		//TODO: Get agent from node IP
		//Should be resolved when we add agent to kubelet
		//We know IP and we know port (4445), so just send a request to it
		//fmt.Println(podObj)
		getPodInfoFromAgent(nodeIP, podObj.Name)

		//TODO: get cgroup ID from pod

		break

	}




	//get node ips from node names

}


func getPodInfoFromAgent(agentIP, podName string) {
	txMsg := &msg_struct.ECMessage{
		ClientIp: proto.String("127.0.0.1"),
		CgroupId: proto.Int32(0),
		ReqType: proto.Int32(6),
		PayloadString: proto.String(podName),
		//RsrcAmnt: proto.Uint64(9),
		//Quota: proto.Uint64(8),
	}
	fmt.Println(txMsg)
	txMsgMarshal, err := proto.Marshal(txMsg)
	checkError(err)
	fmt.Println(txMsgMarshal)

	fmt.Println(agentIP + AGENT_PORT)
	conn, _ := net.Dial("tcp", agentIP + AGENT_PORT)
	defer conn.Close()
	n, err := conn.Write(txMsgMarshal)
	checkError(err)
	fmt.Println("Sent " + strconv.Itoa(n) + " bytes")

	//buff := make([]byte, BUFFSIZE)
	rxMsg := &msg_struct.ECMessage{}
	err = proto.Unmarshal(txMsgMarshal,rxMsg)
	checkError(err)
	fmt.Println(rxMsg)






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

func checkError(err error) {
	if err != nil {
		log.Println("[ERROR]: " + err.Error())
	}
}