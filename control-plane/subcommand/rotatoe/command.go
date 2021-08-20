package rotatoe

import (
	"flag"
	"hash/crc32"
	"io/ioutil"
	"sync"
	"time"

	"github.com/hashicorp/consul-k8s/control-plane/consul"
	"github.com/hashicorp/consul-k8s/control-plane/subcommand/common"
	"github.com/hashicorp/consul-k8s/control-plane/subcommand/flags"
	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/go-hclog"
	"github.com/mitchellh/cli"
)

const (
	defaultBearerTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	defaultTokenSinkFile   = "/consul/connect-inject/acl-token"
	defaultProxyIDFile     = "/consul/connect-inject/proxyid"

	// The number of times to attempt ACL Login.
	numLoginRetries = 3
	// The number of times to attempt to read this service (120s).
	defaultServicePollingRetries = 120
)

type Command struct {
	UI cli.Ui

	flagACLAuthMethod          string // Auth Method to use for ACLs, if enabled.
	flagPodName                string // Pod name.
	flagPodNamespace           string // Pod namespace.
	flagAuthMethodNamespace    string // Consul namespace the auth-method is defined in.
	flagConsulServiceNamespace string // Consul destination namespace for the service.
	flagServiceAccountName     string // Service account name.
	flagServiceName            string // Service name.
	flagLogLevel               string
	flagLogJSON                bool

	flagWatchFile string

	flagSet *flag.FlagSet
	http    *flags.HTTPFlags

	consulClient *api.Client
	once         sync.Once
	help         string
	logger       hclog.Logger
}

func (c *Command) init() {
	c.flagSet = flag.NewFlagSet("", flag.ContinueOnError)
	c.flagSet.StringVar(&c.flagACLAuthMethod, "acl-auth-method", "", "Name of the auth method to login to.")
	c.flagSet.StringVar(&c.flagWatchFile, "file", "", "file to watch")
	c.flagSet.StringVar(&c.flagPodName, "pod-name", "", "Name of the pod.")
	c.flagSet.StringVar(&c.flagPodNamespace, "pod-namespace", "", "Name of the pod namespace.")
	c.flagSet.StringVar(&c.flagAuthMethodNamespace, "auth-method-namespace", "", "Consul namespace the auth-method is defined in")
	c.flagSet.StringVar(&c.flagConsulServiceNamespace, "consul-service-namespace", "", "Consul destination namespace of the service.")
	c.flagSet.StringVar(&c.flagServiceAccountName, "service-account-name", "", "Service account name on the pod.")
	c.flagSet.StringVar(&c.flagServiceName, "service-name", "", "Service name as specified via the pod annotation.")
	c.flagSet.StringVar(&c.flagLogLevel, "log-level", "info",
		"Log verbosity level. Supported values (in order of detail) are \"trace\", "+
			"\"debug\", \"info\", \"warn\", and \"error\".")
	c.flagSet.BoolVar(&c.flagLogJSON, "log-json", false,
		"Enable or disable JSON output format for logging.")

	c.http = &flags.HTTPFlags{}
	flags.Merge(c.flagSet, c.http.Flags())
	c.help = flags.Usage(help, c.flagSet)

}

func (c *Command) Run(args []string) int {
	var err error
	c.once.Do(c.init)

	if err := c.flagSet.Parse(args); err != nil {
		return 1
	}

	// Set up logging.
	if c.logger == nil {
		var err error
		c.logger, err = common.Logger(c.flagLogLevel, c.flagLogJSON)
		if err != nil {
			c.UI.Error(err.Error())
			return 1
		}
	}
	time.Sleep(10 * time.Second)
	cfg := api.DefaultConfig()
	cfg.Namespace = c.flagConsulServiceNamespace
	c.http.MergeOntoConfig(cfg)
	c.consulClient, err = consul.NewClient(cfg)
	if err != nil {
		c.logger.Error("==================== Unable to get client connection", "error", err)
		return 1
	}

	inputFileContents, err := ioutil.ReadFile(c.flagWatchFile)
	c.logger.Error("========== Original inputFile: %v", string(inputFileContents))
	table := crc32.MakeTable(crc32.Castagnoli)

	currentCRC := crc32.Checksum(inputFileContents, table)

	for {
		time.Sleep(10 * time.Second)
		inputFileContents, err := ioutil.ReadFile(c.flagWatchFile)
		c.logger.Error("========== Current inputFile: %v", string(inputFileContents))
		if err != nil {
			c.logger.Error("unable to read file")
		}
		chksum := crc32.Checksum(inputFileContents, table)
		c.logger.Error("===== checksum: %s / %s ", currentCRC, chksum)
		if chksum != currentCRC {
			currentCRC = chksum
			// ROTATE
			err = c.installKey(string(inputFileContents))
			c.logger.Error(" ========== FINISHED UPDATING GOSSIP KEY =========")
		}
	}
	c.logger.Info("======== TEST =========")
	return 0
}

func (c *Command) installKey(newKey string) error {

	err := c.consulClient.Operator().KeyringInstall(newKey, nil)
	if err != nil {
		c.logger.Error("unable to add key to keyring: %s", err)
	}
	for i := 0; i < 100; i++ {
		time.Sleep(10 * time.Second)
		keyringList, err := c.consulClient.Operator().KeyringList(nil)
		if err != nil {
			c.logger.Error("===== unable to get keyring list =====")
			continue
		}
		c.logger.Error("=== keyringList: %v", keyringList)
		if keyringList != nil {
			c.logger.Error(" keys: %v: %v", len(keyringList), keyringList[0].Keys)
		}
		if _, ok := keyringList[0].Keys[newKey]; ok {
			c.logger.Error("found updated key")
			err = c.consulClient.Operator().KeyringUse(newKey, nil)
			if err != nil {
				c.logger.Error("===== unable to set keyring to use new key =====")
			}
			return nil
		}
	}
	return nil
}

func (c *Command) Synopsis() string { return synopsis }
func (c *Command) Help() string {
	c.once.Do(c.init)
	return c.help
}

const synopsis = "Inject connect init command."
const help = `
Usage: consul-k8s-control-plane connect-init [options]

  Bootstraps connect-injected pod components.
  Not intended for stand-alone use.
`
