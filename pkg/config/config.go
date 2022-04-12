package config

import (
	"encoding/json"
	"net"

	"github.com/containernetworking/cni/pkg/types"

	"neutron/pkg/etcd"
)

// LocalConf 基于types.NetConf 扩展添加etcd配置
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

type NetConf struct {
	types.NetConf
	Master string      `json:"master"`
	IPAM   *IPAMConfig `json:"ipam"`
}

type IPAMConfig struct {
	Type   string         `json:"type"`
	Ranges []RangeSet     `json:"ranges"`
	Routes []*types.Route `json:"routes"`
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
