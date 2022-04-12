package etcd

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/etcd/clientv3"
)

// Store implements the Store interface
var _ Storager = &Store{}

func New(etcdClient *clientv3.Client, service, podname string) (*Store, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	store := &Store{
		EtcdClient: etcdClient,
		HostName:   hostname,
		Service:    service,
		PodName:    podname,
	}

	// 初始当前服务的所有endpoints
	endpoints, err := store.GetAllEndpoins()
	if err != nil {
		return nil, err
	}
	store.Endpoints = endpoints
	return store, nil
}

// Store 采用etcd存储, 每个服务下的每个ip是一个key.
type Store struct {
	EtcdClient *clientv3.Client
	Endpoints  []net.IP
	HostName   string
	Service    string
	PodName    string
}

func (s *Store) Lock() error {
	var (
		leaseTTL = 60
		getLock  = false
		key      = GetLockKey(s.Service)
	)

	kv := clientv3.NewKV(s.EtcdClient)
	for {
		// 申请一个租约
		lease := clientv3.NewLease(s.EtcdClient)
		leaseGrantResp, err := lease.Grant(context.TODO(), leaseTTL)
		if err != nil {
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
		txnResp, err := txn.Commit()
		if err != nil {
			return err
		}
		if txnResp.Succeeded {
			getLock = true
			break
		}

		// try again
		time.Sleep(time.Millisecond * 100)
	}

	if getLock {
		return nil
	}
	return fmt.Errorf("Can not get lock")
}

func (s *Store) Unlock() error {
	key := GetLockKey(s.Service)
	resp, err := s.EtcdClient.Get(context.TODO(), key)
	if err != nil {
		return err
	}
	if resp.Count > 0 {
		value := string(resp.Kvs[0].Value)
		leaseId, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		lease := clientv3.NewLease(s.EtcdClient)
		lease.Revoke(context.TODO(), clientv3.LeaseID(leaseId))
	}
	return nil
}

func (s *Store) Close() error {
	s.EtcdClient.Close()
	return nil
}

func (s *Store) Reserve(id string, ifname string, ip net.IP, rangeID string) (bool, error) {
	// key的格式: /neutron/endpoints/pay/10.21.28.4
	key := fmt.Sprintf("%s/%s", GetEndpointsKey(s.Service), ip.String())
	resp, err := s.EtcdClient.Get(context.TODO(), key)
	if err != nil {
		return false, err
	}
	if resp.Count > 0 {
		return false, nil
	}

	value := fmt.Sprintf("%s:%s:%s", s.HostName, id, s.PodName)
	if _, err := s.EtcdClient.Put(context.TODO(), key, value); err != nil {
		return false, nil
	}

	// key的格式: /neutron/lastreserved/pay/0
	key = fmt.Sprintf("%s/%s", GetLastReservedKey(s.Service), rangeID)
	if _, err := s.EtcdClient.Put(context.TODO(), key, ip.String()); err != nil {
		return false, nil
	}
	return true, nil
}

// LastReservedIP returns the last reserved IP if exists
func (s *Store) LastReservedIP(rangeID string) (net.IP, error) {
	// key的格式: /neutron/lastreserved/pay/0
	key := fmt.Sprintf("%s/%s", GetLastReservedKey(s.Service), rangeID)
	resp, err := s.EtcdClient.Get(context.TODO(), key)
	if err != nil {
		return nil, err
	}
	if resp.Count == 0 {
		return nil, fmt.Errorf("Can not find last reserved ip!")
	}
	data := string(resp.Kvs[0].Value)
	return net.ParseIP(data), nil
}

func (s *Store) Release(ip net.IP) error {
	// key的格式: /neutron/endpoints/pay/10.21.28.4
	key := fmt.Sprintf("%s/%s", GetEndpointsKey(s.Service), ip.String())
	if _, err := s.EtcdClient.Delete(context.TODO(), key); err != nil {
		return err
	}
	return nil
}

// ReleaseByID This function eats errors to be tolerant and release as much as possible
func (s *Store) ReleaseByID(id string, ifname string) error {
	/*
	 * param id: container id
	 * param ifname: network interface name
	 */
	key := GetEndpointsKey(s.Service)
	resp, err := s.EtcdClient.Get(context.TODO(), key, clientv3.WithPrefix())
	if err != nil {
		return err
	}
	if resp.Count > 0 {
		for _, kv := range resp.Kvs {
			val := string(kv.Value)
			valList := strings.Split(val, ":")
			if len(valList) == 3 && valList[1] == id {
				if _, err := s.EtcdClient.Delete(context.TODO(), string(kv.Key)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// GetByID returns the IPs which have been allocated to the specific ID
func (s *Store) GetByID(id string, ifname string) []net.IP {
	/*
	 * param id: container id
	 * param ifname: network interface name
	 */
	result := []net.IP{}
	key := GetEndpointsKey(s.Service)
	resp, err := s.EtcdClient.Get(context.TODO(), key, clientv3.WithPrefix())
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
				ip := keyInfo[len(keyInfo)-1]
				result = append(result, net.ParseIP(ip))
				return result
			}
		}
	}
	return nil
}

func (s *Store) FindByID(id string, ifname string) bool {
	/*
	 * param id: container id
	 * param ifname: network interface name
	 */
	key := GetEndpointsKey(s.Service)
	resp, err := s.EtcdClient.Get(context.TODO(), key, clientv3.WithPrefix())
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

// GetAllEndpoins 获取当前服务所有的ip列表
func (s *Store) GetAllEndpoins() ([]net.IP, error) {
	results := []net.IP{}
	key := GetEndpointsKey(s.Service)
	resp, err := s.EtcdClient.Get(context.TODO(), key, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	if resp.Count > 0 {
		for _, kv := range resp.Kvs {
			curKey := string(kv.Value)
			keyInfo := strings.Split(curKey, "/")
			ip := keyInfo[len(keyInfo)-1]
			results = append(results, net.ParseIP(ip))
		}
	}
	return results, nil
}

// IsIPExist 判断当前获取的ip是否在已分配的ip列表里
func (s *Store) IsIPExist(ip net.IP) bool {
	for _, v := range s.Endpoints {
		if v.Equal(ip) {
			return true
		}
	}
	return false
}
