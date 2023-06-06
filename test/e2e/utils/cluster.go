package utils

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	machineclient "github.com/openshift/machine-api-operator/pkg/generated/clientset/versioned/typed/machine/v1beta1"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
)

// See OCP API for Machine in https://docs.openshift.com/container-platform/latest/rest_api/machine_apis/machine-machine-openshift-io-v1beta1.html

const (
	clusterPlatformName = "cluster"
	machinesNamespace   = "openshift-machine-api"
)

// GetClusterInfo fetch the cluster's infrastructure object to identify it's type
func GetClusterInfo(config configclient.Interface) (*configv1.Infrastructure, error) {
	// Copy paste from https://github.com/hybrid-cloud-patterns/patterns-operator/blob/main/controllers/pattern_controller.go#L293-L313
	// oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.metadata.name}'
	// oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.spec.platformSpec.type}'

	clusterInfra, err := config.ConfigV1().Infrastructures().Get(context.Background(), clusterPlatformName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return clusterInfra, nil
}

// GetCredentials searches for AWS or BMH secret, and then returns it decoded
func GetCredentials(clientSet *kubernetes.Clientset, secretName, secretNamespace, secretKey, secretVal string) (string, string, error) {
	// oc get secrets -n openshift-machine-api aws-cloud-credentials -o jsonpath='{.data.aws_access_key_id}' | base64 -d
	// oc get secrets -n openshift-machine-api aws-cloud-credentials -o jsonpath='{.data.aws_secret_access_key}' | base64 -d

	secret, err := clientSet.CoreV1().Secrets(secretNamespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	return string(secret.Data[secretKey]), string(secret.Data[secretVal]), nil
}

// GetAWSNodeInfoList returns a list of the node names and their identification, e.g., AWS instance ID
func GetAWSNodeInfoList(machineClient *machineclient.MachineV1beta1Client) (map[v1alpha1.NodeName]string, error) {
	//  oc get machine -n openshift-machine-api MACHINE_NAME -o jsonpath='{.spec.providerID}'
	//  oc get machine -n openshift-machine-api MACHINE_NAME -o jsonpath='{.status.nodeRef.name}'

	nodeList := make(map[v1alpha1.NodeName]string)

	// Get the list of Machines in the openshift-machine-api namespace
	machineList, err := machineClient.Machines(machinesNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nodeList, err
	}

	var missNodeMachineErr error
	// creates map for nodeName and AWS instance ID
	for _, machine := range machineList.Items {
		if machine.Status.NodeRef == nil || machine.Spec.ProviderID == nil {
			if missNodeMachineErr != nil {
				missNodeMachineErr = fmt.Errorf("machine %s is not associated with any node or it't provider ID is missing\n%w", string(machine.Spec.Name), missNodeMachineErr)
			} else {
				missNodeMachineErr = fmt.Errorf("machine %s is not associated with any node or it't provider ID is missing", string(machine.Spec.Name))
			}
			continue
		}
		nodeName := v1alpha1.NodeName(string(machine.Status.NodeRef.Name))
		providerID := string(*machine.Spec.ProviderID)

		// Get the instance ID from the provider ID aws:///us-east-1b/i-082ac37ab919a82c2 -> i-082ac37ab919a82c2
		splitedProviderID := strings.Split(providerID, "/i-")
		instanceID := "i-" + splitedProviderID[1]
		nodeList[nodeName] = instanceID
		fmt.Printf("node: %s Instance ID: %s \n", nodeName, instanceID)
	}
	return nodeList, missNodeMachineErr
}

// GetBMHNodeInfoList returns a list of the node names and their identification, e.g., ports
func GetBMHNodeInfoList(machineClient *machineclient.MachineV1beta1Client) (map[v1alpha1.NodeName]string, error) {

	//TODO: seacrch for BM and fetch ports

	nodeList := map[v1alpha1.NodeName]string{
		"master-0": "6230",
		"master-1": "6231",
		"master-2": "6232",
		"worker-0": "6233",
		"worker-1": "6234",
		"worker-2": "6235",
	}
	return nodeList, nil
}
