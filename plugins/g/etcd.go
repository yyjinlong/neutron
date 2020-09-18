package g

import (
	"fmt"
	"time"
	"context"

	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/pkg/transport"

	log "github.com/sirupsen/logrus"
)

const (
	ETCDBasePrefix         = "/ipam-etcd-cni"
	ETCDServicePrefix      = ETCDBasePrefix + "/service"
	ETCDLockPrefix         = ETCDBasePrefix + "/lock"
	ETCDEndpointsPrefix    = ETCDBasePrefix + "/endpoints"
	ETCDLastReservedPrefix = ETCDBasePrefix + "/lastreserved"
)

func ConnectEtcd(ec *EtcdConf) (*clientv3.Client, error) {
	/*
	 * 连接etcd, 采用tls认证
	 */
	tlsInfo := transport.TLSInfo{
		CertFile:      ec.CertFile,
		KeyFile:       ec.KeyFile,
		TrustedCAFile: ec.CAFile,
	}

	tlsConfig, err := tlsInfo.ClientConfig()
	if err != nil {
		return nil, err
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{ec.URLs},
		DialTimeout: 5 * time.Second,
		TLS:         tlsConfig,
	})
	return cli, err
}

func GetEtcdEndpointsKey(service string) string {
	return fmt.Sprintf("%s/%s", ETCDEndpointsPrefix, service)
}

func GetEtcdLastReservedKey(service string) string {
	return fmt.Sprintf("%s/%s", ETCDLastReservedPrefix, service)
}

func GetEtcdLockKey(service string) string {
	return fmt.Sprintf("%s/%s", ETCDLockPrefix, service)
}

func GetEtcdServiceKey(service string) string {
	return fmt.Sprintf("%s/%s", ETCDServicePrefix, service)
}

func GetConfigFromEtcd(etcdClient *clientv3.Client, service string) ([]byte, error) {
	/*
	 * 从etcd中获取macvlan配置+ipam配置(真正的配置)
	 */
	key := GetEtcdServiceKey(service)
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
