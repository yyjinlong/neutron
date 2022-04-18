neutron(macvlan + ipam)
-----------------------
yangjinlong

# neutron

* 基于plugins原码包(0.8.2) 进行修改

## macvlan

* 自动创建vlan对应的子网卡
* 整合ipam为一个插件

macvlan配置如下:
```bash
{
  "cniVersion":"0.3.1",
  "name": "neutron",
  "type": "neutron",
  "etcd": {
    "urls": "https://10.12.28.4:2379",
    "cafile": "/etc/etcd/ssl/etcd-ca.pem",
    "keyfile": "/etc/etcd/ssl/etcd-key.pem",
    "certfile": "/etc/etcd/ssl/etcd.pem"
  },
  "ipam": {
    "type": "ipam"
  }
}
```

参数说明:
* `name` (string, required): the name of the network
* `type` (string, required): "macvlan"
* `master` (string, optional): name of the host interface to enslave. Defaults to default route interace.
* `mode` (string, optional): one of "bridge", "private", "vepa", "passthru". Defaults to "bridge".
* `ipam` (dictionary, required): IPAM configuration to be used for this network. For interface only without ip address, create empty dictionary.

流程:
* 读取本地macvlan配置, 获取etcd配置, 连接etcd
* 根据CNI_ARGS获取发布阶段、服务名, 从etcd读取该服务的配置
* 获取该服务配置的master, 解析vlanid看是否需要自动创建vlan子网卡
* 创建macvlan
* 调用ipam, 返回需要的ip

## ipam

* 借鉴host-local原码
* 使用etcd存储每个服务配置
* 管理ip生命周期

服务配置如下:
```bash
{
  "cniVersion": "0.3.1",
  "name": "neutron",
  "type": "neutron",
  "master": "bond0.388",
  "ipam": {
    "type": "ipam",
    "ranges": [
      [
        {
          "rangeStart": "10.21.28.150",
          "rangeEnd": "10.21.28.160",
          "subnet": "10.21.28.0/24",
          "gateway": "10.21.28.1",
          "sandbox": [
            "10.21.28.150"
          ]
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
```

参数说明:
* `type` (string, required): "ipam".
* `routes` (string, optional): list of routes to add to the container namespace. Each route is a dictionary with "dst" and optional "gw" fields. If "gw" is omitted, value of "gateway" will be used.
* `resolvConf` (string, optional): Path to a `resolv.conf` on the host to parse and return as the DNS configuration
* `dataDir` (string, optional): Path to a directory to use for maintaining state, e.g. which IPs have been allocated to which containers
* `ranges`, (array, required, nonempty) an array of arrays of range objects:
	* `subnet` (string, required): CIDR block to allocate out of.
	* `rangeStart` (string, optional): IP inside of "subnet" from which to start allocating addresses. Defaults to ".2" IP inside of the "subnet" block.
	* `rangeEnd` (string, optional): IP inside of "subnet" with which to end allocating addresses. Defaults to ".254" IP inside of the "subnet" block for ipv4, ".255" for IPv6
	* `gateway` (string, optional): IP inside of "subnet" to designate as the gateway. Defaults to ".1" IP inside of the "subnet" block.

## 编译neutron, 并移动到/opt/cni/bin下

```bash
[root@jinlong neutron]# go build main.go -o neutron
[root@jinlong neutron]# mv neutron /opt/cni/bin/
```
