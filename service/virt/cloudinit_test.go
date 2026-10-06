package virt

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// ISO9660（ECMA-119）里几个用于校验的固定偏移。
// 全部按标准写死，而不是从被测库里反查常量——否则库改了行为测试也跟着"改口径"，
// 就验证不了「产出的镜像确实符合 cloud-init 认得的格式」这件事。
const (
	isoSectorSize = 2048          // ECMA-119 逻辑块大小
	isoSystemArea = 16 * 2048     // 前 16 个扇区是系统区（0..32767），必须全 0
	isoPVDOffset  = isoSystemArea // 主卷描述符（PVD）在第 16 扇区，绝对偏移 0x8000
	// 卷标识符（Volume Identifier）在 PVD 内偏移 40、长 32 字节 → 绝对偏移 0x8028。
	// cloud-init 就是靠这 32 字节等于 cidata 来认出 seed 设备的。
	isoVolumeIDOffset = isoPVDOffset + 40
	isoVolumeIDLen    = 32
	// 根目录记录（Directory Record for Root Directory）在 PVD 内偏移 156、长 34 字节。
	isoRootRecordOffset = isoPVDOffset + 156
	isoRootRecordLen    = 34
	// 目录记录内部字段偏移（ECMA-119 9.1）
	isoRecLenOff    = 0  // 本条记录总长度
	isoRecExtentOff = 2  // 数据区起始逻辑块号（LSB 在前，4 字节）
	isoRecSizeOff   = 10 // 数据区字节长度（LSB 在前，4 字节）
	isoRecFlagsOff  = 25 // 文件标志位，bit1 置位表示目录
	isoRecIDLenOff  = 32 // 文件标识符长度
	isoRecIDOff     = 33 // 文件标识符起始
	isoFlagDir      = 0x02
)

// seedEntry ISO 根目录下的一个条目。
type seedEntry struct {
	// Ident 目录记录里的原始文件标识符，带 ISO9660 版本后缀（如 user-data;1）。
	Ident string
	// Name 去掉版本后缀后的文件名，也就是 Linux isofs 驱动挂载后 cloud-init 看到的名字。
	Name string
	// Content 该条目数据区的原始字节（按目录记录里的长度精确截取，不含扇区填充）。
	Content string
}

// seedISO 解析结果。
type seedISO struct {
	Label   string            // 卷标，0x8028 起 32 字节右侧空格已裁
	Entries []seedEntry       // 根目录全部普通文件条目（已排除 . 与 ..）
	Files   map[string]string // Name → Content，便于按名取内容
}

// parseSeedISO 手写的最小 ISO9660 解析器：只做本用例需要的三件事——
// 读 PVD 拿卷标、顺着 PVD 里的根目录记录定位根目录扇区、遍历目录记录取出各文件内容。
//
// 刻意不用 iso9660.OpenImage：写镜像的是同一个库，用它自己的 reader 读回来
// 只能证明「自己写自己能读」，证明不了字节布局真的符合标准。这里全部按
// ECMA-119 的固定偏移硬解，qemu/内核 isofs/cloud-init 看到的就是这些字节。
func parseSeedISO(t *testing.T, data []byte) seedISO {
	t.Helper()

	if len(data) < isoPVDOffset+isoSectorSize {
		t.Fatalf("镜像太短，连主卷描述符都放不下：期望至少 %d 字节，实际 %d 字节",
			isoPVDOffset+isoSectorSize, len(data))
	}

	root := data[isoRootRecordOffset : isoRootRecordOffset+isoRootRecordLen]
	if got := root[isoRecLenOff]; got != isoRootRecordLen {
		t.Fatalf("根目录记录长度期望 %d，实际 %d", isoRootRecordLen, got)
	}
	if root[isoRecFlagsOff]&isoFlagDir == 0 {
		t.Fatalf("根目录记录的目录标志位未置位：flags=0x%02x", root[isoRecFlagsOff])
	}

	rootLBA := binary.LittleEndian.Uint32(root[isoRecExtentOff : isoRecExtentOff+4])
	rootSize := binary.LittleEndian.Uint32(root[isoRecSizeOff : isoRecSizeOff+4])
	rootStart, rootEnd := int(rootLBA)*isoSectorSize, int(rootLBA)*isoSectorSize+int(rootSize)
	if rootEnd > len(data) {
		t.Fatalf("根目录数据区越界：需要 [%d,%d)，镜像只有 %d 字节", rootStart, rootEnd, len(data))
	}

	out := seedISO{
		Label: strings.TrimRight(string(data[isoVolumeIDOffset:isoVolumeIDOffset+isoVolumeIDLen]), " "),
		Files: map[string]string{},
	}

	dir := data[rootStart:rootEnd]
	for off := 0; off < len(dir); {
		recLen := int(dir[off+isoRecLenOff])
		if recLen == 0 {
			// 长度 0 表示本扇区剩余部分是填充，跳到下一个扇区边界继续
			next := (off/isoSectorSize + 1) * isoSectorSize
			if next >= len(dir) {
				break
			}
			off = next
			continue
		}
		if off+recLen > len(dir) {
			t.Fatalf("目录记录跨越了根目录数据区末尾：off=%d recLen=%d 区长=%d", off, recLen, len(dir))
		}
		rec := dir[off : off+recLen]
		off += recLen

		if rec[isoRecFlagsOff]&isoFlagDir != 0 {
			continue // 跳过 .（标识符 0x00）与 ..（标识符 0x01）
		}
		idLen := int(rec[isoRecIDLenOff])
		ident := string(rec[isoRecIDOff : isoRecIDOff+idLen])

		lba := binary.LittleEndian.Uint32(rec[isoRecExtentOff : isoRecExtentOff+4])
		size := binary.LittleEndian.Uint32(rec[isoRecSizeOff : isoRecSizeOff+4])
		start, end := int(lba)*isoSectorSize, int(lba)*isoSectorSize+int(size)
		if end > len(data) {
			t.Fatalf("文件 %q 的数据区越界：需要 [%d,%d)，镜像只有 %d 字节", ident, start, end, len(data))
		}

		e := seedEntry{
			Ident:   ident,
			Name:    strings.SplitN(ident, ";", 2)[0],
			Content: string(data[start:end]),
		}
		out.Entries = append(out.Entries, e)
		out.Files[e.Name] = e.Content
	}
	return out
}

// TestGenerateSeedISOVolumeLayout 验证产出的字节流真的是一张卷标为 cidata 的 ISO9660 镜像，
// 且根目录下恰好是 cloud-init NoCloud 数据源要求的三个文件。
//
// 风险点：这三样（ISO9660 骨架 / cidata 卷标 / 文件名）任一不对，虚拟机都能正常开机，
// 只是 cloud-init 静默不生效——用户名密码没设上、SSH key 没写进去、静态 IP 没配上，
// 表现为"新建的机器登不进去"，而后端日志一片正常，是最难排查的一类故障。
// 因此这里不看库的返回值，直接按 ECMA-119 固定偏移校验字节：
// 0x8000 描述符类型 = 1、0x8001 起 5 字节 = "CD001"、0x8028 起 32 字节 = "cidata" 右填空格。
// 文件名则从根目录的目录记录里逐条解出来，而不是在整个字节流里 strings.Contains
// （后者会把 user-data 的正文内容误当成文件名匹配上）。
func TestGenerateSeedISOVolumeLayout(t *testing.T) {
	tests := []struct {
		name string
		spec *CloudInitSpec
	}{
		{
			name: "完整配置（用户名+密码+SSH key+静态 IP）",
			spec: &CloudInitSpec{
				Hostname: "web-01", User: "ubuntu", Password: "p@ss",
				SSHKey:  "ssh-ed25519 AAAAC3Nz test@vmops",
				NetMode: "static", IP: "192.168.100.50", Gateway: "192.168.100.1",
				DNS: []string{"223.5.5.5", "8.8.8.8"},
			},
		},
		{name: "nil 配置（调用方未勾选 cloud-init）", spec: nil},
		{name: "零值配置（勾选了但什么都没填）", spec: &CloudInitSpec{}},
		{
			name: "中文主机名与中文口令（UTF-8 多字节，不能把长度算错）",
			spec: &CloudInitSpec{Hostname: "测试主机", User: "运维", Password: "口令口令"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := GenerateSeedISO(tt.spec)
			if err != nil {
				t.Fatalf("生成 seed ISO 失败: %v", err)
			}

			// 长度：非空、按扇区对齐、至少要有 16 系统区 + PVD + 终止符 + 根目录 + 3 个文件
			if len(data) == 0 {
				t.Fatal("产出字节数期望 > 0，实际为 0（空镜像会让 qemu 挂载失败）")
			}
			if len(data)%isoSectorSize != 0 {
				t.Errorf("镜像长度期望是 %d 的整数倍，实际 %d 字节（余 %d）",
					isoSectorSize, len(data), len(data)%isoSectorSize)
			}
			if minSectors := 22; len(data) < minSectors*isoSectorSize {
				t.Errorf("镜像长度期望至少 %d 字节（16 系统区 + PVD + 终止符 + 根目录 + 3 文件），实际 %d 字节",
					minSectors*isoSectorSize, len(data))
			}

			// 系统区（前 16 扇区）必须全 0：非 0 说明写进了引导记录之类的东西
			for i, b := range data[:isoSystemArea] {
				if b != 0 {
					t.Fatalf("系统区第 %d 字节期望 0x00，实际 0x%02x（前 %d 字节应全 0）", i, b, isoSystemArea)
				}
			}

			// PVD 头：类型 1 + "CD001" + 版本 1
			if got := data[isoPVDOffset]; got != 1 {
				t.Errorf("0x%X 处卷描述符类型期望 1（主卷描述符），实际 %d", isoPVDOffset, got)
			}
			if got := string(data[isoPVDOffset+1 : isoPVDOffset+6]); got != "CD001" {
				t.Errorf("0x%X 起 5 字节标准标识期望 \"CD001\"，实际 %q", isoPVDOffset+1, got)
			}
			if got := data[isoPVDOffset+6]; got != 1 {
				t.Errorf("0x%X 处描述符版本期望 1，实际 %d", isoPVDOffset+6, got)
			}

			// 卷标：严格比对 32 字节原始内容（"cidata" + 26 个空格），
			// 不只比 TrimRight 后的结果——右侧必须是空格填充而不是 0x00，
			// 否则部分 isofs 实现读出来的标签会带尾随控制字符而匹配不上。
			const wantRaw = "cidata                          "
			gotRaw := string(data[isoVolumeIDOffset : isoVolumeIDOffset+isoVolumeIDLen])
			if len(wantRaw) != isoVolumeIDLen {
				t.Fatalf("用例自身写错了：期望卷标常量应为 %d 字节，实际 %d 字节", isoVolumeIDLen, len(wantRaw))
			}
			if gotRaw != wantRaw {
				t.Errorf("0x%X 起 %d 字节卷标期望 %q，实际 %q",
					isoVolumeIDOffset, isoVolumeIDLen, wantRaw, gotRaw)
			}

			// 卷描述符集终止符（类型 255）紧跟在 PVD 之后的第 17 扇区
			termOff := isoPVDOffset + isoSectorSize
			if got := data[termOff]; got != 0xFF {
				t.Errorf("0x%X 处期望卷描述符集终止符（类型 255），实际 %d", termOff, got)
			}
			if got := string(data[termOff+1 : termOff+6]); got != "CD001" {
				t.Errorf("0x%X 起 5 字节期望 \"CD001\"，实际 %q", termOff+1, got)
			}

			// 根目录条目：恰好三个文件，名字与顺序都按标识符解出来
			iso := parseSeedISO(t, data)
			if iso.Label != "cidata" {
				t.Errorf("解析出的卷标期望 \"cidata\"，实际 %q", iso.Label)
			}
			if len(iso.Entries) != 3 {
				t.Fatalf("根目录普通文件数期望 3，实际 %d（条目：%v）", len(iso.Entries), iso.Entries)
			}
			for _, want := range []string{"user-data", "meta-data", "network-config"} {
				content, ok := iso.Files[want]
				if !ok {
					t.Errorf("根目录缺少文件 %q（现有：%v）", want, entryIdents(iso))
					continue
				}
				if content == "" {
					t.Errorf("文件 %q 内容为空", want)
				}
			}
			// 文件标识符必须带 ISO9660 版本后缀 ";1"：这是 ECMA-119 的要求，
			// Linux isofs 挂载时会自动去掉，cloud-init 看到的仍是 user-data。
			// 若哪天库改成不写版本号，这里会先报出来。
			for _, e := range iso.Entries {
				if !strings.HasSuffix(e.Ident, ";1") {
					t.Errorf("文件标识符 %q 期望以 \";1\" 结尾（ISO9660 版本后缀）", e.Ident)
				}
				if e.Name != strings.ToLower(e.Name) {
					t.Errorf("文件名 %q 含大写字母，cloud-init 只认小写的 user-data/meta-data", e.Name)
				}
			}
		})
	}
}

// entryIdents 收集条目标识符，只用于断言失败时打印现场。
func entryIdents(iso seedISO) []string {
	out := make([]string, 0, len(iso.Entries))
	for _, e := range iso.Entries {
		out = append(out, e.Ident)
	}
	return out
}

// TestGenerateSeedISOUserData 逐字节锁定 user-data 的内容。
//
// 风险点：user-data 是唯一决定"新机器能不能登进去"的文件——多一个空格、少一个
// 换行、key 拼错，cloud-init 都会整段解析失败并静默跳过（虚拟机照样起来，只是
// 没账号）。所以这里不做"包含用户名"这种弱断言，而是整串精确比对。
//
// 生成已改为 yaml.Marshal 结构化序列化（goccy）：缩进与引号由库负责，需要引号的
// 值（纯数字口令等）会被正确包起来。user 与 password 是独立分支（原「与」关系
// 只填其一时两个都丢，已修复）：仅密码 → 设到镜像默认用户；仅用户名 → 指定
// 默认用户名，公钥装到该用户名下。
func TestGenerateSeedISOUserData(t *testing.T) {
	tests := []struct {
		name string
		spec *CloudInitSpec
		want string
	}{
		{
			name: "用户名+密码+SSH key 三者齐全",
			spec: &CloudInitSpec{
				Hostname: "web-01", User: "ubuntu", Password: "p@ss",
				SSHKey: "ssh-ed25519 AAAAC3Nz test@vmops",
			},
			want: "#cloud-config\n" +
				"user: ubuntu\n" +
				"password: p@ss\n" +
				"chpasswd:\n" +
				"  expire: false\n" +
				"ssh_pwauth: true\n" +
				"ssh_authorized_keys:\n" +
				"- ssh-ed25519 AAAAC3Nz test@vmops\n" +
				"hostname: web-01\n",
		},
		{
			name: "只有用户名+密码，没有 SSH key（root 口令登录场景）",
			spec: &CloudInitSpec{Hostname: "db-01", User: "root", Password: "123456"},
			// root 口令登录额外注入 PermitRootLogin 放开（P2B v2）：Rocky/RHEL sshd
			// 默认 prohibit-password，只开 ssh_pwauth 时 root 仍被拒。
			// 纯数字口令被库自动加引号（裸写会解析成整数，cloud-init 收到 float64）
			want: "#cloud-config\n" +
				"user: root\n" +
				"password: \"123456\"\n" +
				"chpasswd:\n" +
				"  expire: false\n" +
				"ssh_pwauth: true\n" +
				"write_files:\n" +
				"- path: /etc/ssh/sshd_config.d/40-enable-root-login.conf\n" +
				"  content: PermitRootLogin yes\n" +
				"runcmd:\n" +
				"- - sed\n" +
				"  - -i\n" +
				"  - \"s/^#\\\\?PermitRootLogin.*/PermitRootLogin yes/\"\n" +
				"  - /etc/ssh/sshd_config\n" +
				"- - systemctl\n" +
				"  - restart\n" +
				"  - sshd\n" +
				"hostname: db-01\n",
		},
		{
			name: "只有 SSH key，没有密码（免密登录场景）",
			spec: &CloudInitSpec{Hostname: "ci-01", SSHKey: "ssh-rsa AAAAB3Nza ci@runner"},
			want: "#cloud-config\n" +
				"ssh_authorized_keys:\n" +
				"- ssh-rsa AAAAB3Nza ci@runner\n" +
				"hostname: ci-01\n",
		},
		{
			name: "有用户名+SSH key，无密码：user 独立生效，ssh_pwauth 不打开",
			spec: &CloudInitSpec{Hostname: "ci-02", User: "ubuntu", SSHKey: "ssh-rsa KEY"},
			want: "#cloud-config\n" +
				"user: ubuntu\n" +
				"ssh_authorized_keys:\n" +
				"- ssh-rsa KEY\n" +
				"hostname: ci-02\n",
		},
		{
			name: "只填密码不填用户名：密码设到镜像默认用户",
			spec: &CloudInitSpec{Hostname: "h1", Password: "onlypass"},
			want: "#cloud-config\n" +
				"password: onlypass\n" +
				"chpasswd:\n" +
				"  expire: false\n" +
				"ssh_pwauth: true\n" +
				"hostname: h1\n",
		},
		{
			name: "零值配置：只有 #cloud-config 头与默认主机名 vmops",
			spec: &CloudInitSpec{},
			want: "#cloud-config\nhostname: vmops\n",
		},
		{
			name: "nil 配置：与零值配置产出一致",
			spec: nil,
			want: "#cloud-config\nhostname: vmops\n",
		},
		{
			name: "中文用户名与中文口令（UTF-8 原样写入，不做转码）",
			spec: &CloudInitSpec{Hostname: "测试主机", User: "运维", Password: "口令"},
			want: "#cloud-config\n" +
				"user: 运维\n" +
				"password: 口令\n" +
				"chpasswd:\n" +
				"  expire: false\n" +
				"ssh_pwauth: true\n" +
				"hostname: 测试主机\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := GenerateSeedISO(tt.spec)
			if err != nil {
				t.Fatalf("生成 seed ISO 失败: %v", err)
			}
			got := parseSeedISO(t, data).Files["user-data"]
			if got != tt.want {
				t.Errorf("user-data 内容不符\n期望:\n%s\n实际:\n%s", tt.want, got)
			}
		})
	}
}

// TestGenerateSeedISOMetaData 锁定 meta-data 的内容与主机名兜底逻辑。
//
// 风险点：instance-id 是 cloud-init 判断"要不要重新执行初始化"的唯一依据。
// 它由主机名拼出来（instance-id: vmops-<hostname>），因此主机名一变、
// instance-id 就变，克隆出来的机器才会重新跑一次初始化；反过来，若这里
// 退化成固定字符串，克隆机会认为自己已经初始化过而跳过全部配置。
func TestGenerateSeedISOMetaData(t *testing.T) {
	tests := []struct {
		name string
		spec *CloudInitSpec
		want string
	}{
		{
			name: "指定主机名",
			spec: &CloudInitSpec{Hostname: "web-01"},
			want: "instance-id: vmops-web-01\nlocal-hostname: web-01\n",
		},
		{
			name: "主机名为空时兜底为 vmops",
			spec: &CloudInitSpec{User: "ubuntu", Password: "p"},
			want: "instance-id: vmops-vmops\nlocal-hostname: vmops\n",
		},
		{
			name: "nil 配置同样兜底为 vmops",
			spec: nil,
			want: "instance-id: vmops-vmops\nlocal-hostname: vmops\n",
		},
		{
			name: "中文主机名原样写入",
			spec: &CloudInitSpec{Hostname: "测试主机"},
			want: "instance-id: vmops-测试主机\nlocal-hostname: 测试主机\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := GenerateSeedISO(tt.spec)
			if err != nil {
				t.Fatalf("生成 seed ISO 失败: %v", err)
			}
			got := parseSeedISO(t, data).Files["meta-data"]
			if got != tt.want {
				t.Errorf("meta-data 内容不符\n期望: %q\n实际: %q", tt.want, got)
			}
			// instance-id 必须随主机名变化，否则克隆机不会重跑初始化
			if !strings.Contains(got, "instance-id: vmops-") {
				t.Errorf("meta-data 缺少 instance-id 前缀 \"instance-id: vmops-\"，实际: %q", got)
			}
		})
	}
}

// TestGenerateSeedISONetworkConfig 锁定 network-config（netplan v2）的内容。
//
// 风险点：这是唯一决定"新机器有没有网"的文件。NetMode 判定已改为大小写不敏感
// （原裸 == 会把 "STATIC" 静默降级成 DHCP，用户配的静态 IP 悄悄丢掉——在没有
// DHCP 服务的隔离网段里，表现就是机器起来了但完全不通）；static 缺 IP/网关
// 直接报错（见 TestGenerateSeedISOStaticValidation），不再拼出语法合法、语义
// 非法的 netplan（那会让 cloud-init 网络配置整段失效，一张网卡都不通）。
func TestGenerateSeedISONetworkConfig(t *testing.T) {
	const dhcpWant = "version: 2\nethernets:\n  id0:\n    dhcp4: true\n"
	const staticHead = "version: 2\nethernets:\n  id0:\n    dhcp4: false\n"

	tests := []struct {
		name string
		spec *CloudInitSpec
		want string
	}{
		{name: "NetMode 为空：默认 DHCP", spec: &CloudInitSpec{}, want: dhcpWant},
		{name: "nil 配置：默认 DHCP", spec: nil, want: dhcpWant},
		{name: "NetMode=dhcp：DHCP", spec: &CloudInitSpec{NetMode: "dhcp"}, want: dhcpWant},
		{
			name: "NetMode=static 且 IP/网关/DNS 齐全",
			spec: &CloudInitSpec{
				NetMode: "static", IP: "192.168.100.50", Gateway: "192.168.100.1",
				DNS: []string{"223.5.5.5", "8.8.8.8"},
			},
			want: staticHead +
				"    addresses:\n" +
				"    - 192.168.100.50/24\n" +
				"    gateway4: 192.168.100.1\n" +
				"    nameservers:\n" +
				"      addresses:\n" +
				"      - 223.5.5.5\n" +
				"      - 8.8.8.8\n",
		},
		{
			name: "NetMode=static 未填 DNS：兜底 8.8.8.8",
			spec: &CloudInitSpec{NetMode: "static", IP: "10.0.0.5", Gateway: "10.0.0.1"},
			want: staticHead +
				"    addresses:\n" +
				"    - 10.0.0.5/24\n" +
				"    gateway4: 10.0.0.1\n" +
				"    nameservers:\n" +
				"      addresses:\n" +
				"      - 8.8.8.8\n",
		},
		{
			name: "NetMode=static 但只填了一个 DNS",
			spec: &CloudInitSpec{NetMode: "static", IP: "10.0.0.5", Gateway: "10.0.0.1", DNS: []string{"223.5.5.5"}},
			want: staticHead +
				"    addresses:\n" +
				"    - 10.0.0.5/24\n" +
				"    gateway4: 10.0.0.1\n" +
				"    nameservers:\n" +
				"      addresses:\n" +
				"      - 223.5.5.5\n",
		},
		{
			name: "NetMode=STATIC 大写：大小写不敏感，仍按静态生成",
			spec: &CloudInitSpec{NetMode: "STATIC", IP: "10.0.0.5", Gateway: "10.0.0.1"},
			want: staticHead +
				"    addresses:\n" +
				"    - 10.0.0.5/24\n" +
				"    gateway4: 10.0.0.1\n" +
				"    nameservers:\n" +
				"      addresses:\n" +
				"      - 8.8.8.8\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := GenerateSeedISO(tt.spec)
			if err != nil {
				t.Fatalf("生成 seed ISO 失败: %v", err)
			}
			got := parseSeedISO(t, data).Files["network-config"]
			if got != tt.want {
				t.Errorf("network-config 内容不符\n期望:\n%s\n实际:\n%s", tt.want, got)
			}
		})
	}
}

// TestGenerateSeedISOStaticValidation 静态网络缺参必须报错。
// 原行为：static 下缺 IP 拼出 "addresses: [/24]"、缺网关拼出 "gateway4: "，
// netplan 解析报错、cloud-init 网络配置整段失效，而创建流程一路"成功"。
func TestGenerateSeedISOStaticValidation(t *testing.T) {
	for name, spec := range map[string]*CloudInitSpec{
		"static 全缺": {NetMode: "static"},
		"只缺网关":      {NetMode: "static", IP: "10.0.0.5"},
		"只缺 IP":     {NetMode: "static", Gateway: "10.0.0.1"},
	} {
		_, err := GenerateSeedISO(spec)
		if err == nil {
			t.Errorf("%s：期望报错（不能生成坏 seed），实际成功", name)
		}
	}
}

// TestGenerateSeedISOWriteToDisk 复刻调用方的落盘链路并做一次读回校验。
//
// 风险点：GenerateSeedISO 只返回 []byte，真正交给 qemu 的是
// service/tasks/vm_tasks.go 落到 SeedDir 的那个 .iso 文件（先 os.MkdirAll
// 再 os.WriteFile）。落盘环节出问题（目录不存在、写入被截断）时，虚拟机会挂上
// 一张残缺的 cdrom，cloud-init 静默失效。这里用 t.TempDir() 走一遍真实文件 IO：
// 写出、比长度、读回、重新按 ISO 偏移解析，确认三个文件内容一字不差。
//
// 说明：GenerateSeedISO 的签名里没有 outPath 参数（返回字节流而非写文件），
// 因此"输出目录不存在"这一分支属于调用方契约，在最后一个子测试里就地验证。
func TestGenerateSeedISOWriteToDisk(t *testing.T) {
	spec := &CloudInitSpec{
		Hostname: "web-01", User: "ubuntu", Password: "p@ss",
		SSHKey:  "ssh-ed25519 AAAAC3Nz test@vmops",
		NetMode: "static", IP: "192.168.100.50", Gateway: "192.168.100.1",
		DNS: []string{"223.5.5.5"},
	}
	want, err := GenerateSeedISO(spec)
	if err != nil {
		t.Fatalf("生成 seed ISO 失败: %v", err)
	}

	t.Run("落盘后读回，字节与解析结果都不变", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "web-01-seed.iso")
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatalf("写入 seed 镜像失败: %v", err)
		}

		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("seed 镜像应存在，stat 失败: %v", err)
		}
		if st.Size() == 0 {
			t.Fatal("落盘文件大小期望 > 0，实际 0 字节")
		}
		if st.Size() != int64(len(want)) {
			t.Fatalf("落盘文件大小期望 %d 字节，实际 %d 字节", len(want), st.Size())
		}

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读回 seed 镜像失败: %v", err)
		}
		if string(got) != string(want) {
			t.Fatal("读回的字节与内存中的镜像不一致")
		}

		iso := parseSeedISO(t, got)
		if iso.Label != "cidata" {
			t.Errorf("读回后卷标期望 \"cidata\"，实际 %q", iso.Label)
		}
		wantFiles := map[string]string{
			"meta-data": "instance-id: vmops-web-01\nlocal-hostname: web-01\n",
			"user-data": "#cloud-config\nuser: ubuntu\npassword: p@ss\n" +
				"chpasswd:\n  expire: false\nssh_pwauth: true\n" +
				"ssh_authorized_keys:\n- ssh-ed25519 AAAAC3Nz test@vmops\nhostname: web-01\n",
			"network-config": "version: 2\nethernets:\n  id0:\n    dhcp4: false\n" +
				"    addresses:\n    - 192.168.100.50/24\n    gateway4: 192.168.100.1\n" +
				"    nameservers:\n      addresses:\n      - 223.5.5.5\n",
		}
		for name, wantContent := range wantFiles {
			if gotContent := iso.Files[name]; gotContent != wantContent {
				t.Errorf("读回后 %s 内容不符\n期望:\n%s\n实际:\n%s", name, wantContent, gotContent)
			}
		}
	})

	t.Run("输出目录不存在时落盘失败（调用方必须先 MkdirAll）", func(t *testing.T) {
		// 现状：GenerateSeedISO 不负责建目录，也不接受路径；写文件由调用方做，
		// 目录缺失就是 os.WriteFile 报 ENOENT。vm_tasks.go 因此必须先 os.MkdirAll。
		// 这个子测试的意义是把"谁负责建目录"这个契约固定下来——若哪天有人把
		// 落盘逻辑挪进 virt 层却忘了建目录，创建虚拟机会在最后一步失败。
		missing := filepath.Join(t.TempDir(), "not-exist-dir", "seed.iso")
		if err := os.WriteFile(missing, want, 0o644); err == nil {
			t.Fatalf("目录不存在时写入期望报错，实际成功写到 %s", missing)
		} else if !os.IsNotExist(err) {
			t.Errorf("期望「路径不存在」类错误，实际: %v", err)
		}
		// MkdirAll 之后同一路径应能写成功
		if err := os.MkdirAll(filepath.Dir(missing), 0o755); err != nil {
			t.Fatalf("创建目录失败: %v", err)
		}
		if err := os.WriteFile(missing, want, 0o644); err != nil {
			t.Fatalf("MkdirAll 之后写入仍失败: %v", err)
		}
	})
}

// TestGenerateSeedISOYAMLInjectionNeutralized 验证 YAML 注入被结构化序列化中和。
//
// 风险点（本文件最高）：user-data 是 YAML，缩进与换行就是语法。CloudInitSpec
// 的每个字段都来自 HTTP 请求体，主机名里塞一个换行加 "runcmd:" 曾能让新建的
// 虚拟机开机以 root 执行任意命令（fmt.Fprintf 手拼时代，对照 snapshot.go 的
// xmlEscape——快照名走了转义，cloud-init 这条路径是漏的）。
//
// 现在 user-data / meta-data / network-config 全部由 yaml.Marshal 序列化：
// 含换行的值被整体包进双引号转义串，成为字段值本身而非新增顶层键——注入的
// 文本还在（数据不丢），但永远只是惰性字符串。断言方式是把产出再 Unmarshal
// 回来检查结构：顶层键集合精确等于预期，注入的 "runcmd" 只能以值的形式存在。
func TestGenerateSeedISOYAMLInjectionNeutralized(t *testing.T) {
	const evilHostname = "h1\nruncmd:\n  - [touch, /tmp/pwned]"
	const evilKey = "ssh-rsa KEY\nssh_pwauth: false\ndisable_root: false"
	const evilPassword = "p\nchpasswd:\n  list: |\n    root:hacked"

	t.Run("主机名塞换行：runcmd 只能是 hostname 的值，不是顶层键", func(t *testing.T) {
		iso := mustGenerate(t, &CloudInitSpec{Hostname: evilHostname})

		var ud map[string]interface{}
		if err := yaml.Unmarshal([]byte(iso.Files["user-data"]), &ud); err != nil {
			t.Fatalf("user-data 应是合法 YAML（注入被中和后必须可解析）: %v", err)
		}
		if _, ok := ud["runcmd"]; ok {
			t.Errorf("user-data 出现顶层 runcmd 键（注入成功），内容:\n%s", iso.Files["user-data"])
		}
		if got, _ := ud["hostname"].(string); got != evilHostname {
			t.Errorf("hostname 值应原样保留完整注入串（作为惰性数据），实际 %q", got)
		}

		var md map[string]interface{}
		if err := yaml.Unmarshal([]byte(iso.Files["meta-data"]), &md); err != nil {
			t.Fatalf("meta-data 应是合法 YAML: %v", err)
		}
		if _, ok := md["runcmd"]; ok {
			t.Errorf("meta-data 出现顶层 runcmd 键（注入成功），内容:\n%s", iso.Files["meta-data"])
		}
		if got, _ := md["instance-id"].(string); got != "vmops-"+evilHostname {
			t.Errorf("instance-id 应原样保留（%q），实际 %q", "vmops-"+evilHostname, got)
		}
	})

	t.Run("SSH key 塞换行：ssh_pwauth 不被覆盖", func(t *testing.T) {
		iso := mustGenerate(t, &CloudInitSpec{Hostname: "h2", User: "u", Password: "p", SSHKey: evilKey})

		var ud map[string]interface{}
		if err := yaml.Unmarshal([]byte(iso.Files["user-data"]), &ud); err != nil {
			t.Fatalf("user-data 应是合法 YAML: %v", err)
		}
		if v, _ := ud["ssh_pwauth"].(bool); !v {
			t.Errorf("ssh_pwauth 应保持 true（注入串不得覆盖），实际 %v，内容:\n%s",
				ud["ssh_pwauth"], iso.Files["user-data"])
		}
		keys, _ := ud["ssh_authorized_keys"].([]interface{})
		if len(keys) != 1 || keys[0] != evilKey {
			t.Errorf("公钥应作为单条完整字符串保留，实际 %v", keys)
		}
	})

	t.Run("密码塞换行：chpasswd 仍是 expire 标量", func(t *testing.T) {
		iso := mustGenerate(t, &CloudInitSpec{Hostname: "h3", User: "u", Password: evilPassword})

		var ud map[string]interface{}
		if err := yaml.Unmarshal([]byte(iso.Files["user-data"]), &ud); err != nil {
			t.Fatalf("user-data 应是合法 YAML: %v", err)
		}
		chpasswd, _ := ud["chpasswd"].(map[string]interface{})
		if chpasswd == nil {
			t.Fatalf("chpasswd 应仍是 {expire: false} 映射（不被注入撑开），实际 %v，内容:\n%s",
				ud["chpasswd"], iso.Files["user-data"])
		}
		if got, _ := ud["password"].(string); got != evilPassword {
			t.Errorf("password 应原样保留完整注入串，实际 %q", got)
		}
	})

	t.Run("注入不破坏 ISO 骨架：卷标与三文件仍在", func(t *testing.T) {
		iso := mustGenerate(t, &CloudInitSpec{Hostname: evilHostname, Password: evilPassword, SSHKey: evilKey})
		if iso.Label != "cidata" {
			t.Errorf("注入输入下卷标期望仍为 \"cidata\"，实际 %q", iso.Label)
		}
		if len(iso.Entries) != 3 {
			t.Errorf("注入输入下根目录文件数期望仍为 3，实际 %d", len(iso.Entries))
		}
	})
}

// mustGenerate 生成 seed ISO 并按 ECMA-119 偏移解析（注入类用例共用）。
func mustGenerate(t *testing.T, spec *CloudInitSpec) seedISO {
	t.Helper()
	data, err := GenerateSeedISO(spec)
	if err != nil {
		t.Fatalf("生成 seed ISO 失败: %v", err)
	}
	return parseSeedISO(t, data)
}
