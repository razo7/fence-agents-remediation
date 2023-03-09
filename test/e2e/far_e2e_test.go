package e2e

import (
	"context"
	"net/http"
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
	// testNamespace  = "fence-agents-remediation"
	testNamespace  = "openshift-operators"
	fenceAgentIPMI = "fence_ipmilan"
	hostNameLabel  = "kubernetes.io/hostname"

	// eventually parameters
	timeout      = 2 * time.Minute
	pollInterval = 10 * time.Second
	offsetExpect = 1
)

var nodeBootTime time.Time

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
		var far *v1alpha1.FenceAgentsRemediation
		var errBoot error
		testNode := &corev1.Node{}
		nodes := &corev1.NodeList{}
		BeforeEach(func() {
			// Use FA on the first node - master-0
			Expect(k8sClient.List(context.Background(), nodes, &client.ListOptions{})).ToNot(HaveOccurred())
			if len(nodes.Items) <= 1 {
				Skip("there is one or less available nodes in the cluster")
			}
			testNode = &nodes.Items[0]
			log.Info("Testing Node", "Node name", testNode.Name)

			// save the node's boot time prior to the fence agent call
			if nodeBootTime, errBoot = getNodeBootTime(testNode.Name); errBoot != nil {
				log.Error(errBoot, "Can't get boot time of the node")
			}
			far = createFAR(testNode.Name, fenceAgentIPMI, testShareParam, testNodeParam)
		})

		AfterEach(func() {
			deleteFAR(far)
		})

		When("running FAR to reboot node ", func() {
			It("should execute the fence agent cli command", func() {
				By("checking the CR has been created")
				farCR := &v1alpha1.FenceAgentsRemediation{}
				ExpectWithOffset(offsetExpect, k8sClient.Get(context.Background(),
					client.ObjectKey{Name: testNode.Name, Namespace: testNamespace}, farCR)).ToNot(HaveOccurred())

				By("checking the command has been executed successfully")
				checkFarLogs(cli.SuccessCommandLog)

				By("checking the node's boot time after running the FA")
				emptyTime := time.Time{}
				if nodeBootTime != emptyTime {
					Eventually(func() (time.Time, error) {
						nodeBootTimeAfter, errBootAfter := getNodeBootTime(testNode.Name)
						if errBoot != nil {
							log.Error(errBootAfter, "Can't get boot time of the node")
						}
						return nodeBootTimeAfter, errBootAfter
					}, timeout, pollInterval).Should(
						BeTemporally(">", nodeBootTime),
					)
				} else {
					Skip("we couldn't get the boot time of the node prior to FAR CR, thus we won't try to fetch and compare it now")
				}
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
		log.Info("got boot time", "time", *bootTime)
		return *bootTime, nil
	}
	return time.Time{}, err
}

// checkFarLogs get the FAR pod and check whether its logs has logString
func checkFarLogs(logString string) {
	By("checking logs")
	var pod *corev1.Pod
	var err error
	EventuallyWithOffset(offsetExpect, func() (*corev1.Pod, error) {
		pod, err = getFenceAgentsPod(testNamespace)
		return pod, err
	}, 2*timeout, pollInterval).ShouldNot(BeNil(), "can't find the pod after timeout")

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
func getFenceAgentsPod(namespace string) (*corev1.Pod, error) {
	pods := new(corev1.PodList)
	podLabelsSelector, _ := metav1.LabelSelectorAsSelector(
		&metav1.LabelSelector{MatchLabels: farController.FaPodLabels})
	options := client.ListOptions{
		LabelSelector: podLabelsSelector,
		Namespace:     namespace,
	}
	if err := k8sClient.List(context.Background(), pods, &options); err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		podNotFoundErr := &apiErrors.StatusError{ErrStatus: metav1.Status{
			Status: metav1.StatusFailure,
			Code:   http.StatusNotFound,
			Reason: metav1.StatusReasonNotFound,
		}}
		return nil, podNotFoundErr
	}
	return &pods.Items[0], nil
}
