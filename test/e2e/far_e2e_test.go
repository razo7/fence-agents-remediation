package e2e

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	farController "github.com/medik8s/fence-agents-remediation/controllers"
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
	farUtils "github.com/medik8s/fence-agents-remediation/test/e2e/utils"
)

const (
	testNamespace  = "fence-agents-remediation"
	fenceAgentIPMI = "fence_ipmilan"

	nodeExecTimeout = 20 * time.Second
	// eventually parameters
	timeout      = 2 * time.Minute
	pollInterval = 10 * time.Second
	offsetExpect = 1
)

var (
	testShareParamHC map[v1alpha1.ParameterName]string
	testNodeParamHC  map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string
	testNodeParam    map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string
)

var _ = Describe("FAR E2e", func() {
	testShareParamHC = map[v1alpha1.ParameterName]string{
		"--username": "admin",
		"--password": "password",
		"--action":   "reboot",
		"--ip":       "192.168.111.1",
		"--lanplus":  "",
	}
	testNodeParamHC = map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{
		"--ipport": {
			"master-0": "6230",
			"master-1": "6231",
			"master-2": "6232",
			"worker-0": "6233",
			"worker-1": "6234",
			"worker-2": "6235",
		},
	} // get ports
	Context("fence agent - fence_ipmilan", func() {
		var (
			// kubeletReadyTimeBefore metav1.Time
			far *v1alpha1.FenceAgentsRemediation
			// nodeBootTimeBefore    time.Time
			errBoot               error
			testNodeName          string
			nodeObj               v1.Node
			nodeBootTimeBeforeRef *time.Time
		)
		nodes := &corev1.NodeList{}
		BeforeEach(func() {
			Expect(k8sClient.List(context.Background(), nodes, &client.ListOptions{})).ToNot(HaveOccurred())
			if len(nodes.Items) <= 1 {
				Fail("there is one or less available nodes in the cluster")
			}
			//TODO: Randomize the node selection
			// Use FA on the first node - master-0
			nodeObj = nodes.Items[2]
			testNodeName = nodeObj.Name
			log.Info("Testing Node", "Node name", testNodeName)

			// save the node's boot time prior to the fence agent call

			nodeBootTimeBeforeRef, errBoot = getBootTime(&nodeObj)

			// nodeBootTimeBefore, errBoot = getNodeBootTime(testNodeName)
			if errBoot != nil {
				Fail("Can't get boot time of the node")
			}

			// // save the last time Ready condition of node's Kubelet has been changed
			// cond, errBoot := getKubeletReadyCondition(testNodeName)
			// kubeletReadyTimeBefore = cond.LastTransitionTime
			// if errBoot != nil || kubeletReadyTimeBefore.IsZero() {
			// 	Fail("Can't get the Ready condition of node's Kubelet or its time is zero, thus we can't verify if it has been changed, and the node has been rebooted")
			// }
			// commandNames := "vbmc list | tail -n +4 | head -n -1 | awk '{print $2}' | paste -s -d ' '"
			// commandPorts := "vbmc list | tail -n +4 | head -n -1 | awk '{print $8}' | paste -s -d ' '"

			commandNamesArray := []string{"vbmc", "list", "|", "tail", "-n", "+4", "|", "head", "-n", "-1", "|", "awk", "'{print $2}'", "|", "paste", "-s", "-d", "'", "'"}
			commandInstallPython := []string{"sudo", "dnf", "install", "python39-devel", "-y"}
			commandInstallVBMC := []string{"sudo", "pip3", "install", "virtualbmc", "-y"}

			commandPortsArray := []string{"vbmc", "list", "|", "tail", "-n", "+4", "|", "head", "-n", "-1", "|", "awk", "'{print $8}'", "|", "paste", "-s", "-d", "'", "'"}
			commandPortsArray = append(commandPortsArray, commandInstallVBMC...)
			commandPortsArray = append(commandPortsArray, commandInstallPython...)

			ctx, _ := context.WithTimeout(context.Background(), nodeExecTimeout)
			fmt.Printf("Nodes info: names %s, and ports %s",
				getClusterNodeInfo(testNodeName, commandNamesArray, &nodeObj, ctx),
				getClusterNodeInfo(testNodeName, commandPortsArray, &nodeObj, ctx),
			)
			// testNodeParam = buildNodeParameters(getClusterNodeNames(testNodeName), getClusterNodePorts(testNodeName))
			far = createFAR(testNodeName, fenceAgentIPMI, testShareParamHC, testNodeParamHC)
		})

		AfterEach(func() {
			deleteFAR(far)
		})

		When("running FAR to reboot node ", func() {
			It("should execute the fence agent cli command", func() {
				By("checking the CR has been created")
				testFarCR := &v1alpha1.FenceAgentsRemediation{}
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(far), testFarCR)).ToNot(HaveOccurred())

				By("checking the command has been executed successfully")
				checkFarLogs(cli.SuccessCommandLog)

				By("checking the node's boot time after running the FA")
				checkReboot(&nodeObj, nodeBootTimeBeforeRef)
				// wasNodeRebootedBoot(testNodeName, nodeBootTimeBefore)
				//wasNodeRebooted(testNodeName, kubeletReadyTimeBefore)
			})
		})
	})
})

func buildNodeParameters(nodeNames []string, nodePorts []string) map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string {
	var nodeNamePorts map[v1alpha1.NodeName]string

	for i, name := range nodeNames {
		keyName := v1alpha1.NodeName(name)
		nodeNamePorts[keyName] = nodePorts[i]
	}
	testNodeParam := map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{v1alpha1.ParameterName("--ipport"): nodeNamePorts}
	return testNodeParam
}

// createFAR assign the input to FenceAgentsRemediation object, create CR it with offset, and return the CR object
func createFAR(nodeName string, agent string, sharedparameters map[v1alpha1.ParameterName]string, nodeparameters map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string) *v1alpha1.FenceAgentsRemediation {
	far := &v1alpha1.FenceAgentsRemediation{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: testNamespace},
		Spec: v1alpha1.FenceAgentsRemediationSpec{
			Agent:            agent,
			SharedParameters: sharedparameters,
			NodeParameters:   nodeparameters,
		},
	}
	ExpectWithOffset(offsetExpect, k8sClient.Create(context.Background(), far)).ToNot(HaveOccurred())
	return far
}

// deleteFAR delete the CR with offset
func deleteFAR(far *v1alpha1.FenceAgentsRemediation) {
	EventuallyWithOffset(offsetExpect, func() error {
		err := k8sClient.Delete(context.Background(), far)
		if apiErrors.IsNotFound(err) {
			return nil
		}
		return err
	}, timeout, pollInterval).ShouldNot(HaveOccurred(), "failed to delete far")
}

// getNodeBootTime return the bootime of node nodeName if it possible, otherwise return an error
func getNodeBootTime(nodeName string) (time.Time, error) {
	bootTime, err := farUtils.GetBootTime(clientSet, nodeName, testNamespace, log)
	if bootTime != nil && err == nil {
		log.Info("got boot time", "node", nodeName, "time", *bootTime)
		return *bootTime, nil
	}
	return time.Time{}, err
}

func wasNodeRebootedBoot(nodeName string, nodeBootTimeBefore time.Time) {
	Eventually(func() (time.Time, error) {
		nodeBootTimeAfter, errBootAfter := getNodeBootTime(nodeName)
		if errBootAfter != nil {
			log.Error(errBootAfter, "Can't get boot time of the node")
		}
		return nodeBootTimeAfter, errBootAfter
	}, timeout, pollInterval).Should(
		BeTemporally(">", nodeBootTimeBefore),
	)
}

// getKubeletReadyCondition return the Ready condition of the node's kubelet, otherwise return an error
func getKubeletReadyCondition(nodeName string) (corev1.NodeCondition, error) {
	emptyCondition := corev1.NodeCondition{}
	node, err := clientSet.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		return emptyCondition, err
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition, nil
		}
	}
	return emptyCondition, fmt.Errorf("Node %s is not ready", nodeName)
}

// checkFarLogs get the FAR pod and check whether its logs has logString
func checkFarLogs(logString string) {
	var pod *corev1.Pod
	EventuallyWithOffset(offsetExpect, func() *corev1.Pod {
		pod = getFenceAgentsPod(testNamespace)
		return pod
	}, timeout, pollInterval).ShouldNot(BeNil(), "can't find the pod after timeout")

	EventuallyWithOffset(offsetExpect, func() string {
		logs, err := farUtils.GetLogs(clientSet, pod, "manager")
		if err != nil {
			log.Error(err, "failed to get logs. Might try again")
			return ""
		}
		return logs
	}, timeout/2, pollInterval).Should(ContainSubstring(logString))
}

// getFenceAgentsPod fetches the FAR pod based on FAR's label and namespace
func getFenceAgentsPod(namespace string) *corev1.Pod {
	pods := new(corev1.PodList)
	podLabelsSelector, _ := metav1.LabelSelectorAsSelector(
		&metav1.LabelSelector{MatchLabels: farController.FaPodLabels})
	options := client.ListOptions{
		LabelSelector: podLabelsSelector,
		Namespace:     namespace,
	}
	if err := k8sClient.List(context.Background(), pods, &options); err != nil {
		log.Error(err, "can't find the pod by it's labels")
		return nil
	}
	if len(pods.Items) == 0 {
		log.Error(errors.New("API error"), "Zero containers for the pod")
		return nil
	}
	return &pods.Items[0]
}

// wasNodeRebooted wait until the node's Kubelet condition status is ready (again) and it is after the time of lastReadyTime
func wasNodeRebooted(nodeName string, lastReadyTime metav1.Time) {
	EventuallyWithOffset(offsetExpect, func() bool {
		cond, err := getKubeletReadyCondition(nodeName)
		if err != nil {
			log.Error(err, "Can't get boot time of the node")
		}
		return cond.Status == corev1.ConditionTrue && cond.LastTransitionTime.After(lastReadyTime.Time)
	}, 2*timeout, pollInterval).Should(BeTrue())
}

func getBootTime(node *v1.Node) (*time.Time, error) {
	bootTimeCommand := []string{"uptime", "-s"}
	var bootTime time.Time
	Eventually(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), nodeExecTimeout)
		defer cancel()
		bootTimeString, err := farUtils.ExecCommandOnNode(k8sClient, bootTimeCommand, node, ctx)
		if err != nil {
			return err
		}
		bootTime, err = time.Parse("2006-01-02 15:04:05", bootTimeString)
		if err != nil {
			return err
		}
		return nil
	}, 6*time.Minute, 10*time.Second).ShouldNot(HaveOccurred())
	return &bootTime, nil
}

func checkReboot(node *v1.Node, oldBootTime *time.Time) {
	By("checking reboot")
	log.Info("boot time", "old", oldBootTime)
	// Note: short timeout only because this check runs after node re-create check,
	// where already multiple minute were spent
	EventuallyWithOffset(1, func() time.Time {
		newBootTime, err := getBootTime(node)
		if err != nil {
			return time.Time{}
		}
		log.Info("boot time", "new", newBootTime)
		return *newBootTime
	}, 7*time.Minute, 10*time.Second).Should(BeTemporally(">", *oldBootTime))
}

func getClusterNodeInfo(nodeName string, command []string, node *v1.Node, ctx context.Context) []string {
	output, _ := farUtils.ExecCommandOnNode(k8sClient, command, node, ctx)
	// output, _ := farUtils.RunCommandInCluster(clientSet, nodeName, testNamespace, command , log)
	// if err != nil {
	// 	return "", err
	// }
	res := strings.Split(output, " ")
	log.Info("Cluster", "Node Info", res)
	return res
}
