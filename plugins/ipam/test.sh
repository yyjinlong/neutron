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
