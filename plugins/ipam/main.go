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
	"fmt"
	"net"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"

	"cni/plugins/g"
	"cni/plugins/ipam/backend/allocator"
	"cni/plugins/ipam/backend/etcd"
	bv "cni/plugins/pkg/utils/buildversion"

)

func main() {
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.All, bv.BuildString("ipam-etcd"))
}

func cmdCheck(args *skel.CmdArgs) error {
	log.Info("Cmd check start check config.")

	// step1, 解析本地配置
	lc, err := g.ParseLocalConf(args.StdinData)
	if err != nil {
		return err
	}

	// step2, 连接etcd
	etcdClient, err := g.ConnectEtcd(lc.Etcd)
	if err != nil {
		return err
	}
	defer etcdClient.Close()

	// step3, 获取service、pod
	service, podname := g.GetCurrentServiceAndPod(args.Args)
	if service == "" {
		return fmt.Errorf("Cmd add fetch service from args.Args: %s is failed.", args.Args)
	}

	// Look to see if there is at least one IP address allocated to the container
	// in the data dir, irrespective of what that address actually is
	store, err := etcd.New(etcdClient, service, podname)
	if err != nil {
		return err
	}
	defer store.Close()

	containerIpFound := store.FindByID(args.ContainerID, args.IfName)
	if containerIpFound == false {
		return fmt.Errorf("ipam-etcd: Failed to find address added by container %v", args.ContainerID)
	}
	return nil
}

func cmdAdd(args *skel.CmdArgs) error {
	log.Info("Cmd add start assign ip.")

	// step1, 解析本地配置
	lc, err := g.ParseLocalConf(args.StdinData)
	if err != nil {
		return err
	}

	// step2, 连接etcd
	etcdClient, err := g.ConnectEtcd(lc.Etcd)
	if err != nil {
		return err
	}
	defer etcdClient.Close()

	// step3, 获取service、pod
	service, podname := g.GetCurrentServiceAndPod(args.Args)
	if service == "" {
		return fmt.Errorf("Cmd add fetch service from args.Args: %s is failed.", args.Args)
	}

	// step4, 获取etcd中该服务的配置
	configBytes, err := g.GetConfigFromEtcd(etcdClient, service)
	if err != nil {
		return err
	}

	ipamConf, confVersion, err := allocator.LoadIPAMConfig(configBytes, args.Args)
	if err != nil {
		return err
	}

	result := &current.Result{}

	log.Infof("Cmd add is have dns resolve conf file, result: %s", ipamConf.ResolvConf)
	if ipamConf.ResolvConf != "" {
		dns, err := parseResolvConf(ipamConf.ResolvConf)
		if err != nil {
			return err
		}
		result.DNS = *dns
	}

	store, err := etcd.New(etcdClient, service, podname)
	if err != nil {
		return err
	}
	defer store.Close()

	// Keep the allocators we used, so we can release all IPs if an error
	// occurs after we start allocating
	allocs := []*allocator.IPAllocator{}

	// Store all requested IPs in a map, so we can easily remove ones we use
	// and error if some remain
	requestedIPs := map[string]net.IP{} //net.IP cannot be a key

	for _, ip := range ipamConf.IPArgs {
		requestedIPs[ip.String()] = ip
	}

	for idx, rangeset := range ipamConf.Ranges {
		allocator := allocator.NewIPAllocator(&rangeset, store, idx)

		// Check to see if there are any custom IPs requested in this range.
		var requestedIP net.IP
		for k, ip := range requestedIPs {
			if rangeset.Contains(ip) {
				requestedIP = ip
				delete(requestedIPs, k)
				break
			}
		}

		// NOTE: 将args.Args传给Get用于获取发布阶段
		ipConf, err := allocator.Get(args.ContainerID, args.IfName, args.Args, requestedIP)
		if err != nil {
			// Deallocate all already allocated IPs
			for _, alloc := range allocs {
				_ = alloc.Release(args.ContainerID, args.IfName)
			}
			return fmt.Errorf("failed to allocate for range %d: %v", idx, err)
		}

		allocs = append(allocs, allocator)

		result.IPs = append(result.IPs, ipConf)
	}
	log.Infof("Cmd add fetch finally result ips: %s", result.IPs)

	// If an IP was requested that wasn't fulfilled, fail
	if len(requestedIPs) != 0 {
		for _, alloc := range allocs {
			_ = alloc.Release(args.ContainerID, args.IfName)
		}
		errstr := "failed to allocate all requested IPs:"
		for _, ip := range requestedIPs {
			errstr = errstr + " " + ip.String()
		}
		return fmt.Errorf(errstr)
	}

	result.Routes = ipamConf.Routes

	return types.PrintResult(result, confVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	log.Info("Cmd del start delete ip.")

	// step1, 解析本地配置
	lc, err := g.ParseLocalConf(args.StdinData)
	if err != nil {
		return err
	}

	// step2, 连接etcd
	etcdClient, err := g.ConnectEtcd(lc.Etcd)
	if err != nil {
		return err
	}
	defer etcdClient.Close()

	// step3, 获取service、pod
	service, podname := g.GetCurrentServiceAndPod(args.Args)
	if service == "" {
		return fmt.Errorf("Cmd add fetch service from args.Args: %s is failed.", args.Args)
	}

	// step4, 获取etcd中该服务的配置
	configBytes, err := g.GetConfigFromEtcd(etcdClient, service)
	if err != nil {
		return err
	}

	ipamConf, _, err := allocator.LoadIPAMConfig(configBytes, args.Args)
	if err != nil {
		return err
	}

	store, err := etcd.New(etcdClient, service, podname)
	if err != nil {
		return err
	}
	defer store.Close()

	// Loop through all ranges, releasing all IPs, even if an error occurs
	var errors []string
	for idx, rangeset := range ipamConf.Ranges {
		ipAllocator := allocator.NewIPAllocator(&rangeset, store, idx)

		err := ipAllocator.Release(args.ContainerID, args.IfName)
		if err != nil {
			errors = append(errors, err.Error())
		}
	}

	if errors != nil {
		return fmt.Errorf(strings.Join(errors, ";"))
	}
	log.Infof("Cmd del release container: %s success.", args.ContainerID)
	return nil
}
