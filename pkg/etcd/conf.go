package etcd

import (
	"context"
	"fmt"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/pkg/transport"

	"neutron/pkg/log"
	"neutron/pkg/util"
)

const (
	ETCD_BASE          = "/neutron"
	ETCD_SERVICE       = ETCD_BASE + "/service"
	ETCD_ENDPOINTS     = ETCD_BASE + "/endpoints"
	ETCD_LAST_RESERVED = ETCD_BASE + "/lastreserved"
	ETCD_LOCK          = ETCD_BASE + "/lock"
)

func GetServiceKey(service string) string {
	return fmt.Sprintf("%s/%s", ETCD_SERVICE, service)
}

func GetEndpointsKey(service string) string {
	return fmt.Sprintf("%s/%s", ETCD_ENDPOINTS, service)
}

func GetLastReservedKey(service string) string {
	return fmt.Sprintf("%s/%s", ETCD_LAST_RESERVED, service)
}

func GetLockKey(service string) string {
	return fmt.Sprintf("%s/%s", ETCD_LOCK, service)
}

func NewEtcdConf() *EtcdConf {
	return &EtcdConf{}
}

type EtcdConf struct {
	URLs     string `json:"urls"`
	CAFile   string `json:"cafile"`
	KeyFile  string `json:"keyfile"`
	CertFile string `json:"certfile"`
}

// Connect 连接etcd, 采用tls认证
func (ec *EtcdConf) Connect(urls, caFile, keyFile, certFile string) (*clientv3.Client, error) {
	tlsInfo := transport.TLSInfo{
		CertFile:      certFile,
		KeyFile:       keyFile,
		TrustedCAFile: caFile,
	}

	tlsConfig, err := tlsInfo.ClientConfig()
	if err != nil {
		return nil, err
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{urls},
		DialTimeout: 5 * time.Second,
		TLS:         tlsConfig,
	})
	return cli, err
}

// GetServiceConf 根据CNI_ARGS获取服务名, 根据服务名从etcd获取配置
func (ec *EtcdConf) GetServiceConf(etcdClient *clientv3.Client, envArgs string) ([]byte, error) {
	service, _ := util.GetCurrentServiceAndPod(envArgs)
	if service == "" {
		return nil, fmt.Errorf("fetch service from args.Args: %s is empty", envArgs)
	}

	return ec.GetConfigFromEtcd(etcdClient, service)
}

// GetConfigFromEtcd 从etcd中获取macvlan配置+ipam配置(真正的配置)
func (ec *EtcdConf) GetConfigFromEtcd(etcdClient *clientv3.Client, service string) ([]byte, error) {
	key := GetServiceKey(service)
	resp, err := etcdClient.Get(context.TODO(), key)
	if err != nil {
		return nil, err
	}
	if resp.Kvs == nil {
		log.Infof("Get key: %s from etcd is nil.", key)
		return nil, fmt.Errorf("resp.Kvs is nil")
	}
	value := resp.Kvs[0].Value
	log.Infof("Get key: %s from etcd value: %s", key, string(value))
	return value, nil
}
