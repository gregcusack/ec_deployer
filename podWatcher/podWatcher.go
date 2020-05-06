/* Resources: 1. https://itnext.io/how-to-create-a-kubernetes-custom-controller-using-client-go-f36a7a7536cc
			  2. https://medium.com/@cloudark/kubernetes-custom-controllers-b6c7d0668fdf
			  3. https://github.com/kubernetes/sample-controller/blob/master/docs/controller-client-go.md
			  4. https://engineering.bitnami.com/articles/a-deep-dive-into-kubernetes-controllers.html
			  5. https://github.com/kubernetes/client-go/blob/master/examples/workqueue/main.go
*/
package podWatcher

import (
	"fmt"
	"time"
	"context"
	"log"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"


	// Controller
	"k8s.io/klog"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	// grpc stuff
	pb "github.com/Maziyar-Na/EC-Agent/grpc"
	dgrpc "github.com/gregcusack/ec_deployer/DeployServerGRPC"
	"google.golang.org/grpc"
	
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	//"k8s.io/client-go/kubernetes/scheme"

)

const AGENT_GRPC_PORT = ":4446"
const GCM_GRPC_PORT = ":4447"
const BUFFSIZE = 2048

type Controller struct {
	indexer  cache.Indexer
	queue    workqueue.RateLimitingInterface
	informer cache.Controller
}

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

func (c *Controller) Run(threadiness int, stopCh chan struct{}) {
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
	indexer, informer := cache.NewIndexerInformer(podListWatcher, &corev1.Pod{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
			// This is where we'd want the GRPC call for calling sysconnect on new pods would be (I think.. because it would handle pods being created and spun)
			// Get Docker ID
			fmt.Printf("Add for Pod %s: +\n", obj.(*corev1.Pod).GetName())
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err == nil {
				queue.Add(key)
			}
			fmt.Printf("Update for Pod %s\n", new.(*corev1.Pod).GetName())
			go handleNewPod(new.(*corev1.Pod), gcmIP, clientset)
		},
		DeleteFunc: func(obj interface{}) {
			// IndexerInformer uses a delta queue, therefore for deletes we have to use this
			// key function.
			fmt.Printf("Delete for Pod %s\n", obj.(*corev1.Pod).GetName())
			// This is where we'd want the GRPC call for calling disconnecting container from the GCM on deleted pods would be (i.e. if and when a pod gets killed, we can identify it here)
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
	}, cache.Indexers{}) 

	controller := NewController(queue, indexer, informer)

	return controller
}

func handleNewPod(podObj *corev1.Pod, gcmIP string, clientset *kubernetes.Clientset) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		out := podObj.Status.Phase
		//fmt.Println(out)
		if string(out) == "Running"{
			fmt.Printf("Pod Status: %s with name: %s\n", string(out), podObj.GetName())
			nodeObj,_ := clientset.CoreV1().Nodes().Get(context.TODO(), podObj.Spec.NodeName, metav1.GetOptions{})
			nodeIP := nodeObj.Status.Addresses[0].Address

			dockerId := GetDockerId(podObj)
			cgId, dockerID := connectContainerRequest(nodeIP, gcmIP, podObj.Name, dockerId)
			exportDeployPodSpec(nodeIP, gcmIP, dockerID, cgId)			
			break
		} else if ctx.Err() != nil {
			break
		}
	}
}

func GetDockerId(podObj *corev1.Pod) string {
	return podObj.Status.ContainerStatuses[0].ContainerID[9:]
}

//TODO: json file should have port
func exportDeployPodSpec(nodeIP string, gcmIP string, dockerID string, cgroupId int32) {
	conn, err := grpc.Dial( gcmIP + GCM_GRPC_PORT, grpc.WithInsecure(), grpc.WithBlock())
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

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	r, err := c.ReportPodSpec(ctx, txMsg)
	if err != nil {
		log.Fatalf("could not ExportPodSpec: %v", err)
	}
	log.Println("Rx back from gcm: ", r.GetDockerId(), r.GetCgroupId(), r.GetNodeIp(), r.GetThanks())

}

func connectContainerRequest(agentIP, gcmIP, podName, dockerId string) (int32, string) {
	//todo: getpodfromname() and getDockerId() from agent into here (aka deployer) send over dockerid to agent for connectcontainer
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