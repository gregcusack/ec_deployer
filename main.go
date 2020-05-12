package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"io/ioutil"
	"strings"

	"github.com/gregcusack/ec_deployer/structs"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/apimachinery/pkg/api/resource"

	// PodWatcher
	"github.com/gregcusack/ec_deployer/podWatcher"

	dgrpc "github.com/gregcusack/ec_deployer/DeployServerGRPC"
	"google.golang.org/grpc"
)
const GCM_GRPC_PORT = ":4447"

func main() {
	// First, we parse the application definition file for app statisitics
	appDefFilePtr := flag.String("f", "", "App Definition File to parse. (Required)")
	flag.Parse()

	if *appDefFilePtr == "" {
		fmt.Println("Must pass in a file path to the app definition file: ")
		flag.PrintDefaults()
		os.Exit(1)
	}

	jsonAppDefFile, err := os.Open(*appDefFilePtr)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fmt.Printf("[DBG] Successfully opened %s\n", *appDefFilePtr)
	defer jsonAppDefFile.Close()

	jsonByteValue, _ := ioutil.ReadAll(jsonAppDefFile)
	var dcDefs structs.DeploymentDefinition
	err = json.Unmarshal(jsonByteValue, &dcDefs)
	if err != nil {
		log.Println("Error reading json file: " + err.Error())
	}

	fmt.Printf("[DBG] Configuring K8s ClientSet\n")
	clientset := configK8()

	// For multiple applications
	for _, appDef := range dcDefs.DCDefs {
		
		appName := appDef.Name
		agentIPs := appDef.AgentIPs
		gcmIP := appDef.GcmIP
		deploymentPath := appDef.DeploymentPath
		namespace := appDef.Namespace
		cpuLimit := appDef.Specs.CPU
		memLimit := appDef.Specs.Mem
	
		if deploymentPath == "" {
			fmt.Printf("[ERROR] Application Deployment spath is null: \n")
			os.Exit(1)
		}
	
		fmt.Printf("AppName: %s, agent IP: %s, gcmIP: %s, deploymentPath: %s \n", appName, agentIPs, gcmIP, deploymentPath)
	
	
		// Add a Pod watcher/listener here for pods added to the namespace
		fmt.Printf("[DBG] Adding a Pod watcher Thread for namespace: %s\n", namespace)
		podListWatcher := podWatcher.ListWatcher(namespace, clientset)
		queue := podWatcher.CreateQueue()
	
		controller := podWatcher.SetupWatcher(podListWatcher, queue, gcmIP, clientset)
		// Now let's start the controller on a seperate thread
		stop := make(chan struct{})
		defer close(stop)
		go controller.Run(1, stop)
		
		// Deploy the Application nominally - as it would be via `kubectl apply -f` and get the container names of all pods in the application
		fmt.Printf("[DBG] Deploying Application %s here  \n", appName)
		err := deployer(appName, gcmIP, deploymentPath, namespace, cpuLimit, memLimit, clientset)
		if err != nil {
			fmt.Printf("Error in parsing through deployment")
		}

	}

	
	select {}
}

func deployer(appName string, gcmIP string, deploymentPath string, namespace string, cpuLimit int, memLimit int, clientset *kubernetes.Clientset) (error) {
	fmt.Printf("Reading Directory: %s\n", deploymentPath)
	files, err := ioutil.ReadDir(deploymentPath)
	if err != nil {
		fmt.Printf("[ERROR] Can't read files from deployment path directory: %s\n", deploymentPath)
		fmt.Println(err)
		os.Exit(1)
	}


	/* Step 1. Tell GCM the application limits via GRPC request */
	sendAppSpecs(gcmIP, appName, cpuLimit, memLimit)	
	fmt.Println("Sent App specs to GCM. Press Enter to continue Deployment")
    fmt.Scanln() // wait for Enter Key

	/* Step 2. Get total number of Pods  */
	fmt.Printf("Reading Files to get number of pods:.. \n")
	totalPods:= int(getNumPods(deploymentPath, namespace, files, clientset))
	fmt.Printf("Total Number of Pods in Application:  %d\n", totalPods)

	/* Step 3. Go through each yaml file, set individual pod limits and deploy the deployment */
	decode := scheme.Codecs.UniversalDeserializer().Decode
	for _, item := range files {
		filePath := fmt.Sprintf("%v", deploymentPath) + item.Name()
		fmt.Printf("Reading File: %s\n", filePath)

		// Attempt 1
		yamlFile, err := ioutil.ReadFile(filePath)
		if err != nil {
			fmt.Printf("Error in reading file: %s, Error: %s\n", filePath, err)
		}
		// There can be multiple yaml definitions per file
		docs := strings.Split(string(yamlFile), "\n---")
		res := []byte{}
		// Trim whitespace in both ends of each yaml docs.
		for _, doc := range docs {
			content := strings.TrimSpace(doc)
			// Ignore empty docs
			if content != "" {
				res = append(res, content+"\n"...)
			}
			obj, groupVersionKind, err := decode(res, nil, nil)
			if err != nil {
				fmt.Println(fmt.Sprintf("Error while decoding YAML object. Err was: %s", err))
				continue
			}
			
			switch groupVersionKind.Kind {
			case "Deployment":
				fmt.Printf("Found Deployment! \n")
				deploymentsClient := clientset.AppsV1().Deployments(namespace)
				originalDeployment := obj.(*appsv1.Deployment)

				for i := 0; i < len(originalDeployment.Spec.Template.Spec.Containers); i++ {
					// fmt.Printf("Container Name: %s\n", originalDeployment.Spec.Template.Spec.Containers[i].Name)
					contCpu := strconv.Itoa(int(cpuLimit/totalPods))+ "m"
					contMem := strconv.Itoa(int(memLimit/totalPods))+ "Mi"
					fmt.Printf("Container limits: %s, %s \n", contCpu, contMem)
					// First argument is the "requests" and the 2nd argument is "limits"
					resReq := getResourceRequirements(getResourceList(contCpu, "0Mi"), getResourceList(contCpu, contMem))
					// resource: https://github.com/kubernetes/kubernetes/blob/master/pkg/controller/resourcequota/resource_quota_controller_test.go
					originalDeployment.Spec.Template.Spec.Containers[i].Resources = resReq
					
					//fmt.Printf("Container Limits: %v\n", originalDeployment.Spec.Template.Spec.Containers[i].Resources)
				}
				// example resource: https://github.com/kubernetes/client-go/blob/master/examples/create-update-delete-deployment/main.go
				// API: https://godoc.org/k8s.io/api/apps/v1
				result, err := deploymentsClient.Create(context.TODO(), originalDeployment, metav1.CreateOptions{})
				if err != nil {
					panic(err)
				}
				fmt.Printf("Created deployment %q.\n", result.GetObjectMeta().GetName())
			case "Service":
				fmt.Printf("Found Service! \n")
				servicesClientInterface := clientset.CoreV1().Services(namespace)
				originalService := obj.(*corev1.Service)
				result, err := servicesClientInterface.Create(context.TODO(), originalService, metav1.CreateOptions{})
				if err != nil {
					panic(err)
				}
				fmt.Printf("Created Service %q.\n", result.GetObjectMeta().GetName())
			default:
				fmt.Printf("Unsupported Type: %s \n", groupVersionKind.Kind)
				continue
			}
		}
		fmt.Printf("\n")
	}
	return nil
}

func sendAppSpecs(gcmIP string, appName string, cpuLimit int, memLimit int) {
	conn, err := grpc.Dial( gcmIP + GCM_GRPC_PORT, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	c := dgrpc.NewDeployerExportClient(conn)

	txMsg := &dgrpc.ExportAppSpec{
		AppName: appName,
		CpuLimit: uint64(cpuLimit),
		MemLimit: uint64(memLimit),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	r, err := c.ReportAppSpec(ctx, txMsg)
	if err != nil {
		log.Fatalf("could not ExportAppSpec: %v", err)
	}
	log.Println("Rx back from gcm: ", r.GetAppName(), r.GetCpu_Limit(), r.GetMemLimit(), r.GetThanks())

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

func getNumPods(deploymentPath string, namespace string, files []os.FileInfo, clientset *kubernetes.Clientset ) int32 {
	numOfPods := int32(0)
	decode := scheme.Codecs.UniversalDeserializer().Decode
	for _, item := range files {
		filePath := fmt.Sprintf("%v", deploymentPath) + item.Name()
		yamlFile, err := ioutil.ReadFile(filePath)
		if err != nil {
			fmt.Printf("Error in reading file: %s, Error: %s\n", filePath, err)
		}
		// There can be multiple yaml definitions per file
		docs := strings.Split(string(yamlFile), "\n---")
		res := []byte{}
		// Trim whitespace in both ends of each yaml docs.
		for _, doc := range docs {
			content := strings.TrimSpace(doc)
			// Ignore empty docs
			if content != "" {
				res = append(res, content+"\n"...)
			}
			obj, groupVersionKind, err := decode(res, nil, nil)
			if err != nil {
				fmt.Println(fmt.Sprintf("Error while decoding YAML object. Err was: %s", err))
				continue
			}
			switch groupVersionKind.Kind {
			case "Deployment":
				originalDeployment := obj.(*appsv1.Deployment)
				numOfPods = numOfPods + *originalDeployment.Spec.Replicas
			default:
				continue
			}
		}
	}
	return numOfPods
}

func getResourceList(cpu, memory string) corev1.ResourceList {
	res := corev1.ResourceList{}
	if cpu != "" {
		res[corev1.ResourceCPU] = resource.MustParse(cpu)
	}
	if memory != "" {
		res[corev1.ResourceMemory] = resource.MustParse(memory)
	}
	return res
}

func getResourceRequirements(requests, limits corev1.ResourceList) corev1.ResourceRequirements {
	res := corev1.ResourceRequirements{}
	res.Requests = requests
	res.Limits = limits
	return res
}