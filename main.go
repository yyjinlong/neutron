// Copyright 2015 CNI authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/utils/buildversion"
	"github.com/containernetworking/plugins/pkg/utils/sysctl"
	"github.com/coreos/etcd/clientv3"
	"github.com/j-keck/arping"
	"github.com/vishvananda/netlink"

	"neutron/pkg/config"
	"neutron/pkg/etcd"
	"neutron/pkg/ipam"
	"neutron/pkg/log"
)

const (
	IPv4InterfaceArpProxySysctlTemplate = "net.ipv4.conf.%s.proxy_arp"
)

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()

	log.InitLogger("/var/log/macvlan.log")
}

func main() {
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.All, buildversion.BuildString("macvlan"))
}

func getClient(bytes []byte) (*clientv3.Client, error) {
	conf, err := config.ReadLocalConf(bytes)
	if err != nil {
		return nil, err
	}

	etcdConf := etcd.NewEtcdConf()
	client, err := etcdConf.Connect(conf.Etcd.URLs, conf.Etcd.CAFile, conf.Etcd.KeyFile, conf.Etcd.CertFile)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// NOTE: 修改loadConf
func loadConf(client *clientv3.Client, envArgs string) (*config.NetConf, string, error) {
	etcdConf := etcd.NewEtcdConf()
	conf, err := etcdConf.GetServiceConf(client, envArgs)
	if err != nil {
		return nil, "", err
	}

	n, err := config.ReadTotalConf(conf)
	if n.Master == "" {
		defaultRouteInterface, err := getDefaultRouteInterfaceName()
		if err != nil {
			return nil, "", err
		}
		n.Master = defaultRouteInterface
	}
	return n, n.CNIVersion, nil
}

func getDefaultRouteInterfaceName() (string, error) {
	routeToDstIP, err := netlink.RouteList(nil, netlink.FAMILY_ALL)
	if err != nil {
		return "", err
	}

	for _, v := range routeToDstIP {
		if v.Dst == nil {
			l, err := netlink.LinkByIndex(v.LinkIndex)
			if err != nil {
				return "", err
			}
			return l.Attrs().Name, nil
		}
	}

	return "", fmt.Errorf("no default route interface found")
}

// Equivalent to: `ip link add link bond0 name mac1 type macvlan mode bridge`
func createMacvlan(conf *config.NetConf, ifName string, netns ns.NetNS) (*current.Interface, error) {
	macvlan := &current.Interface{}

	mode, _ := modeFromString()
	log.Infof("Cmd add create macvlan master is: %s", conf.Master)
	m, err := netlink.LinkByName(conf.Master)
	if err != nil {
		log.Infof("Cmd add link by name error: %s", err)
		if err.Error() == "Link not found" {
			m, err = createVlanInterface(conf)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to lookup master %q: %v", conf.Master, err)
		}
	}

	// due to kernel bug we have to create with tmpName or it might
	// collide with the name on the host and error out
	tmpName, err := ip.RandomVethName()
	if err != nil {
		return nil, err
	}

	mv := &netlink.Macvlan{
		LinkAttrs: netlink.LinkAttrs{
			MTU:         conf.MTU,
			Name:        tmpName,
			ParentIndex: m.Attrs().Index,
			Namespace:   netlink.NsFd(int(netns.Fd())),
		},
		Mode: mode,
	}

	if err := netlink.LinkAdd(mv); err != nil {
		return nil, fmt.Errorf("failed to create macvlan: %v", err)
	}

	err = netns.Do(func(_ ns.NetNS) error {
		// TODO: duplicate following lines for ipv6 support, when it will be added in other places
		ipv4SysctlValueName := fmt.Sprintf(IPv4InterfaceArpProxySysctlTemplate, tmpName)
		if _, err := sysctl.Sysctl(ipv4SysctlValueName, "1"); err != nil {
			// remove the newly added link and ignore errors, because we already are in a failed state
			_ = netlink.LinkDel(mv)
			return fmt.Errorf("failed to set proxy_arp on newly added interface %q: %v", tmpName, err)
		}

		log.Infof("Cmd add create macvlan tmpName is: %s", tmpName)
		log.Infof("Cmd add create macvlan ifName is: %s", ifName)
		err := ip.RenameLink(tmpName, ifName)
		if err != nil {
			_ = netlink.LinkDel(mv)
			return fmt.Errorf("failed to rename macvlan to %q: %v", ifName, err)
		}
		macvlan.Name = ifName

		// Re-fetch macvlan to get all properties/attributes
		contMacvlan, err := netlink.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to refetch macvlan %q: %v", ifName, err)
		}
		macvlan.Mac = contMacvlan.Attrs().HardwareAddr.String()
		macvlan.Sandbox = netns.Path()

		return nil
	})
	if err != nil {
		return nil, err
	}

	return macvlan, nil
}

func modeFromString() (netlink.MacvlanMode, error) {
	return netlink.MACVLAN_MODE_BRIDGE, nil
}

func modeToString() (string, error) {
	return "bridge", nil
}

// create vlan interface if it not exist. eg bond0.1234
func createVlanInterface(conf *config.NetConf) (netlink.Link, error) {
	intfInfo := strings.Split(conf.Master, ".")
	if len(intfInfo) != 2 {
		return nil, fmt.Errorf("Cmd add invalid vlan interface: %s", conf.Master)
	}
	pName := intfInfo[0]
	vlanId, err := strconv.Atoi(intfInfo[1])
	if err != nil {
		return nil, fmt.Errorf("Cmd add invalid vlan id: %s", err)
	}
	pLink, err := netlink.LinkByName(pName)
	if err != nil {
		return nil, fmt.Errorf("Cmd add can't found parent device: %s", err)
	}
	if pLink.Attrs().OperState != netlink.OperUp {
		return nil, fmt.Errorf("Cmd add vlan parentt device: %s not up.", pName)
	}

	// step1 创建vlan接口
	vl := &netlink.Vlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        conf.Master,
			ParentIndex: pLink.Attrs().Index,
		},
		VlanId: vlanId,
	}
	if err := netlink.LinkAdd(vl); err != nil {
		return nil, fmt.Errorf("Cmd add failed to create vlan: %s", err)
	}

	// step2 启用该vlan接口
	mlink, err := netlink.LinkByName(conf.Master)
	if err != nil {
		return nil, err
	}
	if err := netlink.LinkSetUp(mlink); err != nil {
		return nil, fmt.Errorf("Cmd add ip link set %s up failed: %s", conf.Master, err)
	}

	// step3 状态更新后，重新取下网卡
	mlink, err = netlink.LinkByName(conf.Master)
	if err != nil {
		return nil, err
	}
	return mlink, nil
}

func cmdAdd(args *skel.CmdArgs) error {
	log.Info("Cmd add begin to create macvlan.")
	client, err := getClient(args.StdinData)
	if err != nil {
		return err
	}

	n, cniVersion, err := loadConf(client, args.Args)
	if err != nil {
		return err
	}
	log.Infof("Cmd add get plugin cni version: %s", cniVersion)

	isLayer3 := n.IPAM.Type != ""
	log.Infof("Cmd add current isLayer3=%t", isLayer3)

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", netns, err)
	}
	defer netns.Close()

	macvlanInterface, err := createMacvlan(n, args.IfName, netns)
	if err != nil {
		return err
	}

	// Delete link if err to avoid link leak in this ns
	defer func() {
		if err != nil {
			netns.Do(func(_ ns.NetNS) error {
				return ip.DelLinkByName(args.IfName)
			})
		}
	}()

	// Assume L2 interface only
	result := &current.Result{CNIVersion: cniVersion, Interfaces: []*current.Interface{macvlanInterface}}

	if isLayer3 {
		log.Infof("Cmd add put args.StdinData into ipam to add.")
		// run the IPAM plugin and get back the config to apply
		r, err := ipam.ExecAdd(client, n, args)
		if err != nil {
			return err
		}

		// Invoke ipam del if err to avoid ip leak
		defer func() {
			if err != nil {
				ipam.ExecDel(client, n, args)
			}
		}()

		// Convert whatever the IPAM result was into the current Result type
		ipamResult, err := current.NewResultFromResult(r)
		if err != nil {
			return err
		}

		if len(ipamResult.IPs) == 0 {
			return errors.New("IPAM plugin returned missing IP config")
		}

		result.IPs = ipamResult.IPs
		result.Routes = ipamResult.Routes

		for _, ipc := range result.IPs {
			// All addresses apply to the container macvlan interface
			ipc.Interface = current.Int(0)
		}

		err = netns.Do(func(_ ns.NetNS) error {
			if err := ipam.ConfigureIface(args.IfName, result); err != nil {
				return err
			}

			contVeth, err := net.InterfaceByName(args.IfName)
			if err != nil {
				return fmt.Errorf("failed to look up %q: %v", args.IfName, err)
			}

			for _, ipc := range result.IPs {
				if ipc.Version == "4" {
					_ = arping.GratuitousArpOverIface(ipc.Address.IP, *contVeth)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	} else {
		// For L2 just change interface status to up
		err = netns.Do(func(_ ns.NetNS) error {
			macvlanInterfaceLink, err := netlink.LinkByName(args.IfName)
			if err != nil {
				return fmt.Errorf("failed to find interface name %q: %v", macvlanInterface.Name, err)
			}

			if err := netlink.LinkSetUp(macvlanInterfaceLink); err != nil {
				return fmt.Errorf("failed to set %q UP: %v", args.IfName, err)
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	result.DNS = n.DNS

	return types.PrintResult(result, cniVersion)
}

// Equivalent to: `ip link del link dev bond0.388`
func cmdDel(args *skel.CmdArgs) error {
	log.Info("Cmd del begin to del macvlan.")
	client, err := getClient(args.StdinData)
	if err != nil {
		return err
	}

	n, _, err := loadConf(client, args.Args)
	if err != nil {
		return err
	}

	isLayer3 := n.IPAM.Type != ""
	log.Infof("Cmd del current isLayer3=%t", isLayer3)

	if isLayer3 {
		log.Info("Cmd del put args.StdinData into ipam to del.")
		err = ipam.ExecDel(client, n, args)
		if err != nil {
			return err
		}
	}

	if args.Netns == "" {
		return nil
	}

	// There is a netns so try to clean up. Delete can be called multiple times
	// so don't return an error if the device is already removed.
	err = ns.WithNetNSPath(args.Netns, func(_ ns.NetNS) error {
		if err := ip.DelLinkByName(args.IfName); err != nil {
			if err != ip.ErrLinkNotFound {
				return err
			}
		}
		return nil
	})

	return err
}

func cmdCheck(args *skel.CmdArgs) error {
	client, err := getClient(args.StdinData)
	if err != nil {
		return err
	}

	n, _, err := loadConf(client, args.Args)
	if err != nil {
		return err
	}
	isLayer3 := n.IPAM.Type != ""

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	if isLayer3 {
		// run the IPAM plugin and get back the config to apply
		err = ipam.ExecCheck(client, n, args)
		if err != nil {
			return err
		}
	}

	// Parse previous result.
	if n.NetConf.RawPrevResult == nil {
		return fmt.Errorf("Required prevResult missing")
	}

	if err := version.ParsePrevResult(&n.NetConf); err != nil {
		return err
	}

	result, err := current.NewResultFromResult(n.PrevResult)
	if err != nil {
		return err
	}

	var contMap current.Interface
	// Find interfaces for names whe know, macvlan device name inside container
	for _, intf := range result.Interfaces {
		if args.IfName == intf.Name {
			if args.Netns == intf.Sandbox {
				contMap = *intf
				continue
			}
		}
	}

	// The namespace must be the same as what was configured
	if args.Netns != contMap.Sandbox {
		return fmt.Errorf("Sandbox in prevResult %s doesn't match configured netns: %s",
			contMap.Sandbox, args.Netns)
	}

	m, err := netlink.LinkByName(n.Master)
	if err != nil {
		return fmt.Errorf("failed to lookup master %q: %v", n.Master, err)
	}

	// Check prevResults for ips, routes and dns against values found in the container
	if err := netns.Do(func(_ ns.NetNS) error {

		// Check interface against values found in the container
		err := validateCniContainerInterface(contMap, m.Attrs().Index, n.Mode)
		if err != nil {
			return err
		}

		err = ip.ValidateExpectedInterfaceIPs(args.IfName, result.IPs)
		if err != nil {
			return err
		}

		err = ip.ValidateExpectedRoute(result.Routes)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	return nil
}

func validateCniContainerInterface(intf current.Interface, parentIndex int, modeExpected string) error {
	var link netlink.Link
	var err error

	if intf.Name == "" {
		return fmt.Errorf("Container interface name missing in prevResult: %v", intf.Name)
	}
	link, err = netlink.LinkByName(intf.Name)
	if err != nil {
		return fmt.Errorf("Container Interface name in prevResult: %s not found", intf.Name)
	}
	if intf.Sandbox == "" {
		return fmt.Errorf("Error: Container interface %s should not be in host namespace", link.Attrs().Name)
	}

	macv, isMacvlan := link.(*netlink.Macvlan)
	if !isMacvlan {
		return fmt.Errorf("Error: Container interface %s not of type macvlan", link.Attrs().Name)
	}

	mode, _ := modeFromString()
	if macv.Mode != mode {
		return fmt.Errorf("Container macvlan mode %v does not match expected value: %v", macv.Mode, mode)
	}

	if intf.Mac != "" {
		if intf.Mac != link.Attrs().HardwareAddr.String() {
			return fmt.Errorf("Interface %s Mac %s doesn't match container Mac: %s", intf.Name, intf.Mac, link.Attrs().HardwareAddr)
		}
	}
	return nil
}
