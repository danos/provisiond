// Copyright (c) 2018-2019, AT&T Intellectual Property. All rights reserved.
//
// Copyright (c) 2017 by Brocade Communications Systems, Inc.
// All rights reserved.
//
// SPDX-License-Identifier: GPL-2.0-only

package provisiond

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"log/syslog"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"

	"github.com/danos/config/schema"
	"github.com/danos/config/yangconfig"
	"github.com/danos/mgmterror"
	"github.com/danos/vci/conf"
	"github.com/danos/yang/compile"
	"github.com/danos/yang/data/datanode"
	yangenc "github.com/danos/yang/data/encoding"
	yang "github.com/danos/yang/schema"
)

const (
	provisiond_config_file = "/etc/vyatta/provisiond.conf"
	componentDir           = "/lib/vci/components"
	yangDir                = "/usr/share/configd/yang"
)

var (
	wlog *log.Logger
)

func init() {
	var err error
	wlog, err = syslog.NewLogger(syslog.LOG_WARNING, 0)
	if err != nil {
		wlog = log.New(os.Stdout, "WARNING", 0)
	}
}

//Config is the object that holds the current running configuration
//It provides update, validation and retrieval of the configuration
type Config struct {
	state *State
	data  atomic.Value
}

type Settings struct{}

func NewConfig(state *State) *Config {
	config := &Config{
		state: state,
	}
	config.data.Store("")
	return config
}

func (c *Config) Get() string {
	// TODO: honour path
	return c.data.Load().(string)
}

func (c *Config) Check(cfg string) error {
	// TODO - run configd:validate here???
	return nil
}

func (c *Config) saveToFile(cfg string) error {
	if err := ioutil.WriteFile(provisiond_config_file, []byte(cfg) /* b */, 0600); err != nil {
		return err
	}

	// If the config file already exists, the call to ioutil.WriteFile won't
	// change the mode, so we force it here.
	if err := os.Chmod(provisiond_config_file, 0600); err != nil {
		return err
	}

	return nil
}

func (c *Config) loadFromFile() error {
	b, err := ioutil.ReadFile(provisiond_config_file)
	if err != nil {
		return err
	}

	c.data.Store(string(b))

	return nil
}

func (c *Config) Set(cfg string) error {
	c.data.Store(cfg)

	err := c.saveToFile(cfg)
	if err != nil {
		return err
	}

	return nil
}

func (c *Config) String() string {
	provisiond := c.data.Load().(string)

	return fmt.Sprintf(
		"Provisiond Config\n"+
			"%s\n", provisiond)
}

//State provides access to the state of the spaceteam instance.
type State struct {
	settings atomic.Value
}

func NewState() *State {
	state := &State{}
	state.settings.Store(&Settings{})
	return state
}

func (s *State) Get() *Settings {
	// TODO - should this ever be called?
	// TODO: honour path
	return s.settings.Load().(*Settings)
}

func (s *State) set(v *Settings) {
	// TODO - should this ever be called?
	new_state := &Settings{}
	if v == nil {
		s.settings.Store(new_state)
		return
	}
	new_state = v
	s.settings.Store(new_state)
}

// NewRPCMap - return map of RPCs indexed by DBUS-compatible name
//
// Provisiond owns all RPCs in the default component.  As we cannot
// (and should not) know what these are at compile time, we don't use
// the standard VCI mechanism of introspection on an RPC object, and
// instead return a map of functions to call the script for each RPC,
// indexed using the DBUS-compatible method name (as opposed to YANG
// name)
func NewRPCMap() map[string]interface{} {
	ms, err := getModelSet()
	if err != nil {
		return nil
	}

	// Filter RPCs to only include ones belonging to the default component
	modMap := ms.GetDefaultServiceModuleMap()
	allRpcs := ms.Rpcs()
	ourRpcs := make(map[string]interface{}, len(allRpcs))
	for ns, rpcs := range allRpcs {
		if _, ok := modMap[ns]; ok {
			modName := getModuleNameFromNamespace(ms, ns)
			modRpcs := make(map[string]interface{}, len(rpcs))
			for name, rpc := range rpcs {
				modRpcs[name] = genRpcFunc(modName, name, rpc)
			}
			if len(modRpcs) > 0 {
				ourRpcs[modName] = modRpcs
			}
		}
	}

	return ourRpcs
}

func getModuleNameFromNamespace(ms schema.ModelSet, ns string) string {
	for name, mod := range ms.Modules() {
		if mod.Namespace() == ns {
			return name
		}
	}
	return ""
}

func getModelSet() (schema.ModelSet, error) {
	compConfig, err := conf.LoadComponentConfigDir(componentDir)
	if err != nil {
		return nil, nil
	}

	ycfg := yangconfig.NewConfig().IncludeYangDirs(yangDir).
		IncludeFeatures(compile.DefaultCapsLocation).SystemConfig()

	return schema.CompileDir(
		&compile.Config{
			YangLocations: ycfg.YangLocator(),
			Features:      ycfg.FeaturesChecker(),
			Filter:        compile.IsConfig},
		&schema.CompilationExtensions{
			ComponentConfig: compConfig,
		})
}

func genRpcFunc(
	modName string,
	rpcName string,
	rpc interface{},
) func(in string) (string, error) {
	rpcFunc := func(jsonIn string) (jsonOut string, err error) {
		return handleLegacyRpc(rpc.(schema.Rpc), modName, rpcName, jsonIn)
	}
	return rpcFunc
}

func handleLegacyRpc(
	rpc schema.Rpc,
	modName string,
	rpcName string,
	args string,
) (string, error) {

	inputTree, jerr := yangenc.UnmarshalJSON(
		rpc.Input().(schema.Node), []byte(args))
	if jerr != nil {
		err := mgmterror.NewOperationFailedApplicationError()
		err.Message = fmt.Sprintf("Unexpected failure parsing input JSON: %s",
			jerr.Error())
		return "", err
	}

	return callLegacyRpcScript(rpc, modName, rpcName, inputTree)
}

func resolvePath(name, path string) string {
	if strings.Contains(name, "/") {
		// Contains a path separator, return unaltered
		return name
	}

	pathDirs := strings.Split(path, ":")
	for _, dir := range pathDirs {
		// Build possible absolute path and check if it exists
		abs := dir + "/" + name
		d, _ := os.Stat(abs)
		if d != nil {
			// Found a match
			return abs
		}
	}

	// Not found, return unaltered
	return name
}

func callLegacyRpcScript(
	rpc schema.Rpc,
	modName string,
	rpcName string,
	inputTree datanode.DataNode,
) (string, error) {
	scriptPaths :=
		"/bin:/usr/bin:/sbin:/usr/sbin:/opt/vyatta/bin:/opt/vyatta/sbin"
	var script string
	var input []byte

	if rpc.Script() != "" {
		script = rpc.Script()
		inputWithDefaults := yang.AddDefaults(rpc.Input(), inputTree)
		input = yangenc.ToJSON(rpc.Input(), inputWithDefaults)
	} else {
		err := mgmterror.NewOperationFailedApplicationError()
		err.Message = fmt.Sprintf("Unexpected failure: missing RPC script (%s)",
			rpcName)
		return "", err
	}

	rpcargs := strings.Split(script, " ")

	name := resolvePath(rpcargs[0], scriptPaths)
	c := exec.Command(name, rpcargs[1:]...)
	c.Env = append(os.Environ(), "PATH="+scriptPaths)
	c.Stdin = bytes.NewBuffer(input)
	var stdErr bytes.Buffer
	c.Stderr = &stdErr
	output, err := c.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			cerr := mgmterror.NewOperationFailedApplicationError()
			cerr.Message = fmt.Sprintf("Failure to spawn RPC process: %s",
				err.Error())
			return "", cerr
		}
	}
	if !c.ProcessState.Success() {
		errStr := stdErr.String()
		if errStr == "" {
			errStr = string(output)
		}
		cerr := mgmterror.NewOperationFailedApplicationError()
		cerr.Message = fmt.Sprintf("RPC failure: %s", errStr)
		return "", cerr
	}

	stdErrStr := stdErr.String()
	if stdErrStr != "" {
		wlog.Printf("%s:%s (%s): %s\n", modName, rpcName, script, stdErrStr)
	}
	return string(output), err
}
