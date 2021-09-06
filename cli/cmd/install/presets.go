package install

import "sigs.k8s.io/yaml"

const (
	PresetDemo   = "demo"
	PresetSecure = "secure"
)

// Preset map which maps preset name to a map from string
// to interface{}. Basically just YAML.
var presets = map[string]interface{}{
	PresetDemo:   convert(demo),
	PresetSecure: convert(secure),
}

// Below are the various presets in YAML.
var demo = `
global:
  name: consul
connectInject:
  enabled: true
server:
  replicas: 1
  bootstrapExpect: 1
`

var secure = `
global:
  name: consul
  acls:
    manageSystemACLs: true
  tls:
    enabled: true
connectInject:
  enabled: true
server:
  replicas: 1
  bootstrapExpect: 1
`

var globalNameConsul = `
global:
  name: consul
`

// Helper function so we can easily convert YAML to map in line.
func convert(s string) map[string]interface{} {
	var m map[string]interface{}
	yaml.Unmarshal([]byte(s), &m)
	return m
}
