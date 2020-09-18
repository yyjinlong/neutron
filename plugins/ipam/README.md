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
