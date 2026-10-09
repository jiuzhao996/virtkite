#!/bin/sh
# vmops 内置基础优化 —— 模板固化时经 `virt-customize --run` 在 guest 内执行。
#
# 目标：让模板克隆出来的机器「开机即用」——自带 qemu-guest-agent 与 cloud-init（克隆机首次
# 开机自动初始化）、可用串口控制台（virsh console 免 IP 直连）、关掉会捣乱的 SELinux/firewalld、
# 换国内源加速。对齐用户笔记 base_config.sh / Virt02 模版机配置。
#
# 纪律：**全部 best-effort**——非 RHEL/Debian、离线源、包名差异都不得让整个固化失败；
# 每步 `|| true`，末尾 `exit 0`（virt-customize 遇非零退出会判失败）。
set +e

log() { echo "[vmops-optimize] $*"; }

# ── 1. 探测包管理器 ──
PKG=""
if command -v dnf >/dev/null 2>&1; then PKG=dnf
elif command -v yum >/dev/null 2>&1; then PKG=yum
elif command -v apt-get >/dev/null 2>&1; then PKG=apt
fi
log "包管理器: ${PKG:-未知（跳过装包）}"

# ── 2. 换国内源（best-effort，仅 RHEL 系；先换源再装包，避免默认源超时）──
if [ "$PKG" = "dnf" ] || [ "$PKG" = "yum" ]; then
  for f in /etc/yum.repos.d/*.repo; do
    [ -f "$f" ] || continue
    sed -i 's|^mirrorlist=|#mirrorlist=|' "$f" >/dev/null 2>&1
    sed -i 's|^#\?baseurl=http://dl.rockylinux.org|baseurl=https://mirrors.aliyun.com/rockylinux|' "$f" >/dev/null 2>&1
  done
  log "已尝试切换 RHEL 系源到 aliyun"
fi

# ── 3. 装 qemu-guest-agent + cloud-init（克隆机自动初始化 / 平台取 IP 的前提）──
install_pkgs() {
  [ -z "$PKG" ] && return 0
  case "$PKG" in
    dnf|yum) $PKG install -y "$@" >/dev/null 2>&1 || log "装包失败（可忽略）: $*" ;;
    apt) DEBIAN_FRONTEND=noninteractive apt-get install -y "$@" >/dev/null 2>&1 || log "装包失败（可忽略）: $*" ;;
  esac
}
if [ "$PKG" = "apt" ]; then apt-get update >/dev/null 2>&1; fi
install_pkgs qemu-guest-agent cloud-init cloud-utils-growpart
install_pkgs vim-enhanced vim tree wget curl bash-completion lrzsz zip unzip tar net-tools sysstat htop bind-utils openssh-clients

# ── 4. 启服务（离线仅 enable；start 需运行中的 systemd，此处不做）──
systemctl enable qemu-guest-agent >/dev/null 2>&1 || true
systemctl enable cloud-init >/dev/null 2>&1 || true
systemctl enable cloud-init-local >/dev/null 2>&1 || true

# ── 5. 串口控制台（virsh console 免 IP 直连的前提）──
if command -v grubby >/dev/null 2>&1; then
  grubby --update-kernel=ALL --args="console=ttyS0,115200n8" >/dev/null 2>&1 || true
elif [ -f /etc/default/grub ]; then
  if ! grep -q 'console=ttyS0' /etc/default/grub; then
    sed -i 's/^GRUB_CMDLINE_LINUX="\(.*\)"/GRUB_CMDLINE_LINUX="\1 console=ttyS0,115200n8"/' /etc/default/grub >/dev/null 2>&1
    (update-grub || grub2-mkconfig -o /boot/grub2/grub.cfg) >/dev/null 2>&1 || true
  fi
fi
# 启用 serial-getty@ttyS0：离线 chroot 下 systemctl enable 常因无 systemd 运行而失败，
# 直接建符号链接（等价 enable，纯文件操作，稳定）
if [ -f /usr/lib/systemd/system/serial-getty@.service ]; then
  mkdir -p /etc/systemd/system/getty.target.wants
  ln -sf /usr/lib/systemd/system/serial-getty@.service \
    /etc/systemd/system/getty.target.wants/serial-getty@ttyS0.service >/dev/null 2>&1 || true
fi

# ── 6. 关 SELinux / firewalld（教学环境简化；生产按需保留）──
if [ -f /etc/selinux/config ]; then
  sed -i 's/^SELINUX=enforcing/SELINUX=disabled/' /etc/selinux/config >/dev/null 2>&1 || true
fi
systemctl disable firewalld >/dev/null 2>&1 || true

log "基础优化完成（best-effort；个别步骤失败不影响模板产出）"
exit 0
