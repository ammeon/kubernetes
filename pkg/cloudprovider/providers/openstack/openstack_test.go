/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package openstack

import (
	"errors"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pborman/uuid"
	"github.com/rackspace/gophercloud"
	"github.com/rackspace/gophercloud/openstack/compute/v2/servers"
	"github.com/rackspace/gophercloud/openstack/networking/v2/extensions/layer3/routers"
	"github.com/rackspace/gophercloud/openstack/networking/v2/networks"
	"github.com/rackspace/gophercloud/openstack/networking/v2/subnets"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/util/rand"
)

const volumeAvailableStatus = "available"
const volumeInUseStatus = "in-use"
const volumeCreateTimeoutSeconds = 30

var env TestEnvironment

func WaitForVolumeStatus(t *testing.T, os *OpenStack, volumeName string, status string, timeoutSeconds int) {
	timeout := timeoutSeconds
	start := time.Now().Second()
	for {
		time.Sleep(1 * time.Second)

		if timeout >= 0 && time.Now().Second()-start >= timeout {
			t.Logf("Volume (%s) status did not change to %s after %v seconds\n",
				volumeName,
				status,
				timeout)
			return
		}

		getVol, err := os.getVolume(volumeName)
		if err != nil {
			t.Fatalf("Cannot get existing Cinder volume (%s): %v", volumeName, err)
		}
		if getVol.Status == status {
			t.Logf("Volume (%s) status changed to %s after %v seconds\n",
				volumeName,
				status,
				timeout)
			return
		}
	}
}

func TestReadConfig(t *testing.T) {
	_, err := readConfig(nil)
	if err == nil {
		t.Errorf("Should fail when no config is provided: %s", err)
	}

	cfg, err := readConfig(strings.NewReader(`
[Global]
auth-url = http://auth.url
username = user
[LoadBalancer]
create-monitor = yes
monitor-delay = 1m
monitor-timeout = 30s
monitor-max-retries = 3
`))
	if err != nil {
		t.Fatalf("Should succeed when a valid config is provided: %s", err)
	}
	if cfg.Global.AuthUrl != "http://auth.url" {
		t.Errorf("incorrect authurl: %s", cfg.Global.AuthUrl)
	}

	if !cfg.LoadBalancer.CreateMonitor {
		t.Errorf("incorrect lb.createmonitor: %t", cfg.LoadBalancer.CreateMonitor)
	}
	if cfg.LoadBalancer.MonitorDelay.Duration != 1*time.Minute {
		t.Errorf("incorrect lb.monitordelay: %s", cfg.LoadBalancer.MonitorDelay)
	}
	if cfg.LoadBalancer.MonitorTimeout.Duration != 30*time.Second {
		t.Errorf("incorrect lb.monitortimeout: %s", cfg.LoadBalancer.MonitorTimeout)
	}
	if cfg.LoadBalancer.MonitorMaxRetries != 3 {
		t.Errorf("incorrect lb.monitormaxretries: %d", cfg.LoadBalancer.MonitorMaxRetries)
	}
}

func TestToAuthOptions(t *testing.T) {
	cfg := Config{}
	cfg.Global.Username = "user"
	// etc.

	ao := cfg.toAuthOptions()

	if !ao.AllowReauth {
		t.Errorf("Will need to be able to reauthenticate")
	}
	if ao.Username != cfg.Global.Username {
		t.Errorf("Username %s != %s", ao.Username, cfg.Global.Username)
	}
}

// This allows acceptance testing against an existing OpenStack
// install, using the standard OS_* OpenStack client environment
// variables.
// FIXME: it would be better to hermetically test against canned JSON
// requests/responses.
func configFromEnv() (cfg Config, ok bool) {
	cfg.Global.AuthUrl = os.Getenv("OS_AUTH_URL")

	cfg.Global.TenantId = os.Getenv("OS_TENANT_ID")
	// Rax/nova _insists_ that we don't specify both tenant ID and name
	if cfg.Global.TenantId == "" {
		cfg.Global.TenantName = os.Getenv("OS_TENANT_NAME")
	}

	cfg.Global.Username = os.Getenv("OS_USERNAME")
	cfg.Global.Password = os.Getenv("OS_PASSWORD")
	cfg.Global.ApiKey = os.Getenv("OS_API_KEY")
	cfg.Global.Region = os.Getenv("OS_REGION_NAME")
	cfg.Global.DomainId = os.Getenv("OS_DOMAIN_ID")
	cfg.Global.DomainName = os.Getenv("OS_DOMAIN_NAME")
	cfg.LoadBalancer.FloatingNetworkId = os.Getenv("OS_FLOATING_NETWORK_ID")

	ok = (cfg.Global.AuthUrl != "" &&
		cfg.Global.Username != "" &&
		(cfg.Global.Password != "" || cfg.Global.ApiKey != "") &&
		(cfg.Global.TenantId != "" || cfg.Global.TenantName != "" ||
			cfg.Global.DomainId != "" || cfg.Global.DomainName != ""))

	return
}

func TestNewOpenStack(t *testing.T) {
	cfg, ok := configFromEnv()
	if !ok {
		t.Skipf("No config found in environment")
	}

	_, err := newOpenStack(cfg)
	if err != nil {
		t.Fatalf("Failed to construct/authenticate OpenStack: %s", err)
	}
}

func TestInstances(t *testing.T) {
	os := env.Openstack

	i, ok := os.Instances()
	if !ok {
		t.Fatalf("Instances() returned false")
	}

	srvs, err := i.List(".")
	if err != nil {
		t.Fatalf("Instances.List() failed: %s", err)
	}
	if len(srvs) == 0 {
		t.Fatalf("Instances.List() returned zero servers")
	}
	t.Logf("Found servers (%d): %s\n", len(srvs), srvs)

	srvExternalId, err := i.ExternalID(srvs[0])
	if err != nil {
		t.Fatalf("Instances.ExternalId(%s) failed: %s", srvs[0], err)
	}
	t.Logf("Found server (%s), with external id: %s\n", srvs[0], srvExternalId)

	srvInstanceId, err := i.InstanceID(srvs[0])
	if err != nil {
		t.Fatalf("Instance.InstanceId(%s) failed: %s", srvs[0], err)
	}
	t.Logf("Found server (%s), with instance id: %s\n", srvs[0], srvInstanceId)

	addrs, err := i.NodeAddresses(srvs[0])
	if err != nil {
		t.Fatalf("Instances.NodeAddresses(%s) failed: %s", srvs[0], err)
	}
	t.Logf("Found NodeAddresses(%s) = %s\n", srvs[0], addrs)
}

func TestLoadBalancer(t *testing.T) {
	cfg, ok := configFromEnv()
	if !ok {
		t.Skipf("No config found in environment")
	}

	cfg.LoadBalancer.LBVersion = "v2"

	os, err := newOpenStack(cfg)
	if err != nil {
		t.Fatalf("Failed to construct/authenticate OpenStack: %s", err)
	}

	lb, ok := os.LoadBalancer()
	if !ok {
		t.Fatalf("LoadBalancer() returned false - perhaps your stack doesn't support Neutron?")
	}

	_, exists, err := lb.GetLoadBalancer(&api.Service{ObjectMeta: api.ObjectMeta{Name: "noexist"}})
	if err != nil {
		t.Fatalf("GetLoadBalancer(\"noexist\") returned error: %s", err)
	}
	if exists {
		t.Fatalf("GetLoadBalancer(\"noexist\") returned exists")
	}
}

func TestLoadBalancerV2(t *testing.T) {
	cfg, ok := configFromEnv()
	if !ok {
		t.Skipf("No config found in environment")
	}
	cfg.LoadBalancer.LBVersion = "v2"

	os, err := newOpenStack(cfg)
	if err != nil {
		t.Fatalf("Failed to construct/authenticate OpenStack: %s", err)
	}

	lbaas, ok := os.LoadBalancer()
	if !ok {
		t.Fatalf("LoadBalancer() returned false - perhaps your stack doesn't support Neutron?")
	}

	_, exists, err := lbaas.GetLoadBalancer(&api.Service{ObjectMeta: api.ObjectMeta{Name: "noexist"}})
	if err != nil {
		t.Fatalf("GetLoadBalancer(\"noexist\") returned error: %s", err)
	}
	if exists {
		t.Fatalf("GetLoadBalancer(\"noexist\") returned exists")
	}
}

func TestZones(t *testing.T) {
	os := OpenStack{
		provider: &gophercloud.ProviderClient{
			IdentityBase: "http://auth.url/",
		},
		region: "myRegion",
	}

	z, ok := os.Zones()
	if !ok {
		t.Fatalf("Zones() returned false")
	}

	zone, err := z.GetZone()
	if err != nil {
		t.Fatalf("GetZone() returned error: %s", err)
	}

	if zone.Region != "myRegion" {
		t.Fatalf("GetZone() returned wrong region (%s)", zone.Region)
	}
}

func TestVolumes(t *testing.T) {
	os := env.Openstack

	tags := map[string]string{
		"test": "value",
	}
	vol, err := os.CreateVolume("kubernetes-test-volume-"+rand.String(10), 1, &tags)
	if err != nil {
		t.Fatalf("Cannot create a new Cinder volume: %v", err)
	}
	t.Logf("Volume (%s) created\n", vol)

	WaitForVolumeStatus(t, os, vol, volumeAvailableStatus, volumeCreateTimeoutSeconds)

	diskId, err := os.AttachDisk(env.Servers[0].ID, vol)
	if err != nil {
		t.Fatalf("Cannot AttachDisk Cinder volume %s: %v", vol, err)
	}
	t.Logf("Volume (%s) attached, disk ID: %s\n", vol, diskId)

	WaitForVolumeStatus(t, os, vol, volumeInUseStatus, volumeCreateTimeoutSeconds)

	err = os.DetachDisk(env.Servers[0].ID, vol)
	if err != nil {
		t.Fatalf("Cannot DetachDisk Cinder volume %s: %v", vol, err)
	}
	t.Logf("Volume (%s) detached\n", vol)

	WaitForVolumeStatus(t, os, vol, volumeAvailableStatus, volumeCreateTimeoutSeconds)

	err = os.DeleteVolume(vol)
	if err != nil {
		t.Fatalf("Cannot delete Cinder volume %s: %v", vol, err)
	}
	t.Logf("Volume (%s) deleted\n", vol)

}

type TestEnvironment struct {
	Subnet  *subnets.Subnet
	Network *networks.Network
	Router  *routers.Router

	Servers   []*servers.Server
	Openstack *OpenStack
	UUID      string
}

func TestMain(m *testing.M) {
	log.Printf("setup environment")
	err := setup()
	if err == nil {
		m.Run()
	}
	log.Printf("teardown environment")
	teardown()
	os.Exit(0)
}

func setup() error {
	env = TestEnvironment{UUID: uuid.New()}
	cfg, ok := configFromEnv()
	if !ok {
		log.Printf("No config found in environment")
		return errors.New("No config found in environment")
	}
	cfg.Route = RouteOpts{
		HostnameOverride: true,
	}

	openstack, err := newOpenStack(cfg)
	if err != nil {
		log.Printf("Failed to construct/authenticate OpenStack: %s", err)
		return err
	}
	env.Openstack = openstack

	err = openstack.Network()
	if err != nil {
		return err
	}

	netopts := networks.CreateOpts{Name: env.UUID, AdminStateUp: networks.Up}
	network, err := networks.Create(openstack.network, netopts).Extract()
	if err != nil {
		log.Printf("Test network not created: %s", err)
		return err
	}
	log.Printf("Test network %s created", env.UUID)
	env.Network = network

	subnetOpts := subnets.CreateOpts{
		NetworkID: network.ID,
		CIDR:      "192.168.199.0/24",
		IPVersion: subnets.IPv4,
		Name:      env.UUID,
	}

	// Execute the operation and get back a subnets.Subnet struct
	subnet, err := subnets.Create(openstack.network, subnetOpts).Extract()
	if err != nil {
		log.Printf("Test subnet not created: %s", err)
		return err
	}
	log.Printf("Test subnet %s created", env.UUID)
	env.Subnet = subnet
	env.Openstack.lbOpts.SubnetId = subnet.ID

	err = openstack.Compute()
	if err != nil {
		return err
	}

	serverOpts := servers.CreateOpts{
		Name:       env.UUID,
		ImageName:  "cirros",
		FlavorName: "m1.tiny",
		Networks:   []servers.Network{{UUID: network.ID}}}
	server, err := servers.Create(openstack.compute, serverOpts).Extract()
	if err != nil {
		log.Printf("Test server not created: %s", err)
		return err
	}
	log.Printf("Test server %s created", env.UUID)
	env.Servers = append(env.Servers, server)

	routerOpts := routers.CreateOpts{
		Name:        env.UUID,
		GatewayInfo: &routers.GatewayInfo{NetworkID: openstack.lbOpts.FloatingNetworkId},
	}
	router, err := routers.Create(openstack.network, routerOpts).Extract()
	if err != nil {
		log.Printf("Test router not created: %s", err)
		return err
	}
	log.Printf("Test router %s created", env.UUID)
	env.Router = router
	env.Openstack.routeOpts.RouterId = router.ID

	interfaceOpts := routers.InterfaceOpts{
		SubnetID: subnet.ID,
	}
	_, err = routers.AddInterface(openstack.network, router.ID, interfaceOpts).Extract()
	if err != nil {
		log.Printf("Interface not created: %s", err)
		return err
	}
	log.Printf("Router/subnet interface created")

	// TODO: Should limit amount of loops here or return error if status is
	// in an expected state
	for server.Status != "ACTIVE" {
		server, err = servers.Get(openstack.compute, server.ID).Extract()
		if err != nil {
			log.Printf("Server not active yet")
			return err
		}
		time.Sleep(time.Second * 5)
	}
	return nil
}

func teardown() {
	for _, server := range env.Servers {
		err := servers.Delete(env.Openstack.compute, server.ID).ExtractErr()
		if err != nil {
			log.Printf("Server %s not deleted: %s", server.ID, err)
		}
	}
	if env.Subnet != nil {
		interfaceOpts := routers.InterfaceOpts{
			SubnetID: env.Subnet.ID,
		}
		if env.Router != nil {
			_, err := routers.RemoveInterface(env.Openstack.network, env.Router.ID, interfaceOpts).Extract()
			if err != nil {
				log.Printf("Interface for subnet %s not deleted: %s", env.Subnet.ID, err)
			}
			err = routers.Delete(env.Openstack.network, env.Router.ID).ExtractErr()
			if err != nil {
				log.Printf("Router %s not deleted: %s", env.Router.ID, err)
			}
		}
		time.Sleep(time.Second * 10)
		err := subnets.Delete(env.Openstack.network, env.Subnet.ID).ExtractErr()
		if err != nil {
			log.Printf("Subnet %s not deleted: %s", env.Subnet.ID, err)
		}
	}
	if env.Network != nil {
		err := networks.Delete(env.Openstack.network, env.Network.ID).ExtractErr()
		if err != nil {
			log.Printf("Network %s not deleted: %s", env.Network.ID, err)
		}
	}
}

func TestGetServerByName(t *testing.T) {
	os := env.Openstack

	srv, err := getServerByName(os.compute, env.UUID)
	if err != nil {
		t.Fatalf("Instance %s not found: %s", env.UUID, err)
	}
	t.Logf("%s", srv)
}

func TestRoutes(t *testing.T) {
	os := env.Openstack

	routes, ok := os.Routes()
	if !ok {
		t.Fatalf("Routes() returned false - perhaps your stack doesn't support Neutron?")
	}

	newroute := cloudprovider.Route{
		DestinationCIDR: "10.164.2.0/24",
		TargetInstance: cloudprovider.Instance{
			Name: env.UUID + ".openstack.timbyr.com",
			ID:   "openstack:///" + env.Servers[0].ID,
		},
	}
	err := os.CreateRoute("test", "", &newroute)
	if err != nil {
		t.Fatalf("%s", err)
	}

	routelist, err := routes.ListRoutes("")
	if err != nil {
		t.Fatalf("ListRoutes() returned an err - %s", err)
	}
	for _, route := range routelist {
		_, cidr, err := net.ParseCIDR(route.DestinationCIDR)
		if err != nil {
			t.Logf("Ignoring route %s, unparsable CIDR: %v", route.Name, err)
		}
		t.Logf("%s", cidr)
		t.Logf("what %s %s", route.DestinationCIDR, route.TargetInstance)
	}

	err = os.DeleteRoute("test", &newroute)
	if err != nil {
		t.Fatalf("%s", err)
	}

}
