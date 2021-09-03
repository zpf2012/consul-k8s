package install

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/consul-k8s/cli/cmd/common"
	"github.com/hashicorp/consul-k8s/cli/cmd/common/flag"
	"github.com/hashicorp/consul-k8s/cli/cmd/common/terminal"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	helmCLI "helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/cli/values"
	"helm.sh/helm/v3/pkg/getter"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"sigs.k8s.io/yaml"
)

const (
	FlagPreset    = "preset"
	DefaultPreset = ""

	FlagReleaseName    = "name"
	DefaultReleaseName = "consul"

	FlagValueFiles      = "config-file"
	FlagSetStringValues = "set-string"
	FlagSetValues       = "set"
	FlagFileValues      = "set-file"

	FlagDryRun    = "dry-run"
	DefaultDryRun = false

	FlagSkipConfirm    = "skip-confirm"
	DefaultSkipConfirm = false

	FlagNamespace    = "namespace"
	DefaultNamespace = "consul"

	HelmRepository = "https://helm.releases.hashicorp.com"
)

type Command struct {
	*common.BaseCommand

	kubernetes kubernetes.Interface

	set *flag.Sets

	flagPreset          string
	flagReleaseName     string
	flagNamespace       string
	flagDryRun          bool
	flagSkipConfirm     bool
	flagValueFiles      []string
	flagSetStringValues []string
	flagSetValues       []string
	flagFileValues      []string

	flagKubeConfig  string
	flagKubeContext string

	once sync.Once
	help string
}

func (c *Command) init() {
	// Store all the possible preset values in 'presetList'. Printed in the help message.
	var presetList []string
	for name := range presets {
		presetList = append(presetList, name)
	}

	c.set = flag.NewSets()
	{
		f := c.set.NewSet("Command Options")
		f.BoolVar(&flag.BoolVar{
			Name:    FlagSkipConfirm,
			Target:  &c.flagSkipConfirm,
			Default: DefaultSkipConfirm,
			Usage:   "Skip confirmation prompt.",
		})
		f.BoolVar(&flag.BoolVar{
			Name:    FlagDryRun,
			Target:  &c.flagDryRun,
			Default: DefaultDryRun,
			Usage:   "Validate installation and return summary of installation.",
		})
		f.StringSliceVar(&flag.StringSliceVar{
			Name:    FlagValueFiles,
			Aliases: []string{"f"},
			Target:  &c.flagValueFiles,
			Usage:   "Path to a file to customize the installation, such as Consul Helm chart values file. Can be specified multiple times.",
		})
		f.StringVar(&flag.StringVar{
			Name:    FlagReleaseName,
			Target:  &c.flagReleaseName,
			Default: DefaultReleaseName,
			Usage:   "Name of the installation. This will be prefixed to resources installed on the cluster.",
		})
		f.StringVar(&flag.StringVar{
			Name:    FlagNamespace,
			Target:  &c.flagNamespace,
			Default: DefaultNamespace,
			Usage:   fmt.Sprintf("Namespace for the Consul installation. Defaults to \"%q\".", DefaultNamespace),
		})
		f.StringVar(&flag.StringVar{
			Name:    FlagPreset,
			Target:  &c.flagPreset,
			Default: DefaultPreset,
			Usage:   fmt.Sprintf("Use an installation preset, one of %s. Defaults to \"%q\"", strings.Join(presetList, ", "), DefaultPreset),
		})
		f.StringSliceVar(&flag.StringSliceVar{
			Name:   FlagSetValues,
			Target: &c.flagSetValues,
			Usage:  "Set a value to customize. Can be specified multiple times. Supports Consul Helm chart values.",
		})
		f.StringSliceVar(&flag.StringSliceVar{
			Name:   FlagFileValues,
			Target: &c.flagFileValues,
			Usage: "Set a value to customize via a file. The contents of the file will be set as the value. Can be " +
				"specified multiple times. Supports Consul Helm chart values.",
		})
		f.StringSliceVar(&flag.StringSliceVar{
			Name:   FlagSetStringValues,
			Target: &c.flagSetStringValues,
			Usage:  "Set a string value to customize. Can be specified multiple times. Supports Consul Helm chart values.",
		})

		f = c.set.NewSet("Global Options")
		f.StringVar(&flag.StringVar{
			Name:    "kubeconfig",
			Aliases: []string{"c"},
			Target:  &c.flagKubeConfig,
			Default: "",
			Usage:   "Path to kubeconfig file.",
		})
		f.StringVar(&flag.StringVar{
			Name:    "context",
			Target:  &c.flagKubeContext,
			Default: "",
			Usage:   "Kubernetes context to use.",
		})
	}

	c.help = c.set.Help()
}

func (c *Command) Run(args []string) int {
	c.once.Do(c.init)
	// Note that `c.init` and `c.Init` are NOT the same thing. One initializes the command struct,
	// the other the UI. It looks similar because BaseCommand is embedded in Command.
	c.Init()
	defer func() {
		if err := c.Close(); err != nil {
			c.UI.Output(err.Error())
		}
	}()

	// The logger is initialized in main with the name cli. Here, we reset the name to install so log lines would be prefixed with install.
	c.Log.ResetNamed("install")

	if err := validateFlags(c, args); err != nil {
		c.UI.Output(err.Error())
		return 1
	}

	// helmCLI.New() will create a settings object which is used by the Helm Go SDK calls.
	// Any overrides by our kubeconfig and kubecontext flags is done here. The Kube client that
	// is created will use this command's flags first, then the HELM_KUBECONTEXT environment variable,
	// then call out to genericclioptions.ConfigFlag

	// This hack is rather hacky.
	prevHelmNSEnv := os.Getenv("HELM_NAMESPACE")
	os.Setenv("HELM_NAMESPACE", c.flagNamespace)
	settings := helmCLI.New()
	os.Setenv("HELM_NAMESPACE", prevHelmNSEnv)
	if c.flagKubeConfig != "" {
		settings.KubeConfig = c.flagKubeConfig
	}
	if c.flagKubeContext != "" {
		settings.KubeContext = c.flagKubeContext
	}

	// Setup logger to stream Helm library logs
	var uiLogger = func(s string, args ...interface{}) {
		logMsg := fmt.Sprintf(s, args...)
		c.UI.Output(logMsg, terminal.WithInfoStyle())
	}

	// Setup action configuration for Helm Go SDK function calls.
	actionConfig := new(action.Configuration)
	err := actionConfig.Init(settings.RESTClientGetter(), c.flagNamespace,
		os.Getenv("HELM_DRIVER"), uiLogger)
	if err != nil {
		c.UI.Output(err.Error())
		return 1
	}

	// Set up the kubernetes client to use for non Helm SDK calls to the Kubernetes API
	// The Helm SDK will use settings.RESTClientGetter for its calls as well, so this will
	// use a consistent method to target the right cluster for both Helm SDK and non Helm SDK calls.
	if c.kubernetes == nil {
		restConfig, err := settings.RESTClientGetter().ToRESTConfig()
		if err != nil {
			c.UI.Output("Retrieving Kubernetes auth: %v", err, terminal.WithErrorStyle())
			return 1
		}
		c.kubernetes, err = kubernetes.NewForConfig(restConfig)
		if err != nil {
			c.UI.Output("Initializing Kubernetes client: %v", err, terminal.WithErrorStyle())
			return 1
		}
	}

	c.UI.Output("Pre-Install Checks", terminal.WithHeaderStyle())

	// Need a specific action config to call helm list, where namespace is NOT specified.
	listConfig := new(action.Configuration)
	err = listConfig.Init(settings.RESTClientGetter(), "",
		os.Getenv("HELM_DRIVER"), uiLogger)
	if err != nil {
		c.UI.Output(err.Error())
		return 1
	}

	lister := action.NewList(listConfig)
	lister.AllNamespaces = true
	res, err := lister.Run()
	if err != nil {
		c.UI.Output("Error checking for installations", terminal.WithErrorStyle())
		return 1
	}
	for _, rel := range res {
		if rel.Chart.Metadata.Name == "consul" {
			// TODO: In the future the user will be prompted with our own uninstall command.
			c.UI.Output("Existing Consul installation found (name=%s, namespace=%s) - run helm "+
				"delete %s -n %s if you wish to re-install",
				rel.Name, rel.Namespace, rel.Name, rel.Namespace, terminal.WithErrorStyle())
			return 1
		}
	}
	c.UI.Output("No existing installations found", terminal.WithSuccessStyle())

	// Ensure there's no previous PVCs lying around.
	pvcs, err := c.kubernetes.CoreV1().PersistentVolumeClaims("").List(c.Ctx, metav1.ListOptions{})
	if err != nil {
		c.UI.Output("Error listing PVCs: %v", err, terminal.WithErrorStyle())
		return 1
	}
	var previousPVCs []string
	for _, pvc := range pvcs.Items {
		if strings.Contains(pvc.Name, "consul-server") {
			previousPVCs = append(previousPVCs, fmt.Sprintf("%s/%s", pvc.Namespace, pvc.Name))
		}
	}

	if len(previousPVCs) > 0 {
		c.UI.Output("Found PVCs from previous installations (%s), delete before re-installing",
			strings.Join(previousPVCs, ","), terminal.WithErrorStyle())
		return 1
	}
	c.UI.Output("No previous persistent volume claims found", terminal.WithSuccessStyle())

	// Ensure there's no previous bootstrap secret lying around.
	secrets, err := c.kubernetes.CoreV1().Secrets("").List(c.Ctx, metav1.ListOptions{})
	if err != nil {
		c.UI.Output("Error listing secrets: %v", err, terminal.WithErrorStyle())
		return 1
	}
	for _, secret := range secrets.Items {
		// TODO: also check for federation secret
		if strings.Contains(secret.Name, "consul-bootstrap-acl-token") {
			c.UI.Output("Found consul-acl-bootstrap secret from previous installations: %q in namespace %q. To delete, run kubectl delete secret %s --namespace %s",
				secret.Name, secret.Namespace, secret.Name, secret.Namespace, terminal.WithErrorStyle())
			return 1
		}
	}
	c.UI.Output("No previous secrets found", terminal.WithSuccessStyle())

	// Handle preset, value files, and set values logic.
	p := getter.All(settings)
	v := &values.Options{
		ValueFiles:   c.flagValueFiles,
		StringValues: c.flagSetStringValues,
		Values:       c.flagSetValues,
		FileValues:   c.flagFileValues,
	}
	vals, err := v.MergeValues(p)
	if err != nil {
		c.UI.Output("Error merging values: %v", err, terminal.WithErrorStyle())
		return 1
	}
	if c.flagPreset != DefaultPreset {
		// Note the ordering of the function call, presets have lower precedence than set vals.
		presetMap := presets[c.flagPreset].(map[string]interface{})
		vals = mergeMaps(presetMap, vals)
	}

	install := action.NewInstall(actionConfig)
	install.ReleaseName = c.flagReleaseName
	install.Namespace = c.flagNamespace
	install.CreateNamespace = true
	install.ChartPathOptions.RepoURL = HelmRepository
	install.Wait = true
	install.Timeout = time.Minute * 10

	// Dry Run should exit here, no need to actual locate/download the charts.
	if c.flagDryRun {
		c.UI.Output("Dry run complete - installation can proceed.", terminal.WithInfoStyle())
	}

	if !c.flagSkipConfirm {
		c.UI.Output("Consul Installation Summary", terminal.WithHeaderStyle())
		c.UI.Output("Installation name: %s", c.flagReleaseName, terminal.WithInfoStyle())
		c.UI.Output("Namespace: %s", c.flagNamespace, terminal.WithInfoStyle())

		valuesYaml, err := yaml.Marshal(vals)
		if err != nil {
			c.UI.Output("Overrides:"+"\n"+"%+v", err, terminal.WithInfoStyle())
		} else if len(vals) == 0 {
			c.UI.Output("Overrides: "+string(valuesYaml), terminal.WithInfoStyle()) // TODO: Cleaner solution for this \n issue.
		} else {
			c.UI.Output("Overrides:"+"\n"+string(valuesYaml), terminal.WithInfoStyle()) // TODO: Cleaner solution for this \n issue.
		}
	}

	// Without informing the user, let global.Name be equal to consul if it hasn't been set already.
	vals = mergeMaps(convert(setGlobalName), vals)

	if c.flagDryRun {
		return 0
	} else if !c.flagSkipConfirm {
		confirmation, err := c.UI.Input(&terminal.Input{
			Prompt: "Proceed with installation? (y/n)",
			Style:  terminal.InfoStyle,
			Secret: false,
		})

		if err != nil {
			c.UI.Output(err.Error(), terminal.WithErrorStyle())
			return 1
		}
		confirmation = strings.TrimSuffix(confirmation, "\n")
		if !(strings.ToLower(confirmation) == "y" || strings.ToLower(confirmation) == "yes") {
			c.UI.Output("Install aborted. To learn how to customize your installation, run:\nconsul-k8s install --help", terminal.WithInfoStyle())
			return 1
		}
	}

	c.UI.Output("Running Installation", terminal.WithHeaderStyle())

	// Locate the chart, install it in some cache locally.
	chartPath, err := install.ChartPathOptions.LocateChart("consul", settings)
	if err != nil {
		c.UI.Output(err.Error(), terminal.WithErrorStyle())
		return 1
	}

	// Actually load the chart into memory.
	chart, err := loader.Load(chartPath)
	if err != nil {
		c.UI.Output(err.Error(), terminal.WithErrorStyle())
		return 1
	}
	c.UI.Output("Downloaded charts", terminal.WithSuccessStyle())

	// Run the install.
	_, err = install.Run(chart, vals)
	if err != nil {
		c.UI.Output(err.Error(), terminal.WithErrorStyle())
		return 1
	}
	c.UI.Output("Consul installed into namespace %q", c.flagNamespace, terminal.WithSuccessStyle())

	return 0
}
func (c *Command) Help() string {
	c.once.Do(c.init)
	s := "Usage: consul-k8s install [flags]" + "\n" + "Install Consul onto a Kubernetes cluster." + "\n"
	return s + "\n" + c.help
}

func (c *Command) Synopsis() string {
	return "Install Consul on Kubernetes."
}

// This is a helper function used in Run. Merges two maps giving b precedent.
// @source: https://github.com/helm/helm/blob/main/pkg/cli/values/options.go
func mergeMaps(a, b map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(a))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if v, ok := v.(map[string]interface{}); ok {
			if bv, ok := out[k]; ok {
				if bv, ok := bv.(map[string]interface{}); ok {
					out[k] = mergeMaps(bv, v)
					continue
				}
			}
		}
		out[k] = v
	}
	return out
}

// This is a helper function that performs sanity checks on the user's provided flags.
func validateFlags(c *Command, args []string) error {
	if err := c.set.Parse(args); err != nil {
		return err
	} else if len(c.set.Args()) > 0 {
		return errors.New("should have no non-flag arguments")
	} else if len(c.flagValueFiles) != 0 && c.flagPreset != DefaultPreset {
		return errors.New(fmt.Sprintf("Cannot set both -%s and -%s", FlagValueFiles, FlagPreset))
	} else if _, ok := presets[c.flagPreset]; c.flagPreset != DefaultPreset && !ok {
		return errors.New(fmt.Sprintf("'%s' is not a valid preset", c.flagPreset))
	} else if !validLabel(c.flagNamespace) {
		return errors.New(fmt.Sprintf("'%s' is an invalid namespace. Namespaces follow the RFC 1123 label convention and must "+
			"consist of a lower case alphanumeric character or '-' and must start/end with an alphanumeric.", c.flagNamespace))
	} else if len(c.flagValueFiles) != 0 {
		for _, filename := range c.flagValueFiles {
			if _, err := os.Stat(filename); err != nil && os.IsNotExist(err) {
				return errors.New(fmt.Sprintf("File '%s' does not exist.", filename))
			}
		}
	}

	if c.flagDryRun {
		c.UI.Output("Performing dry run installation.", terminal.WithInfoStyle())
	}
	return nil
}

// Helper function that checks if a string follows RFC 1123 labels.
func validLabel(s string) bool {
	for i, c := range s {
		alphanum := ('a' <= c && c <= 'z') || ('0' <= c && c <= '9')
		// If the character is not the last or first, it can be a dash.
		if i != 0 && i != (len(s)-1) {
			alphanum = alphanum || (c == '-')
		}
		if !alphanum {
			return false
		}
	}
	return true
}
