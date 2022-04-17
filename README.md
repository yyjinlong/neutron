# cni插件说明
* 基于plugins源码包(0.8.2) 进行修改
* cni选用macvlan插件, 并在其基础上修改
* ipam 选用host-local源码进行修改

## 采用etcd进行ip集中管理方案
### my-macvlan
* 使用etcd存储macvlan配置, 每个key代表一个服务.
* 根据CNI_ARGS获取服务名, 之后根据服务名从etcd读取配置.
* 用该服务的配置创建macvlan.

#### 编译macvlan, 并移动到/opt/cni/bin下
```bash
[root@jinlong cni]# cd plugins/macvlan/
[root@jinlong macvlan]# go build macvlan.go
[root@jinlong macvlan]# mv macvlan /opt/cni/bin/my-macvlan
```

### my-ipam
* 读取本地10-maclannet.conf.
* 基于Store接口, 用etcd进行实现.
* 获取etcd配置, 并将ip分配信息存储到etcd.
* 根据CNI_ARGS获取发布阶段.
* 判断获取的ip是否符合当前的发布阶段, 如果是则直接返回创建的ip.

#### 编译ipam, 并移动到/opt/cni/bin下
```bash
[root@jinlong cni]# cd plugins/ipam/
[root@jinlong ipam]# go build main.go dns.go
[root@jinlong ipam]# mv main /opt/cni/bin/my-ipam
```

### cni 本地macvlan插件配置举例
```json
/etc/cni/net.d/10-maclannet.conf
{
    "cniVersion":"0.3.1",
    "name": "macvlannet",
    "type": "my-macvlan",
    "etcd": {
        "urls": "https://127.0.0.1:2379",
        "cafile": "/etc/etcd/ssl/etcd-ca.pem",
        "keyfile": "/etc/etcd/ssl/etcd-key.pem",
        "certfile": "/etc/etcd/ssl/etcd.pem"
    },
    "ipam": {
        "type": "my-ipam"
    }
}
```

### etcd 存储服务配置举例
```json
{
  "cniVersion": "0.3.1",
  "name": "macvlannet",
  "type": "my-macvlan",
  "master": "bond0.444",
  "ipam": {
    "type": "my-ipam",
    "ranges": [
      [
        {
          "subnet": "172.132.28.0/24",
          "gateway": "172.132.28.1",
          "rangeStart": "172.132.28.150",
          "rangeEnd": "172.132.28.160",
          "sandbox": ["172.132.28.150"],
          "smallflow": ["172.132.28.151"],
          "routes": [
            {
              "dst": "0.0.0.0/0"
            }
          ]
        }
      ]
    ]
  }
}
```



# macvlan plugin

## Overview

[macvlan](http://backreference.org/2014/03/20/some-notes-on-macvlanmacvtap/) functions like a switch that is already connected to the host interface.
A host interface gets "enslaved" with the virtual interfaces sharing the physical device but having distinct MAC addresses.
Since each macvlan interface has its own MAC address, it makes it easy to use with existing DHCP servers already present on the network.

## Example configuration

```
{
	"cniVersion": "0.3.1",
	"name": "macvlannet",
	"type": "my-macvlan",
	"master": "bond0.444",
	"ipam": {
		"type": "my-ipam"
	}
}
```

## Network configuration reference

* `name` (string, required): the name of the network
* `type` (string, required): "macvlan"
* `master` (string, optional): name of the host interface to enslave. Defaults to default route interace.
* `mode` (string, optional): one of "bridge", "private", "vepa", "passthru". Defaults to "bridge".
* `ipam` (dictionary, required): IPAM configuration to be used for this network. For interface only without ip address, create empty dictionary.




# IPAM(IP address management plugin)

IPAM allocates IPv4 and IPv6 addresses out of a specified address range. Optionally,
it can include a DNS configuration from a `resolv.conf` file on the host.
改用etcd进行集中管理及存储

## Overview

IPAM plugin allocates ip addresses out of a set of address ranges.
It stores the state on the etcd;

The allocator can allocate multiple ranges, and supports sets of multiple (disjoint)
subnets. The allocation strategy is loosely round-robin within each range set.

## Example configurations

Note that the key `ranges` is a list of range sets. That is to say, the length
of the top-level array is the number of addresses returned. The second-level
array is a set of subnets to use as a pool of possible addresses.

This example configuration returns 2 IP addresses.

```json
{
	"ipam": {
		"type": "my-ipam",
		"ranges": [
			[
				{
					"subnet": "10.10.0.0/16",
					"rangeStart": "10.10.1.20",
					"rangeEnd": "10.10.1.40",
					"gateway": "10.10.1.1",
					"sandbox": ["10.10.1.20"],
					"smallflow": ["10.10.1.21"]
				},
				{
					"subnet": "172.16.5.0/24"
				}
			]
		],
		"routes": [
			{ "dst": "0.0.0.0/0" },
		],
		"dataDir": "/run/my-orchestrator/container-ipam-state"
	}
}
```

We can test it out on the command-line:

```bash
$ echo '{ "cniVersion": "0.3.1", "name": "my-macvlan", "ipam": { "type": "my-ipam", "ranges": [ [{"subnet": "203.0.113.0/24"}], [{"subnet": "2001:db8:1::/64"}]], "dataDir": "/tmp/cni-example"  } }' | CNI_COMMAND=ADD CNI_CONTAINERID=example CNI_NETNS=/dev/null CNI_IFNAME=dummy0 CNI_PATH=. go run main.go dns.go

```

## Network configuration reference

* `type` (string, required): "my-ipam".
* `routes` (string, optional): list of routes to add to the container namespace. Each route is a dictionary with "dst" and optional "gw" fields. If "gw" is omitted, value of "gateway" will be used.
* `resolvConf` (string, optional): Path to a `resolv.conf` on the host to parse and return as the DNS configuration
* `dataDir` (string, optional): Path to a directory to use for maintaining state, e.g. which IPs have been allocated to which containers
* `ranges`, (array, required, nonempty) an array of arrays of range objects:
	* `subnet` (string, required): CIDR block to allocate out of.
	* `rangeStart` (string, optional): IP inside of "subnet" from which to start allocating addresses. Defaults to ".2" IP inside of the "subnet" block.
	* `rangeEnd` (string, optional): IP inside of "subnet" with which to end allocating addresses. Defaults to ".254" IP inside of the "subnet" block for ipv4, ".255" for IPv6
	* `gateway` (string, optional): IP inside of "subnet" to designate as the gateway. Defaults to ".1" IP inside of the "subnet" block.




```
#!/bin/bash

testns="yyns"
infra_container="example"
pod="pay-10-sandbox-84f8cc5d4b-8v4fw"

ip netns add $testns

export CNI_NETNS=/var/run/netns/$testns

export CNI_PATH=.

export CNI_IFNAME=eth0
export CNI_CONTAINERID=$infra_container
export CNI_ARGS="IgnoreUnknown=1;K8S_POD_NAMESPACE=default;K8S_POD_NAME=$pod;K8S_POD_INFRA_CONTAINER_ID=$infra_container"

export CNI_COMMAND=ADD

# 调试运行
cat /etc/cni/net.d/10-maclannet.conf | go run main.go dns.go
```
