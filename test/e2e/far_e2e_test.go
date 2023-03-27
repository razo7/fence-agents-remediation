package e2e

import (
	"context"
	"errors"
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
	fenceAgentDummyName = "echo"
	testNamespace       = "openshift-operators"
	fenceAgentIPMI      = "fence_ipmilan"

	// eventually parameters
	timeout      = 2 * time.Minute
	pollInterval = 10 * time.Second
	offsetExpect = 1
)

var _ = Describe("FAR E2e", func() {
	var far *v1alpha1.FenceAgentsRemediation

	Context("fence agent - dummy", func() {
		testNodeName := "dummy-node"

		BeforeEach(func() {
			testShareParam := map[v1alpha1.ParameterName]string{}
			testNodeParam := map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{}
			far = createFAR(testNodeName, fenceAgentDummyName, testShareParam, testNodeParam)
		})

		AfterEach(func() {
			deleteFAR(far)
		})

		It("should check whether the CR has been created", func() {
			testFarCR := &v1alpha1.FenceAgentsRemediation{}
			Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(far), testFarCR)).To(Succeed(), "failed to get FAR CR")
		})
	})

	Context("fence agent - fence_ipmilan", func() {
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
		}

		var (
			testNodeName string
		)
		nodes := &corev1.NodeList{}
		BeforeEach(func() {
			Expect(k8sClient.List(context.Background(), nodes, &client.ListOptions{})).ToNot(HaveOccurred())
			if len(nodes.Items) <= 1 {
				Fail("there is one or less available nodes in the cluster")
			}
			//TODO: Randomize the node selection
			// run FA on the first node - a master node
			nodeObj := nodes.Items[0]
			testNodeName = nodeObj.Name
			log.Info("Testing Node", "Node name", testNodeName)

			far = createFAR(testNodeName, fenceAgentIPMI, testShareParam, testNodeParam)
		})

		AfterEach(func() {
			deleteFAR(far)
		})

		When("running FAR to reboot node ", func() {
			It("should execute the fence agent cli command", func() {
				By("checking the CR has been created")
				testFarCR := &v1alpha1.FenceAgentsRemediation{}
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(far), testFarCR)).To(Succeed(), "failed to get FAR CR")

				By("checking the command has been executed successfully")
				checkFarLogs(cli.SuccessCommandLog)

			})
		})
	})
})

// createFAR assigns the input to FenceAgentsRemediation object, creates CR, and returns the CR object
func createFAR(nodeName string, agent string, sharedParameters map[v1alpha1.ParameterName]string, nodeParameters map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string) *v1alpha1.FenceAgentsRemediation {
	far := &v1alpha1.FenceAgentsRemediation{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: testNamespace},
		Spec: v1alpha1.FenceAgentsRemediationSpec{
			Agent:            agent,
			SharedParameters: sharedParameters,
			NodeParameters:   nodeParameters,
		},
	}
	ExpectWithOffset(1, k8sClient.Create(context.Background(), far)).ToNot(HaveOccurred())
	return far
}

// deleteFAR deletes the CR with offset
func deleteFAR(far *v1alpha1.FenceAgentsRemediation) {
	EventuallyWithOffset(1, func() error {
		err := k8sClient.Delete(context.Background(), far)
		if apiErrors.IsNotFound(err) {
			return nil
		}
		return err
	}, 2*time.Minute, 10*time.Second).ShouldNot(HaveOccurred(), "failed to delete far")
}

// checkFarLogs gets the FAR pod and checks whether it's logs have logString
func checkFarLogs(logString string) {
	var pod *corev1.Pod
	EventuallyWithOffset(1, func() *corev1.Pod {
		pod = getFenceAgentsPod(testNamespace)
		return pod
	}, timeout, pollInterval).ShouldNot(BeNil(), "can't find the pod after timeout")

	EventuallyWithOffset(1, func() string {
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
		log.Error(errors.New("API error"), "Zero pods")
		return nil
	}
	return &pods.Items[0]
}
