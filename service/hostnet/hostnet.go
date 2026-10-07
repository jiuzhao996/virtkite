// Package hostnet 读取宿主机网络状态（ip -br addr / ip neigh / bridge link），
// 供网络拓扑与 IPAM 视图复用；命令均免 root。
package hostnet

import (
	"os"
	"os/exec"
	"strings"
)

// NIC 宿主机网络接口。
type NIC struct {
	Name      string   `json:"name"`
	State     string   `json:"state"`     // UP / DOWN / UNKNOWN
	Addresses []string `json:"addresses"` // CIDR 列表
	Physical  bool     `json:"physical"`  // /sys/class/net/<name>/device 存在即物理设备
}

// Interfaces 枚举宿主机接口（对应 ip -br addr）。
func Interfaces() ([]NIC, error) {
	out, err := exec.Command("ip", "-br", "addr").Output()
	if err != nil {
		return nil, err
	}
	return parseInterfaces(string(out), isPhysical), nil
}

// parseInterfaces 解析 `ip -br addr` 输出（每行：名称 状态 [地址…]；名称可能带 @ifN）。
func parseInterfaces(out string, physical func(string) bool) []NIC {
	list := []NIC{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if i := strings.IndexByte(name, '@'); i > 0 {
			name = name[:i]
		}
		n := NIC{Name: name, State: fields[1]}
		if len(fields) > 2 {
			n.Addresses = append(n.Addresses, fields[2:]...)
		}
		if physical != nil {
			n.Physical = physical(name)
		}
		list = append(list, n)
	}
	return list
}

// isPhysical 物理设备在 /sys/class/net/<name>/device 有软链；虚拟桥/隧道没有。
func isPhysical(name string) bool {
	_, err := os.Stat("/sys/class/net/" + name + "/device")
	return err == nil
}

// Neighbor 邻居表条目（ip neigh），用于「最近有通信」活跃标注。
type Neighbor struct {
	IP    string `json:"ip"`
	Dev   string `json:"dev"`
	MAC   string `json:"mac"`
	State string `json:"state"` // REACHABLE / STALE / DELAY / PROBE / FAILED
}

// Neighbors 读取邻居表（对应 ip neigh）。
func Neighbors() ([]Neighbor, error) {
	out, err := exec.Command("ip", "neigh").Output()
	if err != nil {
		return nil, err
	}
	return parseNeighbors(string(out)), nil
}

// parseNeighbors 解析 `ip neigh`：`10.0.0.5 dev virbr0 lladdr 52:54:.. REACHABLE`。
func parseNeighbors(out string) []Neighbor {
	list := []Neighbor{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		n := Neighbor{IP: f[0]}
		for i := 1; i < len(f); i++ {
			switch f[i] {
			case "dev":
				if i+1 < len(f) {
					n.Dev = f[i+1]
				}
			case "lladdr":
				if i+1 < len(f) {
					n.MAC = strings.ToLower(f[i+1])
				}
			}
		}
		n.State = f[len(f)-1]
		list = append(list, n)
	}
	return list
}

// BridgeLink 桥接从属关系（bridge link）。
type BridgeLink struct {
	Slave  string `json:"slave"`  // vethXXX / vnetX / 物理口
	Master string `json:"master"` // docker0 / br-xxx / virbr0
}

// BridgeLinks 读取桥上挂接的接口（对应 bridge link）；bridge 命令缺失时由调用方忽略错误。
func BridgeLinks() ([]BridgeLink, error) {
	out, err := exec.Command("bridge", "link").Output()
	if err != nil {
		return nil, err
	}
	return parseBridgeLinks(string(out)), nil
}

// parseBridgeLinks 解析 `bridge link`：`5: veth1@if2: <...> master docker0 state forwarding`。
func parseBridgeLinks(out string) []BridgeLink {
	list := []BridgeLink{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		slave := strings.TrimSuffix(f[1], ":")
		if i := strings.IndexByte(slave, '@'); i > 0 {
			slave = slave[:i]
		}
		bl := BridgeLink{Slave: slave}
		for i := 2; i < len(f)-1; i++ {
			if f[i] == "master" {
				bl.Master = f[i+1]
			}
		}
		if bl.Master != "" {
			list = append(list, bl)
		}
	}
	return list
}
