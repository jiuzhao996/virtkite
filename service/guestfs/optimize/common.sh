# ── 公共优化段（发行版无关，全部 best-effort：每步 || true，个别失败不影响模板产出）──

# 装包助手：自动适配 dnf / yum / apt（发行版差异只在这一处）
install_pkgs() {
  [ $# -gt 0 ] || return 0
  if command -v dnf >/dev/null 2>&1; then
    dnf install -y "$@" >/dev/null 2>&1 || log "装包失败（可忽略）: $*"
  elif command -v yum >/dev/null 2>&1; then
    yum install -y "$@" >/dev/null 2>&1 || log "装包失败（可忽略）: $*"
  elif command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get install -y "$@" >/dev/null 2>&1 || log "装包失败（可忽略）: $*"
  fi
}

# 1) 常用运维工具（vim/tree/wget/压缩/网络/性能…）
install_pkgs vim vim-enhanced tree wget curl bash-completion lrzsz zip unzip tar net-tools sysstat htop bind-utils openssh-clients

# 2) 关 SELinux / firewalld（内网 / 教学环境：安全不是主要矛盾；生产按需保留）
if [ -f /etc/selinux/config ]; then
  sed -i 's/^SELINUX=enforcing/SELINUX=disabled/' /etc/selinux/config >/dev/null 2>&1 || true
fi
systemctl disable firewalld >/dev/null 2>&1 || true

# 3) 串口 getty（virsh console 免 IP 登录的前提；离线 chroot 下 systemctl enable 常失败，
#    直接建符号链接——等价 enable 且纯文件操作，稳定）
if [ -f /usr/lib/systemd/system/serial-getty@.service ]; then
  mkdir -p /etc/systemd/system/getty.target.wants
  ln -sf /usr/lib/systemd/system/serial-getty@.service \
    /etc/systemd/system/getty.target.wants/serial-getty@ttyS0.service >/dev/null 2>&1 || true
fi

# 4) MOTD 彩色欢迎语（按主机名——模板克隆后每台显示自己的主机名）
cat > /etc/profile.d/00-vmops-motd.sh <<'MOTDEOF'
#!/bin/sh
# 仅交互式登录时打印（非交互式 ssh 执行命令不打扰）
case "$-" in *i*) ;; *) return 0 2>/dev/null || exit 0 ;; esac
_c1='\033[1;36m'; _c2='\033[0;33m'; _r='\033[0m'
printf "\n${_c1}  ▸ 鸢航 VirtKite 云主机${_r}\n"
printf "    主机名 ${_c2}%s${_r}   内核 %s   IP %s\n\n" \
  "$(hostname)" "$(uname -r)" "$(hostname -I 2>/dev/null | awk '{print $1}')"
MOTDEOF
chmod +x /etc/profile.d/00-vmops-motd.sh >/dev/null 2>&1 || true
