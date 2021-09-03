# Consul Kubernetes CLI
This repository contains a CLI tool for installing and operating [Consul](https://www.consul.io/) on Kubernetes. 
**Warning** this tool is currently experimental. Do not use it on Consul clusters you care about.

## Installation & Setup
Currently the tool is not available on any releases page. Instead clone the repository and run `go build -o bin/consul-k8s`
and proceed to run the binary.

## Commands
* [consul-k8s install](# consul-k8s install)

### consul-k8s install
```
Usage: consul-k8s install [flags]

 Install Consul onto a Kubernetes cluster.

Flags:


  -auto-approve
 	Skip confirmation prompt.

  -dry-run
 	Validate installation and return summary of installation.

  -config-file,-f=<string>
 	Path to a file to customize the installation, such as Consul Helm chart values file. Can be specified multiple times.

  -name=<string>
 	Name of the installation. This will be prefixed to resources installed on the cluster.

  -namespace=<string>
 	Namespace for the Consul installation. Defaults to “consul”.

  -preset=<string>
 	Use an installation preset, one of default, demo, secure. Defaults to "default".

  -set=<string>
 	Set a value to customize. Can be specified multiple times. Supports Consul Helm chart values.

  -set-file=<string>
      Set a value to customize via a file. The contents of the file will be set as the value. Can be specified multiple times. Supports Consul Helm chart values.

  -set-string=<string>
      Set a string value to customize. Can be specified multiple times. Supports Consul Helm chart values.


Global Flags:
-context=<string> 
	Kubernetes context to use

-kubeconfig, -c=<string>
	Path to kubeconfig file
```
