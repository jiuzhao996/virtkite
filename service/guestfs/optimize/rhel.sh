  # ── RHEL 系专属（Rocky / AlmaLinux / CentOS / RHEL）──

  # 换国内源（aliyun；仅 rockylinux 路径命中，其余发行版自然跳过）
  for f in /etc/yum.repos.d/*.repo; do
    [ -f "$f" ] || continue
    sed -i 's|^mirrorlist=|#mirrorlist=|' "$f" >/dev/null 2>&1 || true
    sed -i 's|^#\?baseurl=http://dl.rockylinux.org|baseurl=https://mirrors.aliyun.com/rockylinux|' "$f" >/dev/null 2>&1 || true
  done

  # qemu-guest-agent + cloud-init + 开机自动扩容（RHEL 包名 cloud-utils-growpart）
  install_pkgs qemu-guest-agent cloud-init cloud-utils-growpart
  systemctl enable qemu-guest-agent >/dev/null 2>&1 || true
  systemctl enable cloud-init >/dev/null 2>&1 || true

  # 串口 console 内核参数（grubby；RHEL 系标准工具）
  if command -v grubby >/dev/null 2>&1; then
    grubby --update-kernel=ALL --args="console=ttyS0,115200n8" >/dev/null 2>&1 || true
  fi
