package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	farUtils "github.com/medik8s/fence-agents-remediation/pkg/utils"
	farE2eUtils "github.com/medik8s/fence-agents-remediation/test/e2e/utils"
)

const (
	fenceAgentDummyName      = "echo"
	fenceAgentAWS            = "fence_aws"
	fenceAgentIPMI           = "fence_ipmilan"
	fenceAgentAction         = "reboot"
	nodeIdentifierPrefixAWS  = "--plug"
	nodeIdentifierPrefixIPMI = "--ipport"
	nodeIndex                = 3
	succeesStatusMessage     = "\"Status: ON"
	succeesRebootMessage     = "\"Success: Rebooted"
	containerName            = "manager"

	// eventually parameters
	timeoutLogs   = 1 * time.Minute
	timeoutReboot = 3 * time.Minute
	pollInterval  = 10 * time.Second
)

var _ = Describe("FAR E2e", func() {
	var (
		far             *v1alpha1.FenceAgentsRemediation
		fenceAgent      string
		clusterPlatform *configv1.Infrastructure
		err             error
	)
	BeforeEach(func() {
		clusterPlatform, err = farE2eUtils.GetClusterInfo(configClient)
		if err != nil {
			Fail("can't identify the cluster platform")
		}
		fmt.Printf("\ncluster name: %s and PlatformType: %s \n", string(clusterPlatform.Name), string(clusterPlatform.Status.PlatformStatus.Type))
	})

	Context("fence agent - dummy", func() {
		testNodeName := "dummy-node"
		fenceAgent = fenceAgentDummyName

		BeforeEach(func() {
			testShareParam := map[v1alpha1.ParameterName]string{}
			testNodeParam := map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{}
			far = createFAR(testNodeName, fenceAgent, testShareParam, testNodeParam)
		})

		AfterEach(func() {
			deleteFAR(far)
		})

		It("should check whether the CR has been created", func() {
			testFarCR := &v1alpha1.FenceAgentsRemediation{}
			Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(far), testFarCR)).To(Succeed(), "failed to get FAR CR")
		})
	})

	Context("fence agent - fence_aws or fence_ipmilan", func() {
		var (
			nodeBootTimeBefore   time.Time
			errBoot              error
			testNodeName         string
			nodeIdentifierPrefix string
			testNodeID           string
		)
		BeforeEach(func() {
			nodes := &corev1.NodeList{}
			Expect(k8sClient.List(context.Background(), nodes, &client.ListOptions{})).ToNot(HaveOccurred())
			if len(nodes.Items) <= 1 {
				Fail("there is one or less available nodes in the cluster")
			}
			//TODO: Randomize the node selection
			// run FA on the first node - a master node
			nodeObj := nodes.Items[nodeIndex]
			testNodeName = nodeObj.Name

			switch clusterPlatform.Status.PlatformStatus.Type {
			case configv1.AWSPlatformType:
				fenceAgent = fenceAgentAWS
				nodeIdentifierPrefix = nodeIdentifierPrefixAWS
				By("running fence_aws")
			case configv1.BareMetalPlatformType:
				fenceAgent = fenceAgentIPMI
				nodeIdentifierPrefix = nodeIdentifierPrefixIPMI
				By("running fence_ipmilan")
			default:
				Skip("FAR haven't been tested on this kind of cluster (non AWS or BareMetal)")
			}

			testShareParam, err := buildSharedParameters(clusterPlatform, fenceAgentAction)
			if err != nil {
				Fail("can't get shared information")
			}
			testNodeParam, err := buildNodeParameters(clusterPlatform.Status.PlatformStatus.Type)
			if err != nil {
				Fail("can't get node information")
			}
			nodeName := v1alpha1.NodeName(testNodeName)
			parameterName := v1alpha1.ParameterName(nodeIdentifierPrefix)
			testNodeID = testNodeParam[parameterName][nodeName]
			log.Info("Testing Node", "Node name", testNodeName, "Node ID", testNodeID)

			// save the node's boot time prior to the fence agent call
			nodeBootTimeBefore, errBoot = getNodeBootTime(testNodeName)
			Expect(errBoot).ToNot(HaveOccurred(), "failed to get boot time of the node")

			far = createFAR(testNodeName, fenceAgent, testShareParam, testNodeParam)
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
				expectedLog := buildExpectedLogOutput(clusterPlatform.Status.PlatformStatus.Type, nodeIdentifierPrefix, testNodeID)
				checkFarLogs(expectedLog)

				By("checking the node's boot time after running the FA")
				wasNodeRebooted(testNodeName, nodeBootTimeBefore)
			})
		})
	})
})

// createFAR assigns the input to FenceAgentsRemediation object, creates CR, and returns the CR object
func createFAR(nodeName string, agent string, sharedParameters map[v1alpha1.ParameterName]string, nodeParameters map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string) *v1alpha1.FenceAgentsRemediation {
	far := &v1alpha1.FenceAgentsRemediation{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: operatorNsName},
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

// buildSharedParameters returns a map key-value of shared parameters based on cluster platform type if it finds the credentials, otherwise an error
func buildSharedParameters(clusterPlatform *configv1.Infrastructure, action string) (map[v1alpha1.ParameterName]string, error) {
	const (
		//AWS
		secretAWS    = "aws-cloud-credentials"
		secretKeyAWS = "aws_access_key_id"
		secretValAWS = "aws_secret_access_key"

		// BareMetal
		//TODO: secret BM should be based on node name - > oc get bmh -n openshift-machine-api BM_NAME -o jsonpath='{.spec.bmc.credentialsName}'
		secretBMHExample = "ostest-master-0-bmc-secret"
		secretKeyBM      = "username"
		secretValBM      = "password"
	)
	var testShareParam map[v1alpha1.ParameterName]string

	// oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.status.platformStatus.type}'
	clusterPlatformType := clusterPlatform.Status.PlatformStatus.Type
	if clusterPlatformType == configv1.AWSPlatformType {
		accessKey, secretKey, err := farE2eUtils.GetCredentials(clientSet, secretAWS, secretKeyAWS, secretValAWS)
		if err != nil {
			fmt.Printf("can't get AWS credentials\n")
			return nil, err
		}

		// oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.status.platformStatus.aws.region}'
		regionAWS := string(clusterPlatform.Status.PlatformStatus.AWS.Region)

		testShareParam = map[v1alpha1.ParameterName]string{
			"--access-key": accessKey,
			"--secret-key": secretKey,
			"--region":     regionAWS,
			"--action":     action,
			// "--verbose":    "", // for verbose result
		}
	} else if clusterPlatformType == configv1.BareMetalPlatformType {
		// TODO : get ip from GetCredientals
		// oc get bmh -n openshift-machine-api ostest-master-0 -o jsonpath='{.spec.bmc.address}'
		// then parse ip
		username, password, err := farE2eUtils.GetCredentials(clientSet, secretBMHExample, secretKeyBM, secretValBM)
		if err != nil {
			fmt.Printf("can't get BMH credentials\n")
			return nil, err
		}
		testShareParam = map[v1alpha1.ParameterName]string{
			"--username": username,
			"--password": password,
			"--ip":       "192.168.111.1",
			"--action":   action,
			"--lanplus":  "",
		}
	}
	return testShareParam, nil
}

// buildNodeParameters returns a map key-value of node parameters based on cluster platform type if it finds the node info list, otherwise an error
func buildNodeParameters(clusterPlatformType configv1.PlatformType) (map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string, error) {
	var (
		testNodeParam  map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string
		nodeListParam  map[v1alpha1.NodeName]string
		nodeIdentifier v1alpha1.ParameterName
		err            error
	)

	if clusterPlatformType == configv1.AWSPlatformType {
		nodeListParam, err = farE2eUtils.GetAWSNodeInfoList(machineClient)
		if err != nil {
			fmt.Printf("can't get nodes' information - AWS instance ID\n")
			return nil, err
		}
		nodeIdentifier = v1alpha1.ParameterName(nodeIdentifierPrefixAWS)

	} else if clusterPlatformType == configv1.BareMetalPlatformType {
		nodeListParam, err = farE2eUtils.GetBMHNodeInfoList(machineClient)
		if err != nil {
			fmt.Printf("can't get nodes' information - ports\n")
			return nil, err
		}
		nodeIdentifier = v1alpha1.ParameterName(nodeIdentifierPrefixIPMI)
	}
	testNodeParam = map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{nodeIdentifier: nodeListParam}
	return testNodeParam, nil
}

// getNodeBootTime returns the bootime of node nodeName if possible, otherwise it returns an error
func getNodeBootTime(nodeName string) (time.Time, error) {
	bootTime, err := farE2eUtils.GetBootTime(clientSet, nodeName, testNsName, log)
	if bootTime != nil && err == nil {
		return *bootTime, nil
	}
	return time.Time{}, err
}

// buildExpectedLogOutput
func buildExpectedLogOutput(clusterPlatformType configv1.PlatformType, nodeIdentifierPrefix, testNodeID string) string {
	// Example -> "--plug=i-04172bb40a6a83804"], "stdout": "Status: ON\n" ORoc   "--ipport=6230"], "stdout": "Success: Rebooted\n"
	var expectedString string
	if fenceAgentAction == "status" {
		expectedString = succeesStatusMessage
	} else if fenceAgentAction == "reboot" {
		expectedString = succeesRebootMessage
	}
	expectedString = nodeIdentifierPrefix + "=" + testNodeID + "\"], \"standard output\": " + expectedString
	log.Info("String has been created", "expectedString", expectedString)
	return expectedString

}

// checkFarLogs gets the FAR pod and checks whether it's logs have logString
func checkFarLogs(logString string) {
	var pod *corev1.Pod
	var err error
	EventuallyWithOffset(1, func() *corev1.Pod {
		pod, err = farUtils.GetFenceAgentsRemediationPod(k8sClient)
		if err != nil {
			log.Error(err, "failed to get pod. Might try again")
			return nil
		}
		return pod
	}, timeoutLogs, pollInterval).ShouldNot(BeNil(), "can't find the pod after timeout")

	EventuallyWithOffset(1, func() string {
		logs, err := farE2eUtils.GetLogs(clientSet, pod, containerName)
		if err != nil {
			log.Error(err, "failed to get logs. Might try again")
			return ""
		}
		return logs
	}, timeoutLogs, pollInterval).Should(ContainSubstring(logString))
}

// wasNodeRebooted waits until there is a newer boot time than before, a reboot occurred, otherwise it falls with an error
func wasNodeRebooted(nodeName string, nodeBootTimeBefore time.Time) {
	log.Info("boot time", "node", nodeName, "old", nodeBootTimeBefore)
	var nodeBootTimeAfter time.Time
	Eventually(func() (time.Time, error) {
		var errBootAfter error
		nodeBootTimeAfter, errBootAfter = getNodeBootTime(nodeName)
		if errBootAfter != nil {
			log.Error(errBootAfter, "Can't get boot time of the node")
		}
		return nodeBootTimeAfter, errBootAfter
	}, timeoutReboot, pollInterval).Should(
		BeTemporally(">", nodeBootTimeBefore), "Timeout for node reboot has passed, even though FAR CR has been created")

	log.Info("successful reboot", "node", nodeName, "offset between last boot", nodeBootTimeAfter.Sub(nodeBootTimeBefore), "new boot time", nodeBootTimeAfter)
}
