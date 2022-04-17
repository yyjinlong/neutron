#!/bin/bash

ns="yyns"
infra_container="jinlong"
pod="pay-10-online-84f8cc5d4b-8v4fw"

ip netns add $ns

export CNI_NETNS=/var/run/netns/$ns

export CNI_IFNAME=bond0
export CNI_PATH=.

export CNI_CONTAINERID=$infra_container
export CNI_ARGS="IgnoreUnknown=1;K8S_POD_NAMESPACE=default;K8S_POD_NAME=$pod;K8S_POD_INFRA_CONTAINER_ID=$infra_container"

export CNI_COMMAND=ADD

# 本机macvlan配置如下:
#[root@dx-kvm00 neutron]# cat /etc/cni/net.d/10-maclannet.conf
#{
#    "cniVersion":"0.3.1",
#    "name": "neutron",
#    "type": "neutron",
#    "etcd": {
#        "urls": "https://10.12.28.4:2379",
#        "cafile": "/etc/etcd/ssl/etcd-ca.pem",
#        "keyfile": "/etc/etcd/ssl/etcd-key.pem",
#        "certfile": "/etc/etcd/ssl/etcd.pem"
#    },
#    "ipam": {
#        "type": "ipam"
#    }
#}

# etcd对应服务的key设置
#[root@dx-k8smaster00 ~]# myetcdctl put /neutron/service/pay '{"type": "neutron", "cniVersion": "0.3.1", "master": "bond0.388", "name": "neutron", "ipam": {"ranges": [[{"subnet": "10.21.28.0/24", "sandbox": ["10.21.28.150"], "gateway": "10.21.28.1", "rangeEnd": "10.21.28.160", "rangeStart": "10.21.28.150"}]], "routes": [{"dst": "0.0.0.0/0"}], "type": "ipam"}}'

# 调试运行
go run main.go < /etc/cni/net.d/10-maclannet.conf
