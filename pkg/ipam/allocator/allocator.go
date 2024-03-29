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

package allocator

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/plugins/pkg/ip"

	"neutron/pkg/config"
	"neutron/pkg/etcd"
	"neutron/pkg/log"
)

func NewIPAllocator(s *config.RangeSet, store etcd.Storager, id int) *IPAllocator {
	return &IPAllocator{
		rangeset: s,
		store:    store,
		rangeID:  strconv.Itoa(id),
	}
}

type IPAllocator struct {
	rangeset *config.RangeSet
	store    etcd.Storager
	rangeID  string // Used for tracking last reserved ip
}

// 获取当前发布阶段: 沙盒、全流量 如: K8S_POD_NAME=pay-10-online-84f8cc5d4b-8v4fw
func (a *IPAllocator) getDeployStage(envArgs string) string {
	pairs := strings.Split(envArgs, ";")
	for _, pair := range pairs {
		kv := strings.Split(pair, "=")
		keyString := kv[0]
		valString := kv[1]
		if keyString == "K8S_POD_NAME" {
			reg := regexp.MustCompile(`-\d+-`)
			nameList := reg.Split(valString, -1)
			stageInfo := nameList[1]
			stageList := strings.Split(stageInfo, "-")
			stage := stageList[0]
			log.Infof("Get deploy stage is: %s", stage)
			return stage
		}
	}
	return ""
}

// Get allocates an IP
func (a *IPAllocator) Get(id string, ifname string, envArgs string, requestedIP net.IP) (*current.IPConfig, error) {
	a.store.Lock()
	defer a.store.Unlock()

	// 获取当前的分级发布阶段
	stage := a.getDeployStage(envArgs)
	if stage == "" {
		return nil, fmt.Errorf("Parse deploy stage is empty.")
	}
	log.Infof("Get allocates current deploy stage: %s", stage)

	var reservedIP *net.IPNet
	var gw net.IP

	log.Infof("Get allocates current requestedIP value: %s", requestedIP) // <nil>
	if requestedIP != nil {
		log.Infof("Get allocates requestedIP != nil")
		if err := config.CanonicalizeIP(&requestedIP); err != nil {
			return nil, err
		}

		r, err := a.rangeset.RangeFor(requestedIP)
		if err != nil {
			return nil, err
		}
		log.Infof("Get allocates range: %+v for requestedIP: %v", r, requestedIP)

		if requestedIP.Equal(r.Gateway) {
			return nil, fmt.Errorf("requested ip %s is subnet's gateway", requestedIP.String())
		}

		reserved, err := a.store.Reserve(id, ifname, requestedIP, a.rangeID)
		if err != nil {
			return nil, err
		}
		log.Infof("Get allocates reserve on etcd result: %+v", reserved)
		if !reserved {
			return nil, fmt.Errorf("requested IP address %s is not available in range set %s", requestedIP, a.rangeset.String())
		}
		reservedIP = &net.IPNet{IP: requestedIP, Mask: r.Subnet.Mask}
		gw = r.Gateway

	} else {
		log.Infof("Get allocates requestedIP == nil")
		// try to get allocated IPs for this given id, if exists, just return error
		// because duplicate allocation is not allowed in SPEC
		// https://github.com/containernetworking/cni/blob/master/SPEC.md
		allocatedIPs := a.store.GetByID(id, ifname)
		for _, allocatedIP := range allocatedIPs {
			// check whether the existing IP belong to this range set
			if _, err := a.rangeset.RangeFor(allocatedIP); err == nil {
				return nil, fmt.Errorf("%s has been allocated to %s, duplicate allocation is not allowed", allocatedIP.String(), id)
			}
		}
		log.Infof("Get allocates check container id: %s not duplicate allocation", id)

		iter, err := a.GetIter()
		if err != nil {
			return nil, err
		}
		log.Infof("Get allocates get iter: %+v", *iter)

		for {
			reservedIP, gw = iter.Next()
			if reservedIP == nil {
				break
			}
			log.Infof("Get allocates current stage: %s fetch ip: %+v will to match", stage, reservedIP)

			// NOTE: 判断当前获取到的ip, 是否匹配当前的分级发布阶段; 同时不在已分配的ip列表里
			if iter.matchDeployStageIP(stage, reservedIP.IP) && !a.store.IsIPExist(reservedIP.IP) {
				log.Infof("Stage: %s reserved ip: %s is matched", stage, reservedIP.IP)
				reserved, err := a.store.Reserve(id, ifname, reservedIP.IP, a.rangeID)
				if err != nil {
					return nil, err
				}
				log.Infof("Stage: %s reserved ip: %s reserved: %t", stage, reservedIP.IP, reserved)

				if reserved {
					break
				}
			}
		}
	}

	if reservedIP == nil {
		return nil, fmt.Errorf("no IP addresses available in range set: %s", a.rangeset.String())
	}
	version := "4"
	if reservedIP.IP.To4() == nil {
		version = "6"
	}

	return &current.IPConfig{
		Version: version,
		Address: *reservedIP,
		Gateway: gw,
	}, nil
}

// Release clears all IPs allocated for the container with given ID
func (a *IPAllocator) Release(id string, ifname string) error {
	a.store.Lock()
	defer a.store.Unlock()

	return a.store.ReleaseByID(id, ifname)
}

type RangeIter struct {
	rangeset *config.RangeSet

	// The current range id
	rangeIdx int

	// Our current position
	cur net.IP

	// The IP and range index where we started iterating; if we hit this again, we're done.
	startIP    net.IP
	startRange int
}

// GetIter encapsulates the strategy for this allocator.
// We use a round-robin strategy, attempting to evenly use the whole set.
// More specifically, a crash-looping container will not see the same IP until
// the entire range has been run through.
// We may wish to consider avoiding recently-released IPs in the future.
// 获取该rangeID下的遍历的起始ip、索引id
func (a *IPAllocator) GetIter() (*RangeIter, error) {
	iter := RangeIter{
		rangeset: a.rangeset,
	}

	// Round-robin by trying to allocate from the last reserved IP + 1
	startFromLastReservedIP := false

	// We might get a last reserved IP that is wrong if the range indexes changed.
	// This is not critical, we just lose round-robin this one time.
	lastReservedIP, err := a.store.LastReservedIP(a.rangeID)
	if err != nil && !os.IsNotExist(err) {
		log.Infof("Error retrieving last reserved ip: %v", err)
	} else if lastReservedIP != nil {
		startFromLastReservedIP = a.rangeset.Contains(lastReservedIP)
	}
	log.Infof("Get allocates startFromLastReservedIP: %t", startFromLastReservedIP)

	// Find the range in the set with this IP
	if startFromLastReservedIP {
		for i, r := range *a.rangeset {
			if r.Contains(lastReservedIP) {
				iter.rangeIdx = i
				iter.startRange = i

				// We advance the cursor on every Next(), so the first call
				// to next() will return lastReservedIP + 1
				iter.cur = lastReservedIP
				break
			}
		}
	} else {
		iter.rangeIdx = 0
		iter.startRange = 0
		iter.startIP = (*a.rangeset)[0].RangeStart
	}
	return &iter, nil
}

// Next returns the next IP, its mask, and its gateway. Returns nil
// if the iterator has been exhausted
func (i *RangeIter) Next() (*net.IPNet, net.IP) {
	r := (*i.rangeset)[i.rangeIdx]

	// If this is the first time iterating and we're not starting in the middle
	// of the range, then start at rangeStart, which is inclusive
	if i.cur == nil {
		i.cur = r.RangeStart
		i.startIP = i.cur
		if i.cur.Equal(r.Gateway) {
			return i.Next()
		}
		return &net.IPNet{IP: i.cur, Mask: r.Subnet.Mask}, r.Gateway
	}

	// If we've reached the end of this range, we need to advance the range
	// RangeEnd is inclusive as well
	if i.cur.Equal(r.RangeEnd) {
		i.rangeIdx += 1
		i.rangeIdx %= len(*i.rangeset)
		r = (*i.rangeset)[i.rangeIdx]

		i.cur = r.RangeStart
	} else {
		i.cur = ip.NextIP(i.cur)
	}

	if i.startIP == nil {
		i.startIP = i.cur
	} else if i.rangeIdx == i.startRange && i.cur.Equal(i.startIP) {
		// IF we've looped back to where we started, give up
		return nil, nil
	}

	if i.cur.Equal(r.Gateway) {
		return i.Next()
	}

	return &net.IPNet{IP: i.cur, Mask: r.Subnet.Mask}, r.Gateway
}

// 判断当前发布阶段, 获取的ip是否是在对应的列表里
func (i *RangeIter) matchDeployStageIP(stage string, ip net.IP) bool {
	r := (*i.rangeset)[i.rangeIdx]
	sandboxList := r.Sandbox
	if stage == "sandbox" {
		if i.in(ip, sandboxList) {
			return true
		}
	} else {
		// 全流量阶段, 如果获取到的ip是属于沙盒、小流量的应该返回false
		if i.in(ip, sandboxList) {
			return false
		}
		return true
	}
	return false
}

// 判断一个ip是否在一个ip列表里
func (i *RangeIter) in(ip net.IP, ipList []net.IP) bool {
	if ip == nil || len(ipList) == 0 {
		return false
	}
	for _, curIp := range ipList {
		if ip.Equal(curIp) {
			return true
		}
	}
	return false
}
