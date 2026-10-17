// storage_validate.go：存储池/卷的名称、宿主机路径与格式白名单校验（纯函数，供单测）。
package handler

import (
	"path/filepath"
	"regexp"
	"strings"
)

// volNameRegex 卷/池名称允许字母数字下划线连字符和点（卷名含扩展名）
var volNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// poolPathRegex 存储池宿主机路径字符白名单：以 / 开头，只允许字母、数字、下划线、连字符、点和斜杠。
// 排除引号、尖括号等 XML 元字符，与 virt 层的 encoding/xml 序列化构成纵深防御。
var poolPathRegex = regexp.MustCompile(`^/[a-zA-Z0-9_./-]+$`)

// validVolName 校验卷/池名称合法性
func validVolName(name string) bool {
	return volNameRegex.MatchString(name)
}

// validPoolPath 校验目录型存储池的宿主机路径（对应 virsh pool-define-as --target）。
// 目录池的 path 决定该池所有卷的落盘位置，零校验等于让调用方指定宿主机任意目录，// 因此要求：绝对路径 + 字符白名单 + 规范写法（filepath.Clean 后与原值一致，
// 借此拒绝 ..、// 与结尾斜杠）+ 不得为根目录本身。
func validPoolPath(p string) bool {
	if !filepath.IsAbs(p) || !poolPathRegex.MatchString(p) {
		return false
	}
	clean := filepath.Clean(p)
	if clean != p || clean == "/" || strings.Contains(clean, "..") {
		return false
	}
	return true
}

// validVolFormat 校验卷格式白名单：只允许 qcow2 / raw（与前端下拉选项一致）。
// 空值保持原行为不变（由 virt 层 CreateVolumeCustom 默认 qcow2）。
func validVolFormat(format string) bool {
	switch format {
	case "", "qcow2", "raw":
		return true
	}
	return false
}
