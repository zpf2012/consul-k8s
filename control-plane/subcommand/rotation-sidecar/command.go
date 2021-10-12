package rotationsidecar

import (
	"crypto/md5"
	"flag"
	"io/ioutil"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/hashicorp/consul-k8s/control-plane/consul"
	"github.com/hashicorp/consul-k8s/control-plane/subcommand/common"
	"github.com/hashicorp/consul-k8s/control-plane/subcommand/flags"
	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/go-hclog"
	"github.com/mitchellh/cli"
)

type Command struct {
	UI cli.Ui

	flagLogLevel string
	flagLogJSON  bool

	flagGossipEncryptionFile string

	flagSet *flag.FlagSet
	http    *flags.HTTPFlags

	consulClient *api.Client
	once         sync.Once
	help         string
	sigCh        chan os.Signal
	logger       hclog.Logger
}

func (c *Command) init() {
	c.flagSet = flag.NewFlagSet("", flag.ContinueOnError)
	c.flagSet.StringVar(&c.flagGossipEncryptionFile, "gossip-encryption-file", "", "Path of the gossip encryption file.")
	c.flagSet.StringVar(&c.flagLogLevel, "log-level", "info",
		"Log verbosity level. Supported values (in order of detail) are \"trace\", "+
			"\"debug\", \"info\", \"warn\", and \"error\".")
	c.flagSet.BoolVar(&c.flagLogJSON, "log-json", false,
		"Enable or disable JSON output format for logging.")

	c.http = &flags.HTTPFlags{}
	flags.Merge(c.flagSet, c.http.Flags())
	c.help = flags.Usage(help, c.flagSet)

	if c.sigCh == nil {
		c.sigCh = make(chan os.Signal, 1)
		signal.Notify(c.sigCh, syscall.SIGINT, syscall.SIGTERM)
	}

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
	cfg := api.DefaultConfig()
	c.http.MergeOntoConfig(cfg)
	c.consulClient, err = consul.NewClient(cfg)
	if err != nil {
		c.logger.Error("Unable to get client connection", "error", err)
		return 1
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		c.logger.Error("Unable to set watcher", "error", err)
		return 1
	}
	defer watcher.Close()

	err = watcher.Add(c.flagGossipEncryptionFile)
	if err != nil {
		c.logger.Error("Unable to add file to watcher", "error", err)
		return 1
	}
	errCh := make(chan error)

	data, err := ioutil.ReadFile(c.flagGossipEncryptionFile)
	if err != nil {
		c.logger.Error("Unable to read secret file: ", "error", err)
		return 1
	}

	podIP := os.Getenv("POD_IP")
	currentChecksum := md5.Sum(data)
	for {
		select {
		case event := <-watcher.Events:
			leader, _ := c.consulClient.Status().Leader()
			if leader == "" {
				continue
			} else if strings.Split(leader, ":")[0] != podIP {
				continue
			}
			switch {
			case event.Op&fsnotify.Remove == fsnotify.Remove:
				err = watcher.Add(event.Name)
				fallthrough
			case event.Op&fsnotify.Write == fsnotify.Write:
				c.logger.Info("Write detected, checking to see if the file changed", "filename", event.Name)
				data, err := ioutil.ReadFile(c.flagGossipEncryptionFile)
				if err != nil {
					c.logger.Error("Unable to read secret file: ", "error", err)
					continue
				}
				checksum := md5.Sum(data)
				if checksum != currentChecksum {
					c.logger.Info("New Encryption Key, executing rotation: ", "key", string(data))
					if err := c.installKey(string(data)); err == nil {
						currentChecksum = checksum
					}
				}
			}
		case err := <-watcher.Errors:
			errCh <- err
		case <-time.After(600 * time.Second):
			c.logger.Info("Reconcile Timer.")
		case <-c.sigCh:
			break
		}
	}

	c.logger.Error("Error channel: %v", <-errCh)
	c.logger.Info("Exiting")
	return 0
}

func (c *Command) installKey(newKey string) error {
	oldkeyringList, err := c.consulClient.Operator().KeyringList(nil)
	if err != nil {
		c.logger.Error("unable to get old keyring list")
		return err
	}
	c.logger.Info("Old primary keys: ", "key", oldkeyringList[0].PrimaryKeys)
	c.logger.Info("Installing new key: ", "key", newKey)
	err = c.consulClient.Operator().KeyringInstall(newKey, nil)
	if err != nil {
		c.logger.Error("Unable to install key to keyring: ", "err", err)
		return err
	}
	for i := 0; i < 100; i++ {
		time.Sleep(1 * time.Second)
		keyringList, err := c.consulClient.Operator().KeyringList(nil)
		if err != nil {
			c.logger.Error("Unable to get keyring list, retrying.")
			continue
		}
		for x, _ := range keyringList[0].Keys {
			if x == string(newKey) {
				c.logger.Info("Setting new key to primary: ", "key", newKey)
				if err := c.consulClient.Operator().KeyringUse(newKey, nil); err != nil {
					c.logger.Error("Unable to set key to primary, retrying. ", "key", newKey, "err", err)
					continue
				}
			}
		}
		for x, _ := range keyringList[0].Keys {
			if x != newKey {
				c.logger.Info("Deleting Key: ", "key", x)
				if err := c.consulClient.Operator().KeyringRemove(x, nil); err != nil {
					c.logger.Error("Unable to delete old primary key, retrying. ", "err", err)
				}
			}
		}
		c.logger.Info("Key rotation completed: ", "key", newKey)
		return nil
	}
	return nil

}

func (c *Command) deleteKeysNotIn(keys map[string]int, key string) {
	for k, _ := range keys {
		if k != key {
			if err := c.consulClient.Operator().KeyringRemove(k, nil); err != nil {
				c.logger.Error("unable to remove old key from keyring: %v, %v", k, err)
			}
			c.logger.Error("removed key %v", k)
		}
	}
	return
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
