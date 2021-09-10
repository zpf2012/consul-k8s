package install

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/consul-k8s/cli/cmd/common"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Helper function which sets up a Command struct for you.
func getInitializedCommand(t *testing.T) *Command {
	t.Helper()
	log := hclog.New(&hclog.LoggerOptions{
		Name:   "cli",
		Level:  hclog.Info,
		Output: os.Stdout,
	})
	ctx, _ := context.WithCancel(context.Background())

	baseCommand := &common.BaseCommand{
		Ctx: ctx,
		Log: log,
	}

	c := &Command{
		BaseCommand: baseCommand,
	}
	c.init()
	c.Init()
	return c
}

// TestDebugger is used to play with install.go for ad-hoc testing.
func TestDebugger(t *testing.T) {
	c := getInitializedCommand(t)
	c.Run([]string{"-auto-approve", "-f=../../config.yaml"})
}

func TestCheckForPreviousPVCs(t *testing.T) {
	c := getInitializedCommand(t)
	c.kubernetes = fake.NewSimpleClientset()
	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "consul-server-test1",
		},
	}
	pvc2 := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "consul-server-test2",
		},
	}
	c.kubernetes.CoreV1().PersistentVolumeClaims("default").Create(context.TODO(), pvc, metav1.CreateOptions{})
	c.kubernetes.CoreV1().PersistentVolumeClaims("default").Create(context.TODO(), pvc2, metav1.CreateOptions{})
	err := c.checkForPreviousPVCs()
	require.Error(t, err)
	require.Contains(t, err.Error(), "found PVCs from previous installations (default/consul-server-test1,default/consul-server-test2), delete before re-installing")

	// Clear out the client and make sure the check now passes.
	c.kubernetes = fake.NewSimpleClientset()
	err = c.checkForPreviousPVCs()
	require.NoError(t, err)

	// Add a new irrelevant PVC and make sure the check continues to pass.
	pvc = &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "irrelevant-pvc",
		},
	}
	c.kubernetes.CoreV1().PersistentVolumeClaims("default").Create(context.TODO(), pvc, metav1.CreateOptions{})
	err = c.checkForPreviousPVCs()
	require.NoError(t, err)
}

func TestCheckForPreviousSecrets(t *testing.T) {
	c := getInitializedCommand(t)
	c.kubernetes = fake.NewSimpleClientset()
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-consul-bootstrap-acl-token",
		},
	}
	c.kubernetes.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
	err := c.checkForPreviousSecrets()
	require.Error(t, err)
	require.Contains(t, err.Error(), "found consul-acl-bootstrap-token secret from previous installations: \"test-consul-bootstrap-acl-token\" in namespace \"default\". To delete, run kubectl delete secret test-consul-bootstrap-acl-token --namespace default")

	// Clear out the client and make sure the check now passes.
	c.kubernetes = fake.NewSimpleClientset()
	err = c.checkForPreviousSecrets()
	require.NoError(t, err)

	// Add a new irrelevant secret and make sure the check continues to pass.
	secret = &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "irrelevant-secret",
		},
	}
	c.kubernetes.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
	err = c.checkForPreviousSecrets()
	require.NoError(t, err)
}

// TestValidateFlags tests the validate flags function.
func TestValidateFlags(t *testing.T) {
	// The following cases should all error, if they fail to this test fails.
	testCases := []struct {
		input       []string
		description string
	}{
		{[]string{"foo", "-auto-approve"}, "Should disallow non-flag arguments."},
		{[]string{"-f='f.txt'", "-preset=demo"}, "Should disallow specifying both values file AND presets."},
		{[]string{"-preset=foo"}, "Should error on invalid presets."},
		{[]string{"-namespace=\" preset\""}, "Should error on an invalid namespace. If this failed, TestValidLabel() probably did too."},
		{[]string{"-f=\"does_not_exist.txt\""}, "Should have errored on a non-existant file."},
	}

	for _, testCase := range testCases {
		c := getInitializedCommand(t)
		t.Run(testCase.description, func(t *testing.T) {
			if err := c.validateFlags(testCase.input); err == nil {
				t.Errorf("Test case should have failed.")
			}
		})
	}
}

// TestValidLabel calls validLabel() which checks strings match RFC 1123 label convention.
func TestValidLabel(t *testing.T) {
	testCases := []struct {
		expected    bool
		input       string
		description string
	}{
		{true, "1234-abc", "Standard name with leading numbers works."},
		{true, "peppertrout", "All lower case letters works."},
		{true, "pepper-trout", "Test that dashes in the middle are allowed."},
		{false, "Peppertrout", "Capitals violate RFC 1123 lower case label."},
		{false, "ab_cd", "Underscores are not permitted anywhere."},
		{false, "peppertrout-", "The dash must be in the middle of the word, not on the start/end character."},
	}

	for _, testCase := range testCases {
		t.Run(testCase.description, func(t *testing.T) {
			if result := validLabel(testCase.input); result != testCase.expected {
				t.Errorf("Incorrect output, got %v and expected %v", result, testCase.expected)
			}
		})
	}
}

func assertEqual(t *testing.T, a, b interface{}) {
	if a == b {
		return
	}
	t.Errorf(fmt.Sprintf("Assertion failed. %v != %v", a, b))
}
