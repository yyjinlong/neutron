package etcd

import (
	"os"
	"fmt"
	"net"
	"time"
	"context"
	"strconv"
	"strings"

	"github.com/coreos/etcd/clientv3"

	"cni/plugins/g"
	"cni/plugins/ipam/backend"
)


// Store采用etcd存储, 每个服务下的每个ip是一个key.
type Store struct {
	EtcdClient *clientv3.Client
	Endpoints []net.IP
	HostName string
	Service string
	PodName string
}

// Store implements the Store interface
var _ backend.Store = &Store{}

func New(etcdClient *clientv3.Client, service, podname string) (*Store, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	store := &Store {
		EtcdClient: etcdClient,
		HostName: hostname,
		Service: service,
		PodName: podname,
	}

	// 初始当前服务的所有endpoints
	endpoints, err := store.GetAllEndpoins()
	if err != nil {
		return nil, err
	}
	store.Endpoints = endpoints
	return store, nil
}

func (this *Store) Lock() error {
	const leaseTTL = 60
	var (
		err error
		leaseGrantResp *clientv3.LeaseGrantResponse
		txnResp *clientv3.TxnResponse
		getLock bool = false
	)

	key := g.GetEtcdLockKey(this.Service)

	kv := clientv3.NewKV(this.EtcdClient)

	for {
		// 申请一个租约
		lease := clientv3.NewLease(this.EtcdClient)
		if leaseGrantResp, err = lease.Grant(context.TODO(), leaseTTL); err != nil {
			return err
		}

		// 获取租约id
		leaseId := leaseGrantResp.ID

		// 创建一个事务
		txn := kv.Txn(context.TODO())
		txn.If(clientv3.Compare(clientv3.CreateRevision(key), "=", 0)).
			Then(clientv3.OpPut(key, strconv.FormatInt(int64(leaseId), 10), clientv3.WithLease(leaseId))).
			Else(clientv3.OpGet(key))

		// 提交事务
		if txnResp, err = txn.Commit(); err != nil {
			return err
		}
		if txnResp.Succeeded {
			getLock = true
			break
		} else {
			// try again
			time.Sleep(time.Millisecond * 100)
			continue
		}
	}

	if getLock {
		return nil
	} else {
		return fmt.Errorf("Can not get lock!")
	}
}

func (this *Store) Unlock() error {
	key := g.GetEtcdLockKey(this.Service)
	resp, err := this.EtcdClient.Get(context.TODO(), key)
	if err != nil {
		return err
	}
	if resp.Count > 0 {
		value := string(resp.Kvs[0].Value)
		leaseId, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		lease := clientv3.NewLease(this.EtcdClient)
		lease.Revoke(context.TODO(), clientv3.LeaseID(leaseId))
	}
	return nil
}

func (this *Store) Close() error {
	this.EtcdClient.Close()
	return nil
}

func (this *Store) Reserve(id string, ifname string, ip net.IP, rangeID string) (bool, error) {
	// key的格式: /ipam-etcd-cni/endpoints/pay/10.21.28.4
	key := fmt.Sprintf("%s/%s", g.GetEtcdEndpointsKey(this.Service), ip.String())
	resp, err := this.EtcdClient.Get(context.TODO(), key)
	if err != nil {
		return false, err
	}
	if resp.Count > 0 {
		return false, nil
	}

	value := fmt.Sprintf("%s:%s:%s", this.HostName, id, this.PodName)
	_, err = this.EtcdClient.Put(context.TODO(), key, value)
	if err != nil {
		return false, nil
	}

	// key的格式: /ipam-etcd-cni/lastreserved/pay/0
	key = fmt.Sprintf("%s/%s", g.GetEtcdLastReservedKey(this.Service), rangeID)
	_, err = this.EtcdClient.Put(context.TODO(), key, ip.String())
	if err != nil {
		return false, nil
	}
	return true, nil
}

// LastReservedIP returns the last reserved IP if exists
func (this *Store) LastReservedIP(rangeID string) (net.IP, error) {
	// key的格式: /ipam-etcd-cni/lastreserved/pay/0
	key := fmt.Sprintf("%s/%s", g.GetEtcdLastReservedKey(this.Service), rangeID)
	resp, err := this.EtcdClient.Get(context.TODO(), key)
	if err != nil {
		return nil, err
	}
	if resp.Count == 0 {
		return nil, fmt.Errorf("Can not find last reserved ip!")
	}
	data := string(resp.Kvs[0].Value)
	return net.ParseIP(data), nil
}

func (this *Store) Release(ip net.IP) error {
	// key的格式: /ipam-etcd-cni/endpoints/pay/10.21.28.4
	key := fmt.Sprintf("%s/%s", g.GetEtcdEndpointsKey(this.Service), ip.String())
	_, err := this.EtcdClient.Delete(context.TODO(), key)
	if err != nil {
		return err
	}
	return nil
}

// N.B. This function eats errors to be tolerant and
// release as much as possible
func (this *Store) ReleaseByID(id string, ifname string) error {
	/*
 	 * param id: container id
	 * param ifname: network interface name
	 */
	key := g.GetEtcdEndpointsKey(this.Service)
	resp, err := this.EtcdClient.Get(context.TODO(), key, clientv3.WithPrefix())
	if err != nil {
		return err
	}
	if resp.Count > 0 {
		for _, kv := range resp.Kvs {
			val := string(kv.Value)
			valList := strings.Split(val, ":")
			if len(valList) == 3 && valList[1] == id {
				_, err = this.EtcdClient.Delete(context.TODO(), string(kv.Key))
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// GetByID returns the IPs which have been allocated to the specific ID
func (this *Store) GetByID(id string, ifname string) []net.IP {
	/*
 	 * param id: container id
	 * param ifname: network interface name
	 */
	result := []net.IP{}
	key := g.GetEtcdEndpointsKey(this.Service)
	resp, err := this.EtcdClient.Get(context.TODO(), key, clientv3.WithPrefix())
	if err != nil {
		return nil
	}
	if resp.Count > 0 {
		for _, kv := range resp.Kvs {
			val := string(kv.Value)
			valList := strings.Split(val, ":")
			if len(valList) == 3 && valList[1] == id {
				curKey := string(kv.Key)
				keyInfo := strings.Split(curKey, "/")
				ip := keyInfo[len(keyInfo) - 1]
				result = append(result, net.ParseIP(ip))
				return result
			}
		}
	}
	return nil
}

func (this *Store) FindByID(id string, ifname string) bool {
	/*
 	 * param id: container id
	 * param ifname: network interface name
	 */
	key := g.GetEtcdEndpointsKey(this.Service)
	resp, err := this.EtcdClient.Get(context.TODO(), key, clientv3.WithPrefix())
	if err != nil {
		return false
	}
	if resp.Count > 0 {
		for _, kv := range resp.Kvs {
			val := string(kv.Value)
			valList := strings.Split(val, ":")
			if len(valList) == 3 && valList[1] == id {
				return true
			}
		}
	}
	return false
}

func (this *Store) GetAllEndpoins() ([]net.IP, error) {
	/*
	 * 获取当前服务所有的ip列表
	 */
	results := []net.IP{}
	key := g.GetEtcdEndpointsKey(this.Service)
	resp, err := this.EtcdClient.Get(context.TODO(), key, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	if resp.Count >0 {
		for _, kv := range resp.Kvs {
			curKey := string(kv.Value)
			keyInfo := strings.Split(curKey, "/")
			ip := keyInfo[len(keyInfo) - 1]
			results = append(results, net.ParseIP(ip))
		}
	}
	return results, nil
}

func (this *Store) IsIPExist(ip net.IP) bool {
	/*
	 * 判断当前获取的ip是否在已分配的ip列表里
	 */
	for _, v := range this.Endpoints {
		if v.Equal(ip) {
			return true
		}
	}
	return false
}
