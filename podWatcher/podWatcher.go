/* Resources: 1. https://itnext.io/how-to-create-a-kubernetes-custom-controller-using-client-go-f36a7a7536cc
			  2. https://medium.com/@cloudark/kubernetes-custom-controllers-b6c7d0668fdf
			  3. https://github.com/kubernetes/sample-controller/blob/master/docs/controller-client-go.md
			  4. https://engineering.bitnami.com/articles/a-deep-dive-into-kubernetes-controllers.html
			  5. https://github.com/kubernetes/client-go/blob/master/examples/workqueue/main.go
*/
package podWatcher

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	// Controller
	"k8s.io/klog"

	// grpc stuff
	pb "github.com/Maziyar-Na/EC-Agent/grpc"
	dgrpc "github.com/gregcusack/ec_deployer/DeployServerGRPC"
	"google.golang.org/grpc"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	//"k8s.io/client-go/kubernetes/scheme"

)

const AGENT_GRPC_PORT = ":4446"
const GCM_GRPC_PORT = ":4447"
var BaseGcmGrpcPort = 4447 //app1 gets 4447, app2 gets 4448, ..., appN gets 4447 + appN - 1
const BUFFSIZE = 2048

//keeps track of the number of applications being deployed
//Only relevant for multitenancy
var nsToAppNumMap = make(map[string]int32)

type podNameToDockerIdMap struct {
	sync.RWMutex
	internal map[string]string
}

func (m *podNameToDockerIdMap) Read(key string) (string, bool) {
	m.RLock()
	result, ok := m.internal[key]
	m.RUnlock()
	return result, ok
}

func (m *podNameToDockerIdMap) Insert(key, value string) {
	m.Lock()
	m.internal[key] = value
	m.Unlock()
}

func (m *podNameToDockerIdMap) Delete(key string) {
	m.Lock()
	delete(m.internal, key)
	m.Unlock()
}

type Controller struct {
	indexer  cache.Indexer
	queue    workqueue.RateLimitingInterface
	informer cache.Controller
}

var m *podNameToDockerIdMap
//var nsMap *nsToAppNumMap

func NewController(queue workqueue.RateLimitingInterface, indexer cache.Indexer, informer cache.Controller) *Controller {
	return &Controller{
		informer: informer,
		indexer:  indexer,
		queue:    queue,
	}
}

func (c *Controller) processNextItem() bool {
	// Wait until there is a new item in the working queue
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two pods with the same key are never processed in
	// parallel.
	defer c.queue.Done(key)

	// Invoke the method containing the business logic
	err := c.syncToStdout(key.(string))
	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}
// syncToStdout is the business logic of the controller. In this controller it simply prints
// information about the pod to stdout. In case an error happened, it has to simply return the error.
// The retry logic should not be part of the business logic.
func (c *Controller) syncToStdout(key string) error {
	_, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		klog.Errorf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}

	if !exists {
		// Below we will warm up our cache with a Pod, so that we will see a delete for one pod
		fmt.Printf("Pod with key: %s does not exist anymore\n", key)

	} 
	// else {
	// 	// Note that you also have to check the uid if you have a local controlled resource, which
	// 	// is dependent on the actual instance, to detect that a Pod was recreated with the same name
	// 	fmt.Printf("Sync/Add/Update for Pod %s\n", obj.(*corev1.Pod).GetName())
	// }
	return nil
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *Controller) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.queue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.queue.NumRequeues(key) < 5 {
		klog.Infof("Error syncing pod %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.queue.AddRateLimited(key)
		return
	}

	c.queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	runtime.HandleError(err)
	klog.Infof("Dropping pod %q out of the queue: %v", key, err)
}

func (c *Controller) Run(threadiness int, stopCh chan struct{}, namespace string, appCount int32) {
	initPodToDockerMap()
	//initNamespacetoAppNumMap()
	nsToAppNumMap[namespace] = appCount

	defer runtime.HandleCrash()

	// Let the workers stop when we are done
	defer c.queue.ShutDown()
	klog.Info("Starting Pod controller")

	go c.informer.Run(stopCh)

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(stopCh, c.informer.HasSynced) {
		runtime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	klog.Info("Stopping Pod controller")
}

func (c *Controller) runWorker() {
	for c.processNextItem() {
	}
}

func initPodToDockerMap() {
	m = &podNameToDockerIdMap{internal: map[string]string{}}
}

//func initNamespacetoAppNumMap() {
//	nsMap = &nsToAppNumMap{internal: map[string]int{}}
//}

func ListWatcher(namespace string, clientset *kubernetes.Clientset) *cache.ListWatch {
	return cache.NewListWatchFromClient(clientset.CoreV1().RESTClient(), "pods", namespace, fields.Everything())
}

func CreateQueue() workqueue.RateLimitingInterface {
	return workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
}

func SetupWatcher(podListWatcher *cache.ListWatch, queue workqueue.RateLimitingInterface, gcmIP string, clientset *kubernetes.Clientset) *Controller {
	// Bind the workqueue to a cache with the help of an informer. This way we make sure that
	// whenever the cache is updated, the pod key is added sto the workqueue.
	// Note that when we finally process the item from the workqueue, we might see a newer version
	// of the Pod than the version which was responsible for triggering the update.
	var wg sync.WaitGroup

	indexer, informer := cache.NewIndexerInformer(podListWatcher, &corev1.Pod{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
			// This is where we'd want the GRPC call for calling sysconnect on new pods would be (I think.. because it would handle pods being created and spun)
			// Get Docker ID
			fmt.Println("Addding pod: " +  obj.(*corev1.Pod).GetName())
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			fmt.Println("###############")
			fmt.Println("updateFunc for old pod: " + old.(*corev1.Pod).GetName() + ", new pod: " + new.(*corev1.Pod).GetName())
			fmt.Println("updateFunc podStatus for old pod: " + string(old.(*corev1.Pod).Status.Phase) + ", new pod: " + string(new.(*corev1.Pod).Status.Phase))
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err == nil  {
				queue.Add(key)
			}
			ns, _, _ := cache.SplitMetaNamespaceKey(key)
			podNew := new.(*corev1.Pod)
			podOld := old.(*corev1.Pod)
			for i := range podNew.Status.ContainerStatuses {
				container := podNew.Status.ContainerStatuses[i]
				if container.State.Waiting != nil && container.State.Waiting.Reason != "" {
					fmt.Println("update pod. waiting status: " + container.State.Waiting.Reason)
					wg.Add(1)
					go handleNewPod(&wg, podNew, ns, gcmIP, clientset)
				}
			}
			wg.Wait()
			if podOld.DeletionTimestamp != nil || string(podNew.Status.Phase) == "Failed" {
				fmt.Println("Old Pod is terminating! name: " + podOld.GetName())
				if dockerId, ok := m.Read(podOld.GetName()); ok {
					go exportDeletePod(gcmIP, dockerId)
					fmt.Println("Deleting Docker id: " + dockerId)
					m.Delete(podOld.GetName())
				} else {
					fmt.Println("Failed to get dockerId from map! (" + podOld.GetName() + ")")
				}
			}
			fmt.Println("leaving updatefunc()")
		},
		DeleteFunc: func(obj interface{}) {
			// IndexerInformer uses a delta queue, therefore for deletes we have to use this
			// key function.
			fmt.Println("Delete Pod: " +  obj.(*corev1.Pod).GetName())
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
	}, cache.Indexers{}) 

	controller := NewController(queue, indexer, informer)

	return controller
}

func handleNewPod(wg *sync.WaitGroup, podObj *corev1.Pod, ns string, gcmIP string, clientset *kubernetes.Clientset) {
	fmt.Println("handleNewPod(). pod: " + podObj.GetName())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for {
		podObj, _ = clientset.CoreV1().Pods(ns).Get(context.TODO(), podObj.Name, metav1.GetOptions{})
		out := podObj.Status.Phase
		//fmt.Println(out)
		if string(out) == "Running"{
			fmt.Printf("Pod Status: %s with name: %s\n", string(out), podObj.GetName())
			nodeObj,_ := clientset.CoreV1().Nodes().Get(context.TODO(), podObj.Spec.NodeName, metav1.GetOptions{})
			fmt.Println("Pod deployed on: " + nodeObj.Status.Addresses[0].Address)
			//nodeIPNew := []corev1.NodeAddress{}
			//fmt.Println(nodeIPNew)
			//nodeIPNew = nodeObj.Status.Addresses
			nodeIP := nodeObj.Status.Addresses[0].Address
			fmt.Println("handleNewPod NodeIP: " + nodeIP)

			dockerId := GetDockerId(podObj)
			m.Insert(podObj.GetName(), dockerId)

			appNum, ok := nsToAppNumMap[ns] //get the appNum from the namespace
			if !ok {
				fmt.Println("Failed to get App Num from ns! ns: " + ns)
			}

			//TODO: this needs to be called by agent maybe. makes things much more complicated though
			//cgId, dockerID := connectContainerRequest(nodeIP, gcmIP, podObj.Name, dockerId, appNum)
			//if cgId != 0 {
			//	exportDeployPodSpec(nodeIP, gcmIP, dockerID, cgId, appNum)
			//}
			//exportDeployPodSpec(nodeIP, gcmIP, dockerId, cgId, appNum)
			break
		} else if ctx.Err() != nil {
			break
		}
	}
	wg.Done()
}

func GetDockerId(podObj *corev1.Pod) string {
	// This returns the first container manually
	//TODO: there is a bug here! will crash if docker id does not exist or something. so have to wait for it to exist
	dockID := podObj.Status.ContainerStatuses[0].ContainerID[9:]
	fmt.Println("dockID on deploy: " + dockID)
	return dockID
}

//TODO: json file should have port
func exportDeployPodSpec(nodeIP string, gcmIP string, dockerID string, cgroupId int32, appCount int32) {
	fmt.Println("Export pod Spec from cgID: " + strconv.Itoa(int(cgroupId)))
	var gcm_addr = gcmIP + ":" + strconv.Itoa(BaseGcmGrpcPort + (int(appCount) - 1))
	//conn, err := grpc.Dial( gcmIP + GCM_GRPC_PORT, grpc.WithInsecure(), grpc.WithBlock())
	conn, err := grpc.Dial( gcm_addr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := dgrpc.NewDeployerExportClient(conn)

	txMsg := &dgrpc.ExportPodSpec{
		DockerId: dockerID,
		CgroupId: cgroupId,
		NodeIp: nodeIP,
	}

	fmt.Println(txMsg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	r, err := c.ReportPodSpec(ctx, txMsg)
	if err != nil {
		log.Fatalf("could not ExportPodSpec: %v", err)
	}
	log.Println("Rx back from gcm: ", r.GetDockerId(), r.GetCgroupId(), r.GetNodeIp(), r.GetThanks())

}

func exportDeletePod(gcmIP string, dockerId string) {
	fmt.Println("export Delete Pod (dId): " + dockerId)
	conn, err := grpc.Dial( gcmIP + GCM_GRPC_PORT, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := dgrpc.NewDeployerExportClient(conn)
	txMsg := &dgrpc.ExportDeletePod{
		DockerId: dockerId,
	}

	fmt.Println(txMsg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	r, err := c.DeletePod(ctx, txMsg)
	if err != nil {
		log.Fatalf("could not ExportDeletePod: %v", err)
	}
	log.Println("Rx back from gcm: ", r.GetDockerId(), r.GetThanks())
}

func connectContainerRequest(agentIP, gcmIP, podName, dockerId string, appNum int32) (int32, string) {
	fmt.Println("connect container req")
	conn, err := grpc.Dial(agentIP + AGENT_GRPC_PORT, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewHandlerClient(conn)

	txMsg := &pb.ConnectContainerRequest{
		GcmIP: gcmIP,
		PodName: podName,
		DockerId: dockerId,
		AppNum: appNum,
	}
	fmt.Println("txMsg to agent -> gcmIP: " + txMsg.GcmIP + ", podname: " + txMsg.PodName + ", dockId: " + txMsg.DockerId)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r, err := c.ReqConnectContainer(ctx,txMsg)
	if err != nil {
		log.Fatalf("could not greet: %v", err)
	}
	log.Println("Rx back: ", r.GetPodName(), r.GetDockerID(), r.GetCgroupID())
	if r.GetCgroupID() == -1 {
		fmt.Println("ERROR IN SYSCONNECT. Rx back cgroupID: -1")
	}
	return r.GetCgroupID(), r.GetDockerID()

}

func SendNamespaceToAgent(gcmIP string, agentIPs []string, namespace string, appCount int32) []int32 {
	fmt.Println("sendNamespaceToAgent()")
	var returnStatuses []int32

	for _, agent_ip := range agentIPs {
		conn, err := grpc.Dial(agent_ip + AGENT_GRPC_PORT, grpc.WithInsecure(), grpc.WithBlock())
		c := pb.NewHandlerClient(conn)

		txMsg := &pb.TriggerPodDeploymentWatcherRequest{
			GcmIP:     	gcmIP,
			AgentIP:	agent_ip,
			Namespace: 	namespace,
			AppCount:  	appCount,
		}
		fmt.Println("txMsg to agent -> gcmIP: " + txMsg.GcmIP + ", namespace: " + txMsg.Namespace + ", appCount: " + strconv.Itoa(int(txMsg.AppCount)))

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		r, err := c.ReqTriggerAgentWatcher(ctx, txMsg)
		if err != nil {
			log.Fatalf("could not greet: %v", err)
		}
		log.Println("Rx back: ", r.GetReturnStatus())
		if r.GetReturnStatus() != 0 {
			fmt.Println("ERROR IN TriggerAgentWatcher for app: " + strconv.Itoa(int(appCount)) + ". Rx back returnStatus: " + string(r.GetReturnStatus()))
		}
		returnStatuses = append(returnStatuses, r.GetReturnStatus())
	}
	return returnStatuses


}
