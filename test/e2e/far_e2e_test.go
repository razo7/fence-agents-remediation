package e2e

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
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

	// eventually parameters
	timeout      = 2 * time.Minute
	pollInterval = 10 * time.Second
	offsetExpect = 1
)

var _ = Describe("FAR E2e", func() {
	testShareParam := map[v1alpha1.ParameterName]string{
		"--username": "admin",
		"--password": "password",
		"--action":   "reboot",
		"--ip":       "192.168.111.1",
		"--lanplus":  "",
	}
	testNodeParam := map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{
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
			far                *v1alpha1.FenceAgentsRemediation
			nodeBootTimeBefore time.Time
			errBoot            error
			testNodeName       string
		)
		nodes := &corev1.NodeList{}
		BeforeEach(func() {
			Expect(k8sClient.List(context.Background(), nodes, &client.ListOptions{})).ToNot(HaveOccurred())
			if len(nodes.Items) <= 1 {
				Fail("there is one or less available nodes in the cluster")
			}
			//TODO: Randomize the node selection
			// Use FA on the first node - master-0
			nodeObj := &nodes.Items[0]
			testNodeName := nodeObj.Name
			log.Info("Testing Node", "Node name", testNodeName)

			// save the node's boot time prior to the fence agent call
			nodeBootTimeBefore, errBoot = getNodeBootTime(testNodeName)
			if errBoot != nil {
				Fail("Can't get boot time of the node")
			}

			// // save the last time Ready condition of node's Kubelet has been changed
			// cond, errBoot := getKubeletReadyCondition(testNodeName)
			// kubeletReadyTimeBefore = cond.LastTransitionTime
			// if errBoot != nil || kubeletReadyTimeBefore.IsZero() {
			// 	Fail("Can't get the Ready condition of node's Kubelet or its time is zero, thus we can't verify if it has been changed, and the node has been rebooted")
			// }

			far = createFAR(testNodeName, fenceAgentIPMI, testShareParam, testNodeParam)
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
				wasNodeRebootedBoot(testNodeName, nodeBootTimeBefore)
				//wasNodeRebooted(testNodeName, kubeletReadyTimeBefore)
			})
		})
	})
})

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
		log.Info("got boot time", "time", *bootTime, "node", nodeName)
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
