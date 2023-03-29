package utils

// Copy paste from https://github.com/hybrid-cloud-patterns/patterns-operator/blob/main/controllers/pattern_controller.go#L293-L313

import (
	"context"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetClusterPlatform(config *configclient.Interface) (string, error) {
	// oc get Infrastructure.config.openshift.io/cluster  -o jsonpath='{.spec.platformSpec.type}'
	c := *config
	clusterInfra, err := c.ConfigV1().Infrastructures().Get(context.Background(), "cluster", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	//   status:
	//    apiServerInternalURI: https://api-int.beekhof49.blueprints.rhecoeng.com:6443
	//    apiServerURL: https://api.beekhof49.blueprints.rhecoeng.com:6443
	//    controlPlaneTopology: HighlyAvailable
	//    etcdDiscoveryDomain: ""
	//    infrastructureName: beekhof49-pqzfb
	//    infrastructureTopology: HighlyAvailable
	//    platform: AWS
	//    platformStatus:
	//      aws:
	//        region: ap-southeast-2
	//      type: AWS
	return string(clusterInfra.Spec.PlatformSpec.Type), nil
}
