package g

import (
	"os"
	"fmt"
	"regexp"
	"strings"
	"encoding/json"

	log "github.com/sirupsen/logrus"

	"github.com/containernetworking/cni/pkg/types"
)

const (
	LOGPath = "/var/log/macvlan.log"
)

// 本机配置: 10-maclannet.conf
type LocalConf struct {
	types.NetConf
	Etcd *EtcdConf `json:"etcd"`
}

type EtcdConf struct {
	URLs     string `json:"urls"`
	CAFile   string `json:"cafile"`
	KeyFile  string `json:"keyfile"`
	CertFile string `json:"certfile"`
}

func init() {
	logFile, err := os.OpenFile(LOGPath, os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Open log file: %s failed: %s ", LOGPath, err)
	}
	log.SetFormatter(&log.TextFormatter{})
	log.SetOutput(logFile)
	log.SetLevel(log.InfoLevel)
}

func ParseLocalConf(bytes []byte) (*LocalConf, error) {
	/*
	 * 解析macvlan插件本地配置: /etc/cni/net.d/10-maclannet.conf
	 * 获取etcd地址、证书信息
	 */
	n := &LocalConf{}
	if err := json.Unmarshal(bytes, n); err != nil {
		return nil, fmt.Errorf("failed to load macvlan conf: %v", err)
	}
	return n, nil
}

func GetCurrentServiceAndPod(envArgs string) (string, string) {
	/*
	 *	根据CNI_ARGS获取服务名
	 */
	log.Infof("Get current service args from CNI_ARGS: %s", envArgs)
	pairs := strings.Split(envArgs, ";")
	for _, pair := range pairs {
		kv := strings.Split(pair, "=")
		keyString := kv[0]
		valString := kv[1]
		if keyString == "K8S_POD_NAME" {
			reg := regexp.MustCompile(`-\d+`)
			nameList := reg.Split(valString, -1)
			service := nameList[0]
			log.Infof("Get current service name is: %s", service)
			return service, valString
		}
	}
	return "", ""
}

func LoadEtcdConfig(envArgs string, etcd *EtcdConf) ([]byte, error) {
	/*
	 *	根据服务从etcd加载配置
	 */
	etcdClient, err := ConnectEtcd(etcd)
	if err != nil {
		return nil, err
	}
	defer etcdClient.Close()

	service, _ := GetCurrentServiceAndPod(envArgs)
	if service == "" {
		return nil, fmt.Errorf("fetch service from args.Args: %s is empty.", envArgs)
	}

	configBytes, err := GetConfigFromEtcd(etcdClient, service)
	return configBytes, err
}
