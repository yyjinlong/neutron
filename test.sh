#!/bin/bash

ns="yyns"
infra_container="jinlong"
pod="pay-10-online-84f8cc5d4b-8v4fw"

ip netns add $ns

export CNI_COMMAND=ADD
export CNI_NETNS=/var/run/netns/$ns

export CNI_IFNAME=eth0
export CNI_PATH=/opt/cni/bin/

export CNI_CONTAINERID=$infra_container
export CNI_ARGS="IgnoreUnknown=1;K8S_POD_NAMESPACE=default;K8S_POD_NAME=$pod;K8S_POD_INFRA_CONTAINER_ID=$infra_container"

# TODO: 修改插件名称
#[root@m6-kvm26 ~]# cd /opt/cni/bin/
#[root@m6-kvm26 bin]# cp macvlan my-macvlan
#[root@m6-kvm26 bin]# cp host-local my-ipam

# TODO: 本机10-maclannet.conf配置如下:
#[root@jinlong macvlan]# cat /etc/cni/net.d/10-maclannet.conf
#{
#    "cniVersion":"0.3.1",
#    "name": "macvlannet",
#    "type": "my-macvlan",
#    "etcd": {
#        "urls": "https://10.21.23.7:2379",
#        "cafile": "/etc/etcd/ssl/etcd-ca.pem",
#        "keyfile": "/etc/etcd/ssl/etcd-key.pem",
#        "certfile": "/etc/etcd/ssl/etcd.pem"
#    },
#    "ipam": {
#        "type": "my-ipam"
#    }
#}

# TODO: etcd对应服务的key设置
#[root@jinlong macvlan]# myetcdctl put /ipam-etcd-cni/service/pay '{"master": "bond0.388", "cniVersion": "0.3.1", "type": "my-macvlan", "name": "macvlannet", "ipam": {"ranges": [[{"subnet": "10.21.28.0/24", "gateway": "10.21.28.1", "rangeStart": "10.21.28.150", "rangeEnd": "10.21.28.160", "sandbox": ["10.21.28.152"], "smallflow": ["10.21.28.153"], "routes": [{"dst": "0.0.0.0/0"}]}]], "type": "my-ipam"}}'

# 调试运行
go run macvlan.go < /etc/cni/net.d/10-maclannet.conf
