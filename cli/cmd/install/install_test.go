package install

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/consul-k8s/cli/cmd/common"
	"github.com/hashicorp/go-hclog"
)

// Helper function which sets up a Command struct for you.
func getInitializedCommand() *Command {
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
	c := getInitializedCommand()
	c.Run([]string{"-skip-confirm", "-f=../../config.yaml"})
}

// TestValidateFlags tests the validate flags function.
func TestValidateFlags(t *testing.T) {
	// The following cases should all error, if they fail to this test fails.
	testCases := []struct {
		input       []string
		description string
	}{
		{[]string{"foo", "-skip-confirm"}, "Should disallow non-flag arguments."},
		{[]string{"-f='f.txt'", "-preset=demo"}, "Should disallow specifying both values file AND presets."},
		{[]string{"-preset=foo"}, "Should error on invalid presets."},
		{[]string{"-namespace=\" preset\""}, "Should error on an invalid namespace. If this failed, TestValidLabel() probably did too."},
		{[]string{"-f=\"does_not_exist.txt\""}, "Should have errored on a non-existant file."},
	}

	for _, testCase := range testCases {
		c := getInitializedCommand()
		t.Run(testCase.description, func(t *testing.T) {
			if err := validateFlags(c, testCase.input); err == nil {
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
