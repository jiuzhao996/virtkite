package virt

// OSItem OS 类型选项（创建虚拟机向导展示用，对应 virt-manager 的 OS 类型选择）。
type OSItem struct {
	Name      string `json:"name"`       // 展示名（如 "CentOS Stream 9"）
	OSType    string `json:"os_type"`    // os type，hvm
	Arch      string `json:"arch"`       // x86_64
	Machine   string `json:"machine"`    // 默认机型
	DiskBus   string `json:"disk_bus"`   // 默认磁盘总线（virtio / sata）
	NicModel  string `json:"nic_model"`  // 默认网卡型号（virtio / e1000）
	CloudInit bool   `json:"cloud_init"` // 是否支持 cloud-init（云镜像）
}

// OSList 创建虚拟机向导的静态 OS 选项（对齐 virt-manager 的 os-variant 常用项）。
// 覆盖常用 Linux 发行版 / Windows / 其它，供前端下拉选择与设备型号自动推荐。
var OSList = []OSItem{
	{Name: "CentOS Stream 9", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio"},
	{Name: "CentOS Stream 8", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio"},
	{Name: "Rocky Linux 9", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio"},
	{Name: "Rocky Linux 8", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio"},
	{Name: "Ubuntu 24.04 LTS", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio", CloudInit: true},
	{Name: "Ubuntu 22.04 LTS", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio", CloudInit: true},
	{Name: "Ubuntu 20.04 LTS", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio", CloudInit: true},
	{Name: "Debian 12", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio"},
	{Name: "Debian 11", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio"},
	{Name: "openEuler 22.03", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio", CloudInit: true},
	{Name: "openEuler 20.03", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio", CloudInit: true},
	{Name: "Kylin V10 (银河麒麟)", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio", CloudInit: true},
	{Name: "UOS V20 (统信)", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio", CloudInit: true},
	{Name: "Fedora 40", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio"},
	{Name: "SUSE SLES 15", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio"},
	{Name: "Arch Linux", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio"},
	{Name: "Windows Server 2022", OSType: "hvm", Arch: "x86_64", DiskBus: "sata", NicModel: "e1000e"},
	{Name: "Windows Server 2019", OSType: "hvm", Arch: "x86_64", DiskBus: "sata", NicModel: "e1000e"},
	{Name: "Windows 11", OSType: "hvm", Arch: "x86_64", DiskBus: "sata", NicModel: "e1000e"},
	{Name: "Windows 10", OSType: "hvm", Arch: "x86_64", DiskBus: "sata", NicModel: "e1000e"},
	{Name: "Generic Linux", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio"},
	{Name: "Generic (无 OS)", OSType: "hvm", Arch: "x86_64", DiskBus: "virtio", NicModel: "virtio"},
}
