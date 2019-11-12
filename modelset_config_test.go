// Copyright (c) 2017,2019, AT&T Intellectual Property. All rights reserved.
//
// Copyright (c) 2017 by Brocade Communications Systems, Inc.
// All rights reserved.
//
// SPDX-License-Identifier: GPL-2.0-only

// This file contains tests relating to the correct allocation of
// configuration according to namespace between different VCI components,
// including the 'default' component.

package provisiond

import (
	"bytes"
	"github.com/danos/config/data"
	"github.com/danos/config/load"
	"github.com/danos/config/schema"
	"github.com/danos/config/testutils"
	"github.com/danos/config/union"
	"github.com/danos/encoding/rfc7951"
	"github.com/danos/vci/conf"
	"github.com/danos/yang/compile"
	"github.com/danos/yangd"
	"io/ioutil"
	"testing"
)

type cfgTestDispatcher struct{}

var cfgTestServices map[string]*cfgTestService

type cfgTestService struct {
	name   string
	config []byte
}

func createTestServiceList() {
	cfgTestServices = make(map[string]*cfgTestService, 0)
}

func addServiceToTestServiceList(svc *cfgTestService) {
	cfgTestServices[svc.name] = svc
}

func (d *cfgTestDispatcher) NewService(name string) (yangd.Service, error) {
	svc := &cfgTestService{name: name}
	addServiceToTestServiceList(svc)
	return svc, nil
}

func (s *cfgTestService) GetRunning(path string) ([]byte, error) {
	return s.config, nil
}

func (s *cfgTestService) GetState(path string) ([]byte, error) {
	return nil, nil
}

func (s *cfgTestService) ValidateCandidate(candidate []byte) error {
	return nil
}

func (s *cfgTestService) SetRunning(candidate []byte) error {
	s.config = candidate
	return nil
}

func getComponentConfigs(t *testing.T, dotCompFiles ...string,
) (configs []*conf.ServiceConfig) {

	for _, file := range dotCompFiles {
		cfg, err := conf.ParseConfiguration([]byte(file))
		if err != nil {
			t.Fatalf("Unexpected component config parse failure:\n  %s\n\n",
				err.Error())
		}
		configs = append(configs, cfg)
	}

	return configs
}

func getTestModelSet(t *testing.T, yangDir string, dotCompFiles ...string,
) (ms schema.ModelSet, err error) {

	compExt := &schema.CompilationExtensions{
		Dispatcher: &cfgTestDispatcher{},
		ComponentConfig: getComponentConfigs(
			t, dotCompFiles...),
	}

	return schema.CompileDir(
		&compile.Config{
			YangDir:      yangDir,
			CapsLocation: ""},
		compExt,
	)
}

func loadCfg(t *testing.T, config string, ms schema.ModelSet) union.Node {
	dnode, err, _ := load.LoadString("testdata/configtest/configd",
		config, ms)
	if err != nil {
		t.Fatalf("Unable to parse configuration: %s", err.Error())
		return nil
	}

	atomicDN := data.NewAtomicNode(dnode)
	return union.NewNode(nil, atomicDN.Load(), ms, nil, 0)
}

// checkConfig - verify expected and actual configuration for service agree.
// Configuration is compared as unformatted JSON (ie no extraneous whitespace).
func checkConfig(
	t *testing.T,
	svcName string,
	expCfg string) {

	svc, ok := cfgTestServices[svcName]
	if !ok {
		t.Fatalf("Unable to find service '%s'.", svcName)
		return
	}

	// Need to compare expConfig(str) with actCfg(JSON).

	actCfgMapOrder, err := svc.GetRunning("")
	if err != nil {
		t.Fatalf("Unable to get actual confg for '%s'.", svcName)
		return
	}
	var actCfgIntf interface{}
	err = rfc7951.Unmarshal(actCfgMapOrder, &actCfgIntf)
	if err != nil {
		t.Fatalf("Unable to unmarshal config for '%s'.", svcName)
		return
	}
	actCfg, err := rfc7951.Marshal(actCfgIntf)
	if err != nil {
		t.Fatalf("Unable to marshal config for '%s'.", svcName)
		return
	}

	var unformattedExpCfg, reformattedExpCfg bytes.Buffer
	rfc7951.Compact(&unformattedExpCfg, []byte(expCfg))
	rfc7951.Indent(
		&reformattedExpCfg, []byte(unformattedExpCfg.String()), "", "\t")

	if string(actCfg) != unformattedExpCfg.String() {
		var formattedActCfg bytes.Buffer
		rfc7951.Indent(&formattedActCfg, actCfg, "", "\t")
		t.Fatalf("Exp:\n%s\nGot:\n%s\n",
			reformattedExpCfg.String(), formattedActCfg.String())
		return
	}
}

const cfgTestComp1 = `[Vyatta Component]
Name=net.vyatta.test.service.first
Description=First Component
ExecName=/opt/vyatta/sbin/first-service
ConfigFile=/etc/vyatta/first.conf

[Model net.vyatta.test.service.first.brocade]
Modules=vyatta-service-first-v1
ModelSets=vyatta-v1`

const cfgTestComp2 = `[Vyatta Component]
Name=net.vyatta.test.service.second
Description=Second Component
ExecName=/opt/vyatta/sbin/second-service
ConfigFile=/etc/vyatta/second.conf

[Model net.vyatta.test.service.second.brocade]
Modules=vyatta-service-second-v1
ModelSets=vyatta-v1`

const cfgTestDefaultComp = `[Vyatta Component]
Name=net.vyatta.test.service.default
Description=Default Component
ExecName=/opt/vyatta/sbin/default-service
ConfigFile=/etc/vyatta/default.conf
DefaultComponent=true

[Model net.vyatta.test.service.default.brocade]
ModelSets=vyatta-v1`

const expCfgComp1 = `
{
	"vyatta-service-first-v1:first": {
		"firstLeaf": "firstValue",
		"firstSubCont": {}
	}
}`

const expCfgComp2 = `
{
	"vyatta-service-first-v1:first": {
		"vyatta-service-second-v1:second": {
			"secondLeaf": "secondValue"
		}
	},
	"vyatta-service-unassigned-a-v1:unassigned-a-cont": {
		"vyatta-service-second-v1:second": {
			"secondLeaf": "secondOtherValue"
		}
	}
}`

const expCfgDefaultComp = `
{
	"vyatta-service-first-v1:first": {
		"firstSubCont": {
			"vyatta-service-unassigned-b-v1:unassigned-b": {
				"unassigned-b-leaf": "foo"
			}
		}
	},
	"vyatta-service-unassigned-a-v1:unassigned-a-cont": {
		"unassigned-a-leaf": "a_value",
		"unassigned-a-subcont": {
			"vyatta-service-unassigned-b-v1:unassigned-b": {
				"unassigned-b-leaf": "bar"
			}
		},
		"vyatta-service-unassigned-c-v1:unassigned-c": {
			"unassigned-c-leaf": "anotherValue"
		}
	}
}`

func TestComponentConfigSet(t *testing.T) {
	createTestServiceList()
	ms, err := getTestModelSet(t, "testdata/configtest_yang",
		cfgTestComp1,
		cfgTestComp2,
		cfgTestDefaultComp)
	if err != nil {
		t.Fatalf("Unable to parse component files: %s", err.Error())
		return
	}

	config, err := ioutil.ReadFile("testdata/configtest/config")
	if err != nil {
		t.Fatalf("Unable to read configuration: %s", err.Error())
		return
	}

	runningCfg := loadCfg(t, string(config), ms)
	ms.ServiceSetRunning(runningCfg, nil)

	checkConfig(t, "net.vyatta.test.service.first.brocade", expCfgComp1)
	checkConfig(t, "net.vyatta.test.service.second.brocade", expCfgComp2)
	checkConfig(t, "net.vyatta.test.service.default.brocade",
		expCfgDefaultComp)
}

func TestCompWithDefaultConfig(t *testing.T) {
	t.Skipf("TBD")
}

func TestCompWithPresence(t *testing.T) {
	t.Skipf("TBD")
}

const cfgComp1ListsJSON = `
{
	"vyatta-service-first-v1:first": {
		"systemList": [
			{"name": "alpha"},
			{"name": "bravo"},
			{"name": "charlie"},
			{"name": "delta"}
		],
		"userList": [
			{"name": "firstEntry"},
			{"name": "secondEntry"},
			{"name": "thirdEntry"},
			{"name": "fourthEntry"}
		]
	}
}`

// TODO - automatic conversion cfg <-> JSON for tests.
// Maybe some sort of builder function that constructs using generic
// cont / list structures, then you can call String() and JSON() methods?
var cfgComp1Lists = testutils.Root(
	testutils.Cont("first",
		testutils.List("userList",
			testutils.ListEntry("firstEntry"),
			testutils.ListEntry("secondEntry"),
			testutils.ListEntry("thirdEntry"),
			testutils.ListEntry("fourthEntry")),
		testutils.List("systemList",
			testutils.ListEntry("alpha"),
			testutils.ListEntry("bravo"),
			testutils.ListEntry("charlie"),
			testutils.ListEntry("delta"))))

func TestCompSetRunningWithOrderedLists(t *testing.T) {
	createTestServiceList()

	ms, err := getTestModelSet(t, "testdata/configtest_yang",
		cfgTestComp1)
	if err != nil {
		t.Fatalf("Unable to parse component files: %s", err.Error())
		return
	}

	runningCfg := loadCfg(t, cfgComp1Lists, ms)
	ms.ServiceSetRunning(runningCfg, nil)

	checkConfig(t, "net.vyatta.test.service.first.brocade",
		cfgComp1ListsJSON)
}
