// 本文件负责 docker 网络维度的操作：网络列表、网段/网关增强信息、创建/删除自定义网络
// （含驱动白名单与内置网络删除守卫），以及网络拓扑（网段 + 挂接容器）视图。
package dockerx

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
)

// Network 网络列表条目（docker network ls --format '{{json .}}'，IPv6/Internal 为 "true"/"false" 字符串）。
type Network struct {
	Name      string `json:"Name"`
	Driver    string `json:"Driver"`
	Scope     string `json:"Scope"`
	IPv6      string `json:"IPv6"`
	Internal  string `json:"Internal"`
	CreatedAt string `json:"CreatedAt"`
}

// Networks 返回 docker 网络列表（等价 docker network ls）。
func (d *Dockerx) Networks() ([]Network, error) {
	out, err := run("network", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, fmt.Errorf("获取网络列表失败: %w", err)
	}
	list := []Network{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var n Network
		if err := jsonUnmarshal(line, &n); err != nil {
			continue // 跳过无法解析行，不让单行脏数据毁掉整表
		}
		list = append(list, n)
	}
	return list, nil
}

// NetworkDetail 单网络的增强信息（IPAM 网段/网关，列表页卡片展示用）。
type NetworkDetail struct {
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway"`
}

// NetworkDetails 批量 inspect 多个网络（docker network inspect 支持多名称一次调用），
// 返回 name → {subnet, gateway}。host/none 等无 IPAM 的网络天然缺失该键，调用方按缺省处理。
// 供网络页卡片展示网段/网关（与 libvirt 虚拟网络卡片信息对齐）。
func (d *Dockerx) NetworkDetails(names []string) (map[string]NetworkDetail, error) {
	out := map[string]NetworkDetail{}
	if len(names) == 0 {
		return out, nil
	}
	raw, err := run(append([]string{"network", "inspect"}, names...)...)
	if err != nil {
		return nil, fmt.Errorf("检查网络详情失败: %w", err)
	}
	var arr []struct {
		Name string `json:"Name"`
		IPAM struct {
			Config []struct {
				Subnet  string `json:"Subnet"`
				Gateway string `json:"Gateway"`
			} `json:"Config"`
		} `json:"IPAM"`
	}
	if err := jsonUnmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("解析网络详情失败: %w", err)
	}
	for _, n := range arr {
		detail := NetworkDetail{}
		if len(n.IPAM.Config) > 0 {
			detail.Subnet = n.IPAM.Config[0].Subnet
			detail.Gateway = n.IPAM.Config[0].Gateway
		}
		out[n.Name] = detail
	}
	return out, nil
}

// networkDrivers docker network create -d 的驱动白名单。
var networkDrivers = map[string]bool{
	"bridge":  true,
	"overlay": true,
	"macvlan": true,
	"ipvlan":  true,
	"host":    true,
	"none":    true,
}

// builtinNetworks docker 内置网络黑名单——删除一律拒绝（与 libvirt 删卷守卫同一立场：
// 宁可删不掉，不可弄坏宿主底座）。docker_gwbridge 是 overlay 的隐藏网关桥，同样不可删。
var builtinNetworks = map[string]bool{
	"bridge":          true,
	"host":            true,
	"none":            true,
	"docker_gwbridge": true,
}

// CreateNetwork 创建自定义网络（等价 docker network create）。subnet/gateway 可空，
// 提供时必须分别是合法 CIDR 与 IP——数组参数拼接本无注入风险，这里挡的是明显误输入。
func (d *Dockerx) CreateNetwork(name, driver, subnet, gateway string) error {
	if !safeDockerName(name) {
		return errors.New("网络名非法（仅允许字母数字与 -_.）")
	}
	if driver == "" {
		driver = "bridge"
	}
	if !networkDrivers[driver] {
		return errors.New("网络驱动不支持（bridge/overlay/macvlan/ipvlan/host/none）")
	}
	if subnet != "" {
		if _, _, err := net.ParseCIDR(subnet); err != nil {
			return errors.New("subnet 必须是合法 CIDR，如 172.30.0.0/16")
		}
	}
	if gateway != "" && net.ParseIP(gateway) == nil {
		return errors.New("gateway 必须是合法 IP 地址")
	}
	args := []string{"network", "create", "-d", driver}
	if subnet != "" {
		args = append(args, "--subnet", subnet)
	}
	if gateway != "" {
		args = append(args, "--gateway", gateway)
	}
	if _, err := run(append(args, name)...); err != nil {
		return fmt.Errorf("创建网络失败: %w", err)
	}
	return nil
}

// RemoveNetwork 删除自定义网络（等价 docker network rm），内置网络直接拒绝。
func (d *Dockerx) RemoveNetwork(name string) error {
	if !safeDockerName(name) {
		return errors.New("网络名非法（仅允许字母数字与 -_.）")
	}
	if builtinNetworks[name] {
		return errors.New("内置网络不允许删除")
	}
	if _, err := run("network", "rm", name); err != nil {
		return fmt.Errorf("删除网络失败: %w", err)
	}
	return nil
}

// NetworkContainer docker 网络内的容器挂接（network inspect 的 Containers 项）。
type NetworkContainer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	IPv4 string `json:"ipv4"`
	MAC  string `json:"mac"`
}

// NetworkTopo docker 网络拓扑要素（网段 + 挂接容器）。
type NetworkTopo struct {
	Name       string             `json:"name"`
	Driver     string             `json:"driver"`
	Subnet     string             `json:"subnet"`
	Gateway    string             `json:"gateway"`
	Builtin    bool               `json:"builtin"`
	Containers []NetworkContainer `json:"containers"`
}

// NetworkTopology 批量 inspect 全部 docker 网络，返回网段与挂接容器（网络拓扑视图用）。
// 容器挂接取自 network inspect 的 Containers——仅运行中/暂停容器有活跃端点，停止容器不再挂接，
// 与「网络拓扑只画当前真实连接」的语义一致。
func (d *Dockerx) NetworkTopology() ([]NetworkTopo, error) {
	nets, err := d.Networks()
	if err != nil {
		return nil, err
	}
	if len(nets) == 0 {
		return []NetworkTopo{}, nil
	}
	names := make([]string, 0, len(nets))
	for _, n := range nets {
		names = append(names, n.Name)
	}
	raw, err := run(append([]string{"network", "inspect"}, names...)...)
	if err != nil {
		return nil, fmt.Errorf("检查网络失败: %w", err)
	}
	return parseNetworkTopo(raw)
}

// NetworkDetail 单个 docker 网络详情（网段 + 挂接容器）。
func (d *Dockerx) NetworkDetail(name string) (*NetworkTopo, error) {
	raw, err := run("network", "inspect", name)
	if err != nil {
		return nil, fmt.Errorf("检查网络失败: %w", err)
	}
	arr, err := parseNetworkTopo(raw)
	if err != nil {
		return nil, err
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("网络不存在: %s", name)
	}
	return &arr[0], nil
}

// parseNetworkTopo 解析 `docker network inspect` 的 JSON 输出（单网络或多网络数组）。
func parseNetworkTopo(raw string) ([]NetworkTopo, error) {
	var arr []struct {
		Name   string `json:"Name"`
		Driver string `json:"Driver"`
		IPAM   struct {
			Config []struct {
				Subnet  string `json:"Subnet"`
				Gateway string `json:"Gateway"`
			} `json:"Config"`
		} `json:"IPAM"`
		Containers map[string]struct {
			Name        string `json:"Name"`
			MacAddress  string `json:"MacAddress"`
			IPv4Address string `json:"IPv4Address"`
		} `json:"Containers"`
	}
	if err := jsonUnmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("解析网络详情失败: %w", err)
	}
	out := make([]NetworkTopo, 0, len(arr))
	for _, n := range arr {
		t := NetworkTopo{Name: n.Name, Driver: n.Driver, Builtin: builtinNetworks[n.Name]}
		if len(n.IPAM.Config) > 0 {
			t.Subnet = n.IPAM.Config[0].Subnet
			t.Gateway = n.IPAM.Config[0].Gateway
		}
		ids := make([]string, 0, len(n.Containers))
		for id := range n.Containers {
			ids = append(ids, id)
		}
		sort.Strings(ids) // 输出稳定，便于前端 diff/对比
		for _, id := range ids {
			c := n.Containers[id]
			ip := c.IPv4Address
			if i := strings.IndexByte(ip, '/'); i > 0 {
				ip = ip[:i]
			}
			t.Containers = append(t.Containers, NetworkContainer{
				ID: id, Name: c.Name, IPv4: ip, MAC: strings.ToLower(c.MacAddress),
			})
		}
		out = append(out, t)
	}
	return out, nil
}
