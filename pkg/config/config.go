package config

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/containernetworking/cni/pkg/types"
	types020 "github.com/containernetworking/cni/pkg/types/020"

	"neutron/pkg/etcd"
)

// LocalConf 基于types.NetConf扩展 添加etcd配置
type LocalConf struct {
	types.NetConf
	Etcd *etcd.EtcdConf `json:"etcd"`
}

// ReadLocalConf 解析macvlan插件本地配置: /etc/cni/net.d/10-maclannet.conf
func ReadLocalConf(std []byte) (*LocalConf, error) {
	/*
		{
			"cniVersion":"0.3.1",
			"name": "neutron",
			"type": "neutron",
			"etcd": {
				"urls": "https://127.0.0.1:2379",
				"cafile": "/etc/etcd/ssl/etcd-ca.pem",
				"keyfile": "/etc/etcd/ssl/etcd-key.pem",
				"certfile": "/etc/etcd/ssl/etcd.pem"
			}
		}
	*/
	var conf LocalConf
	if err := json.Unmarshal(std, &conf); err != nil {
		return nil, err
	}
	return &conf, nil
}

// NetConf 基于types.NetConf扩展 添加macvlan配置 添加整个ipam配置(NOTE: 来自host-local)
type NetConf struct {
	types.NetConf
	Master        string      `json:"master"` // macvlan网卡
	Mode          string      `json:"mode"`   // macvlan模式, 默认bridge
	MTU           int         `json:"mtu"`    // macvlan mtu值
	IPAM          *IPAMConfig `json:"ipam"`   // ipam 配置
	RuntimeConfig struct {    // The capability arg
		IPRanges []RangeSet `json:"ipRanges,omitempty"`
	} `json:"runtimeConfig,omitempty"`
	Args *struct {
		A *IPAMArgs `json:"cni"`
	} `json:"args"`
}

type IPAMConfig struct {
	*Range
	Name       string
	Type       string         `json:"type"`
	Routes     []*types.Route `json:"routes"`
	DataDir    string         `json:"dataDir"`
	ResolvConf string         `json:"resolvConf"`
	Ranges     []RangeSet     `json:"ranges"`
	IPArgs     []net.IP       `json:"-"` // Requested IPs from CNI_ARGS and args
}

type IPAMEnvArgs struct {
	types.CommonArgs
	IP net.IP `json:"ip,omitempty"`
}

type IPAMArgs struct {
	IPs []net.IP `json:"ips"`
}

type RangeSet []Range

type Range struct {
	RangeStart net.IP      `json:"rangeStart,omitempty"` // The first ip, inclusive
	RangeEnd   net.IP      `json:"rangeEnd,omitempty"`   // The last ip, inclusive
	Subnet     types.IPNet `json:"subnet"`               // cidr
	Gateway    net.IP      `json:"gateway,omitempty"`    // giteway
	Sandbox    []net.IP    `json:"sandbox,omitempty"`    // [ip]
}

// ReadTotalConf 将etcd中的完整配置转出对应结构
func ReadTotalConf(std []byte) (*NetConf, error) {
	/*
		{
		  "cniVersion": "0.3.1",
		  "name": "neutron",
		  "type": "neutron",
		  "master": "bond0.444",
		  "ipam": {
			"type": "ipam",
			"ranges": [
			  [
				{
				  "rangeStart": "172.132.28.150",
				  "rangeEnd": "172.132.28.160",
				  "subnet": "172.132.28.0/24",
				  "gateway": "172.132.28.1",
				  "sandbox": ["172.132.28.150"]
				}
			  ]
			],
			"routes": [
			  {
				"dst": "0.0.0.0/0"
			  }
			]
		  }
		}
	*/
	var conf NetConf
	if err := json.Unmarshal(std, &conf); err != nil {
		return nil, err
	}
	return &conf, nil
}

// NewIPAMConfig creates a NetworkConfig from the given network name.
func LoadIPAMConfig(n *NetConf, envArgs string) (*IPAMConfig, string, error) {
	if n.IPAM == nil {
		return nil, "", fmt.Errorf("IPAM config missing 'ipam' key")
	}

	// Parse custom IP from both env args *and* the top-level args config
	if envArgs != "" {
		e := IPAMEnvArgs{}
		err := types.LoadArgs(envArgs, &e)
		if err != nil {
			return nil, "", err
		}

		if e.IP != nil {
			n.IPAM.IPArgs = []net.IP{e.IP}
		}
	}

	if n.Args != nil && n.Args.A != nil && len(n.Args.A.IPs) != 0 {
		n.IPAM.IPArgs = append(n.IPAM.IPArgs, n.Args.A.IPs...)
	}

	for idx := range n.IPAM.IPArgs {
		if err := CanonicalizeIP(&n.IPAM.IPArgs[idx]); err != nil {
			return nil, "", fmt.Errorf("cannot understand ip: %v", err)
		}
	}

	// If a single range (old-style config) is specified, prepend it to
	// the Ranges array
	if n.IPAM.Range != nil && n.IPAM.Range.Subnet.IP != nil {
		n.IPAM.Ranges = append([]RangeSet{{*n.IPAM.Range}}, n.IPAM.Ranges...)
	}
	n.IPAM.Range = nil

	// If a range is supplied as a runtime config, prepend it to the Ranges
	if len(n.RuntimeConfig.IPRanges) > 0 {
		n.IPAM.Ranges = append(n.RuntimeConfig.IPRanges, n.IPAM.Ranges...)
	}

	if len(n.IPAM.Ranges) == 0 {
		return nil, "", fmt.Errorf("no IP ranges specified")
	}

	// Validate all ranges
	numV4 := 0
	numV6 := 0
	for i := range n.IPAM.Ranges {
		if err := n.IPAM.Ranges[i].Canonicalize(); err != nil {
			return nil, "", fmt.Errorf("invalid range set %d: %s", i, err)
		}

		if n.IPAM.Ranges[i][0].RangeStart.To4() != nil {
			numV4++
		} else {
			numV6++
		}
	}

	// CNI spec 0.2.0 and below supported only one v4 and v6 address
	if numV4 > 1 || numV6 > 1 {
		for _, v := range types020.SupportedVersions {
			if n.CNIVersion == v {
				return nil, "", fmt.Errorf("CNI version %v does not support more than 1 address per family", n.CNIVersion)
			}
		}
	}

	// Check for overlaps
	l := len(n.IPAM.Ranges)
	for i, p1 := range n.IPAM.Ranges[:l-1] {
		for j, p2 := range n.IPAM.Ranges[i+1:] {
			if p1.Overlaps(&p2) {
				return nil, "", fmt.Errorf("range set %d overlaps with %d", i, (i + j + 1))
			}
		}
	}

	// Copy net name into IPAM so not to drag Net struct around
	n.IPAM.Name = n.Name

	return n.IPAM, n.CNIVersion, nil
}
