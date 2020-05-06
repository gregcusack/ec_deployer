package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	pb "github.com/Maziyar-Na/EC-Agent/grpc"
	dgrpc "github.com/gregcusack/ec_deployer/DeployServerGRPC"
	"github.com/gregcusack/ec_deployer/structs"
	"google.golang.org/grpc"
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
	"time"
)

const AGENT_GRPC_PORT = ":4446"
const GCM_GRPC_PORT = ":4447"
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

	err = json.Unmarshal(byteVal, &dcDefs)
	if err != nil {
		log.Println("Error reading json file: " + err.Error())
	}

	clientset := configK8()
	podClient := clientset.CoreV1().Pods(apiv1.NamespaceDefault)
	//fmt.Println(byteVal)

	//Generate Pod Defs and Deploy Pods
	for i := 0; i < len(dcDefs.DCDefs[0].PodNames); i++ {
		pod := createPodDefinition(&dcDefs.DCDefs[0].PodNames[i], &dcDefs.DCDefs[0].Images[i], &dcDefs)
		result, err := podClient.Create(context.TODO(), pod, metav1.CreateOptions{})

		if err != nil {
			panic(err)
		}
		fmt.Println("Pod created successfully: " + result.GetObjectMeta().GetName())

		//Get Nodes from pod names
		podObj, _ := clientset.CoreV1().Pods(apiv1.NamespaceDefault).Get(context.TODO(), pod.Name, metav1.GetOptions{})

		nodeObj,_ := clientset.CoreV1().Nodes().Get(context.TODO(), podObj.Spec.NodeName, metav1.GetOptions{})
		//fmt.Println(nodeObj.Status.Addresses)
		//nodeIP := nodeObj.Status.Addresses[0].Address
		//fmt.Println("Node Name: " + podObj.Spec.NodeName + ", Node ip: " + nodeIP)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for {
			//out := podObj.Status.Phase
			podObj, _ = clientset.CoreV1().Pods(apiv1.NamespaceDefault).Get(context.TODO(), pod.Name, metav1.GetOptions{})
			out := podObj.Status.Phase
			//fmt.Println(out)
			if string(out) == "Running" || ctx.Err() != nil {
				fmt.Println("Pod Status: " + string(out))
				break
			}
		}

		//TODO: Get agent from node IP
		//Should be resolved when we add agent to kubelet
		//We know IP and we know port (4445), so just send a request to it
		//fmt.Println(podObj.Status.ContainerStatuses[0].ContainerID)

		//getPodInfoFromAgent(nodeIP, podObj.Name)
		nodeIP := nodeObj.Status.Addresses[0].Address

		fmt.Println(nodeIP)
		//dockerID := podObj.Status.ContainerStatuses[0].ContainerID[9:]

		cgId, dockerID := connectContainerRequest(nodeIP, podObj.Name)
		exportDeployPodSpec(nodeIP, dockerID, cgId)

		//TODO: get cgroup ID from pod

		//break

	}




	//get node ips from node names

}
//
//TODO: json file should have port
func exportDeployPodSpec(gcmIP string, dockerID string, cgroupId int32) {
	conn, err := grpc.Dial( gcmIP + GCM_GRPC_PORT, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := dgrpc.NewDeployerExportClient(conn)

	txMsg := &dgrpc.ExportPodSpec{
		DockerId: dockerID,
		CgroupId: cgroupId,
		NodeIp: gcmIP,
	}

	fmt.Println(txMsg)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	r, err := c.ReportPodSpec(ctx, txMsg)
	if err != nil {
		log.Fatalf("could not ExportPodSpec: %v", err)
	}
	log.Println("Rx back from gcm: ", r.GetDockerId(), r.GetCgroupId(), r.GetNodeIp(), r.GetThanks())

}


func connectContainerRequest(agentIP, podName string) (int32, string) {
	conn, err := grpc.Dial(agentIP + AGENT_GRPC_PORT, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewHandlerClient(conn)

	//TODO: agentIP needs to be GcmIP
	txMsg := &pb.ConnectContainerRequest{
		GcmIP: agentIP,
		PodName: podName,
	}
	fmt.Println(txMsg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := c.ReqConnectContainer(ctx,txMsg)
	if err != nil {
		log.Fatalf("could not greet: %v", err)
	}
	log.Println("Rx back: ", r.GetPodName(), r.GetDockerID(), r.GetCgroupID())
	return r.GetCgroupID(), r.GetDockerID()

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