// 本文件负责网络安装源（FTP/HTTP/HTTPS）的内核与 initrd 下载、以及建机向导
// 「测试」按钮的安装树可达性探测（均为 curl 实现，Go 标准库无 FTP 客户端）。
package tasks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jiuzhao/vmops/config"
)

// fetchInstallKernel 从网络安装源下载内核与 initrd 到本地（等价 virt-install --location 的准备工作）。
// 默认按 RHEL/Rocky 树布局 images/pxeboot/{vmlinuz,initrd.img}，可用 payload 覆盖相对路径。
// 落盘 data/install/<vm名>/，VM 删除时一并清理。
func fetchInstallKernel(ctx *ExecContext, vmName, baseURL, kernelRel, initrdRel string) (string, string, error) {
	if kernelRel == "" {
		kernelRel = "images/pxeboot/vmlinuz"
	}
	if initrdRel == "" {
		initrdRel = "images/pxeboot/initrd.img"
	}
	dir := config.DataPath("install", vmName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("创建安装源缓存目录失败: %w", err)
	}
	kp := filepath.Join(dir, "vmlinuz")
	ip := filepath.Join(dir, "initrd.img")
	if err := downloadViaCurl(ctx, joinURL(baseURL, kernelRel), kp); err != nil {
		return "", "", fmt.Errorf("下载安装内核失败: %w", err)
	}
	if err := downloadViaCurl(ctx, joinURL(baseURL, initrdRel), ip); err != nil {
		return "", "", fmt.Errorf("下载安装 initrd 失败: %w", err)
	}
	return kp, ip, nil
}

// joinURL 拼接基础 URL 与相对路径（去掉多余斜杠）。
func joinURL(base, rel string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(rel, "/")
}

// ProbeInstallSource 探测网络安装树是否可达且含标准内核/initrd（供建机向导「测试」按钮）。
// 复用真实下载的默认相对路径（RHEL 系 images/pxeboot/…），--range 0-0 只取首字节不拉全量；
// 支持 ftp/http/https（与下载同为 curl）。返回 nil 表示内核与 initrd 都可取。
func ProbeInstallSource(baseURL string) error {
	u := strings.TrimSpace(baseURL)
	if !validInstallSourceURL(u) {
		return errors.New("地址格式不正确：需 ftp:// 或 http(s):// 开头并含安装树路径")
	}
	// 与 fetchInstallKernel 的默认相对路径保持一致（改一处须同步另一处）
	for _, rel := range []string{"images/pxeboot/vmlinuz", "images/pxeboot/initrd.img"} {
		if err := probeURLExists(joinURL(u, rel)); err != nil {
			return fmt.Errorf("取不到 %s：%w", rel, err)
		}
	}
	return nil
}

// validInstallSourceURL 校验 scheme(ftp/http/https) + 主机 + 安装树路径（与前端 isValidInstallTreeUrl 同口径）。
func validInstallSourceURL(u string) bool {
	l := strings.ToLower(u)
	if !strings.HasPrefix(l, "ftp://") && !strings.HasPrefix(l, "http://") && !strings.HasPrefix(l, "https://") {
		return false
	}
	rest := u[strings.Index(u, "://")+3:]
	// 主机非空（rest 不以 / 开头）且含路径（至少一个 /）
	return !strings.HasPrefix(rest, "/") && strings.Contains(rest, "/")
}

// probeURLExists 用 curl 探测单个 URL 可取（--range 0-0 取首字节，-o /dev/null 丢弃内容）。
func probeURLExists(rawURL string) error {
	cctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "curl", "-fsS", "--max-time", "15", "--range", "0-0", "-o", "/dev/null", rawURL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s: %w", msg, err)
		}
		return fmt.Errorf("curl 探测失败: %w", err)
	}
	return nil
}

// downloadViaCurl 用 curl 下载（支持 ftp/http/https——Go 标准库无 FTP 客户端）。
// 数组参数、20 分钟超时；失败把 curl 的 stderr 带进错误链。
func downloadViaCurl(ctx *ExecContext, rawURL, target string) error {
	base := context.Background()
	if ctx != nil && ctx.Ctx != nil {
		base = ctx.Ctx
	}
	cctx, cancel := context.WithTimeout(base, 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, "curl", "-fsSL", "--retry", "2", "-o", target, rawURL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("%s: %w", msg, err)
		}
		return fmt.Errorf("curl 下载失败: %w", err)
	}
	return nil
}
