// designer_apply_provision.go：架构设计器落地——逐节点供给（网络 / 虚拟机 / 容器栈）。
// provisionNode 按节点类型分发到 provisionNet（网络，同名幂等跳过）/ provisionVM（虚拟机，
// 走 create_vm→app_install→ansible_run 任务管线编排）/ 容器栈部署；resolveVMNetwork 解析
// VM 相连的网络节点。全量落地（Apply）与按差异收敛（Reconcile）共用本文件的落地逻辑。
package handler

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/jiuzhao/vmops/service/tasks"
	"github.com/jiuzhao/vmops/service/virt"
)

// provisionNode 落地单个节点（net/vm/container）。原 Apply 内联循环抽出，供全量落地
// 与按差异收敛共用；本身对已存在的网络幂等（provisionNet 同名跳过）。
func (h *DesignerHandler) provisionNode(p *dsgPlan, n dsgNode, st *dsgApplyState, userID *uint, username string) error {
	switch n.Kind {
	case "container":
		st.Steps = append(st.Steps, "部署栈 "+n.Ref+"（"+n.Name+"）…")
		meta, sraw, err := parseStackMeta(filepath.Join(stacksDir(), n.Ref+".yml"))
		if err != nil {
			return fmt.Errorf("栈不存在：%s", n.Ref)
		}
		target := config.DataPath("stacks", n.Ref)
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("创建部署目录失败: %w", err)
		}
		_ = os.WriteFile(filepath.Join(target, "docker-compose.yml"), sraw, 0o644)
		if err := h.Docker.ComposeUpFromFile(target); err != nil {
			return fmt.Errorf("栈 %s 部署失败: %w", n.Ref, err)
		}
		st.Steps = append(st.Steps, "✓ "+n.Ref+" 完成（"+meta.Desc+"）")
	case "vm":
		vmID, err := h.provisionVM(p, n, st, userID, username)
		if err != nil {
			return err
		}
		st.Steps = append(st.Steps, "✓ VM "+n.Name+" 就绪（id="+strconv.FormatUint(uint64(vmID), 10)+"）")
	case "net":
		created, nerr := h.provisionNet(n)
		if nerr != nil {
			return nerr
		}
		if created {
			st.Steps = append(st.Steps, "✓ 网络 "+n.Name+" 已创建（NAT）")
		} else {
			st.Steps = append(st.Steps, "· 网络 "+n.Name+" 已存在，跳过")
		}
	default:
		st.Steps = append(st.Steps, "跳过 "+n.Name+"（未知节点类型）")
	}
	return nil
}

// provisionNet 网络节点落地（D3）：libvirt 已存在同名网络则跳过（不覆盖用户网络），
// 否则按默认 NAT 模板定义并启动。节点 ref 若形如网段（10.10.0.0/24），取其第一个
// 可用地址作网关（当前模板固定 /24 掩码，不支持更宽网段）。
func (h *DesignerHandler) provisionNet(n dsgNode) (bool, error) {
	if h.Virt == nil {
		return false, fmt.Errorf("虚拟化服务不可用")
	}
	if !validateVMName(n.Name) {
		return false, fmt.Errorf("网络名 %q 非法（字母数字与下划线/连字符）", n.Name)
	}
	if nets, err := h.Virt.ListNetworks(); err == nil {
		for _, e := range nets {
			if e.Name == n.Name {
				return false, nil // 已存在：不覆盖
			}
		}
	}
	xml := virt.NetworkXMLFromParams(n.Name, "", netGatewayFromCIDR(n.Ref))
	if xml == "" {
		return false, fmt.Errorf("生成网络定义失败")
	}
	if err := h.Virt.DefineNetwork(xml); err != nil {
		return false, err
	}
	return true, nil
}

// netGatewayFromCIDR 若是 CIDR（如 10.10.0.0/24）返回其第一个可用地址作网关，否则空串。
func netGatewayFromCIDR(s string) string {
	_, ipnet, err := net.ParseCIDR(strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	ip := ipnet.IP.To4()
	if ip == nil {
		return ""
	}
	next := net.IPv4(ip[0], ip[1], ip[2], ip[3]+1)
	if !ipnet.Contains(next) {
		return ""
	}
	return next.String()
}

// resolveVMNetwork 找与本 VM 相连的 net 节点，返回其节点名（= libvirt 网络名，provisionNet 即按此建网）；
// 无 net 连线时回落 "default"。
func resolveVMNetwork(p *dsgPlan, n dsgNode) string {
	if p == nil {
		return "default"
	}
	netName := make(map[string]string)
	for _, nd := range p.Nodes {
		if nd.Kind == "net" && nd.Name != "" {
			netName[nd.ID] = nd.Name
		}
	}
	for _, l := range p.Links {
		if l.From == n.ID {
			if name, ok := netName[l.To]; ok {
				return name
			}
		}
		if l.To == n.ID {
			if name, ok := netName[l.From]; ok {
				return name
			}
		}
	}
	return "default"
}

// provisionVM VM 节点落地：create_vm 建机（云镜像 + cloud-init + 设计网络 + provision 块）→
// 由 create_vm executor 统一完成「开机 → 等 IP → 托管凭据」→ 本函数再逐个装应用 / 跑 playbook。
// create_vm / app_install / ansible_run 均复用既有 executor（与页面操作同一套链路），本函数只做编排。
// userID/username 由调用方（goroutine 起手时）从 HTTP 上下文取定传入，不再读共享 handler 字段。
func (h *DesignerHandler) provisionVM(p *dsgPlan, n dsgNode, st *dsgApplyState, userID *uint, username string) (uint, error) {
	// 前置校验：云镜像必须存在于镜像库（images 表按 id）。
	// n.Ref 来自计划文件（用户可控的字符串），必须先解析成 uint 再交给 GORM——
	// 直传非纯数字字符串会被 GORM 当原始 SQL 片段拼进 WHERE（注入面，见 AGENTS 第 8 条）。
	imgID, ok := parseID(n.Ref)
	if !ok {
		return 0, fmt.Errorf("云镜像 ID 非法（%s）", n.Ref)
	}
	var img model.Image
	if err := h.DB.First(&img, imgID).Error; err != nil {
		return 0, fmt.Errorf("云镜像不存在（id=%s）", n.Ref)
	}
	pool := n.Pool
	if pool == "" {
		pool = tasks.DefaultStoragePoolResolver()
	}
	vcpu := n.VCPU
	if vcpu <= 0 {
		vcpu = 1
	}
	mem := n.MemoryMB
	if mem <= 0 {
		mem = 1024
	}
	sshUser := n.SSHUser
	if sshUser == "" {
		sshUser = "root"
	}

	// 1) 提交 create_vm（payload 与镜像管理页「基于云镜像创建」同构：source_image_id 引用不拷贝）。
	// 网络接画布上与本 VM 相连的 net 节点（无连线回落 default）；口令以密文进 provision 块，
	// 由 executor 解密后做 cloud-init seed 并托管到 vm_credentials——payload/日志不留明文。
	var host model.Host
	if err := h.DB.First(&host).Error; err != nil {
		return 0, fmt.Errorf("请先登记宿主机")
	}
	ciSpec := &virt.CloudInitSpec{Hostname: n.Name, User: sshUser, Password: n.SSHSecret}
	if verr := validateCloudInitText(ciSpec); verr != nil {
		return 0, fmt.Errorf("VM 节点 %s 的 cloud-init 配置不合法: %w", n.Name, verr)
	}
	netName := resolveVMNetwork(p, n)
	prov := map[string]interface{}{"start": true, "wait_ip": true, "ssh_user": sshUser}
	if n.SSHSecret != "" {
		cipherB64, saltHex, err := secretbox.SealWithMaster(h.VMCred.MasterSecret, n.SSHSecret)
		if err != nil {
			return 0, fmt.Errorf("SSH 口令加密失败: %w", err)
		}
		prov["password_enc"] = cipherB64
		prov["salt"] = saltHex
	}
	createPayload := map[string]interface{}{
		"name":         n.Name,
		"host_id":      host.ID,
		"storage_pool": pool,
		"vcpu":         vcpu,
		"memory_mb":    mem,
		"disks":        []map[string]interface{}{{"source_image_id": img.ID, "create_gb": int(img.SizeGB)}},
		"interfaces":   []map[string]interface{}{{"type": "network", "source": netName, "model": "virtio"}},
		"network":      netName,
		"cloud_init":   ciSpec,
		"provision":    prov,
	}
	task, err := h.Tasks.Submit("create_vm", "设计器创建 VM "+n.Name, createPayload, userID, username, n.Name, nil)
	if err != nil {
		return 0, fmt.Errorf("提交建机任务失败: %w", err)
	}
	st.Steps = append(st.Steps, "建机任务已提交（task="+strconv.FormatUint(uint64(task.ID), 10)+"），等待建机+初始化…")
	// 建机任务已含「等 IP」，窗口放宽到 12 分钟
	result, err := h.waitTask(task.ID, 12*time.Minute)
	if err != nil {
		return 0, err
	}
	// create_vm 成功结果 {vm_id: N}
	var res struct {
		VMID uint `json:"vm_id"`
	}
	_ = json.Unmarshal([]byte(result), &res)
	vmID := res.VMID
	if vmID == 0 {
		return 0, fmt.Errorf("建机任务未返回 vm_id")
	}
	var vm model.VM
	if err := h.DB.First(&vm, vmID).Error; err != nil {
		return 0, fmt.Errorf("读取虚拟机记录失败: %w", err)
	}
	st.Steps = append(st.Steps, "VM 运行中，IP="+vm.IP+"（凭据已自动托管）")
	if vmID == 0 {
		return 0, fmt.Errorf("建机任务成功但未返回 vm_id")
	}

	// 2) app_install 逐个装（口令边界就地加密；凭据不落明文）
	for _, appID := range n.Apps {
		cipherB64, saltHex, err := secretbox.SealWithMaster(h.VMCred.MasterSecret, n.SSHSecret)
		if err != nil {
			return vmID, fmt.Errorf("SSH 口令加密失败: %w", err)
		}
		payload := map[string]interface{}{
			"app_id":       appID,
			"vm_id":        vmID,
			"host":         vm.IP,
			"port":         22,
			"user":         sshUser,
			"password_enc": cipherB64,
			"salt":         saltHex,
		}
		t, err := h.Tasks.Submit("app_install", "设计器安装 "+appID+" 到 "+n.Name, payload, userID, username, n.Name, &vmID)
		if err != nil {
			return vmID, fmt.Errorf("提交应用安装失败（%s）: %w", appID, err)
		}
		st.Steps = append(st.Steps, "安装 "+appID+"…（task="+strconv.FormatUint(uint64(t.ID), 10)+"）")
		if _, err := h.waitTask(t.ID, 15*time.Minute); err != nil {
			return vmID, err
		}
		st.Steps = append(st.Steps, "✓ "+appID+" 安装完成")
	}

	// 5) playbook 逐个跑（P4-S3 编排联动）：复用 ansible_run 任务管线（凭据解析/
	// inventory 生成/RECAP 全在 executor），这里只编排。playbook 跑的是初始化/加固/
	// 优化类动作，与应用安装互补
	for _, pbID := range n.Playbooks {
		if !playbookIDRe.MatchString(pbID) {
			return vmID, fmt.Errorf("playbook ID 非法: %s", pbID)
		}
		if _, perr := os.Stat(config.DataPath("ansible", "playbooks", pbID+".yml")); perr != nil {
			return vmID, fmt.Errorf("playbook 不存在: %s", pbID)
		}
		t, err := h.Tasks.Submit("ansible_run", "设计器 playbook "+pbID+" → "+n.Name,
			map[string]interface{}{"targets": []uint{vmID}, "playbook": pbID}, userID, username, n.Name, &vmID)
		if err != nil {
			return vmID, fmt.Errorf("提交 playbook 执行失败（%s）: %w", pbID, err)
		}
		st.Steps = append(st.Steps, "执行 playbook "+pbID+"…（task="+strconv.FormatUint(uint64(t.ID), 10)+"）")
		if _, err := h.waitTask(t.ID, 25*time.Minute); err != nil {
			return vmID, err
		}
		st.Steps = append(st.Steps, "✓ "+pbID+" 完成")
	}
	return vmID, nil
}
