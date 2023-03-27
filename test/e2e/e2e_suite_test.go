package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"go.uber.org/zap/zapcore"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.
var (
	log       logr.Logger
	clientSet *kubernetes.Clientset
	k8sClient ctrl.Client

	// The ns test pods are started in
	testNsName = "far-test"
)

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "FAR e2e suite")
}

var _ = BeforeSuite(func() {
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseFlagOptions(&opts)))
	log = logf.Log

	// +kubebuilder:scaffold:scheme

	// get the k8sClient or die
	config, err := config.GetConfig()
	if err != nil {
		Fail(fmt.Sprintf("Couldn't get kubeconfig %v", err))
	}
	clientSet, err = kubernetes.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	Expect(clientSet).NotTo(BeNil())

	scheme.AddToScheme(scheme.Scheme)
	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = ctrl.New(config, ctrl.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	// create test ns
	testNs := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNsName,
			Labels: map[string]string{
				// allow privileged pods in test namespace, needed for API blocker pod
				"pod-security.kubernetes.io/enforce":             "privileged",
				"security.openshift.io/scc.podSecurityLabelSync": "true",
			},
		},
	}
	err = k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(testNs), testNs)
	if errors.IsNotFound(err) {
		err = k8sClient.Create(context.Background(), testNs)
	}
	Expect(err).ToNot(HaveOccurred(), "could not get or create test ns")

	debug()
})

func debug() {
	version, _ := clientSet.ServerVersion()
	fmt.Fprint(GinkgoWriter, version)
}
