package util

import (
	"regexp"
	"strings"

	"neutron/pkg/log"
)

// GetCurrentServiceAndPod 根据CNI_ARGS获取服务名
func GetCurrentServiceAndPod(envArgs string) (string, string) {
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
