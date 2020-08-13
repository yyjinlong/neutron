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
  "master": "bond0.444",
  "cniVersion": "0.3.1",
  "type": "my-macvlan",
  "name": "macvlannet",
  "ipam": {
    "ranges": [
      [
        {
          "subnet": "172.132.28.0/24",
          "gateway": "172.132.28.1",
          "rangeStart": "172.132.28.150",
          "rangeEnd": "172.132.28.160",
          "routes": [
            {
              "dst": "0.0.0.0/0"
            }
          ]
        }
      ]
    ],
    "type": "my-ipam"
  }
}
```
