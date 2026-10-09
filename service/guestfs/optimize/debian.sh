  # ── Debian 系专属（Ubuntu / Debian）──

  # 换国内源（aliyun；best-effort，同时覆盖 deb822 新格式 ubuntu.sources）
  if [ -f /etc/apt/sources.list ] && ! grep -q 'mirrors.aliyun.com' /etc/apt/sources.list 2>/dev/null; then
    sed -i 's|//archive.ubuntu.com|//mirrors.aliyun.com|g; s|//security.ubuntu.com|//mirrors.aliyun.com|g; s|//deb.debian.org|//mirrors.aliyun.com|g' /etc/apt/sources.list >/dev/null 2>&1 || true
  fi
  if [ -f /etc/apt/sources.list.d/ubuntu.sources ] && ! grep -q 'mirrors.aliyun.com' /etc/apt/sources.list.d/ubuntu.sources 2>/dev/null; then
    sed -i 's|//archive.ubuntu.com|//mirrors.aliyun.com|g; s|//security.ubuntu.com|//mirrors.aliyun.com|g' /etc/apt/sources.list.d/ubuntu.sources >/dev/null 2>&1 || true
  fi
  apt-get update >/dev/null 2>&1 || true

  # qemu-guest-agent + cloud-init + 开机自动扩容（Debian 包名 cloud-guest-utils）
  install_pkgs qemu-guest-agent cloud-init cloud-guest-utils
  systemctl enable qemu-guest-agent >/dev/null 2>&1 || true
  systemctl enable cloud-init >/dev/null 2>&1 || true
  systemctl enable cloud-init-local >/dev/null 2>&1 || true

  # 串口 console 内核参数（/etc/default/grub + update-grub）
  if [ -f /etc/default/grub ] && ! grep -q 'console=ttyS0' /etc/default/grub; then
    sed -i 's/^GRUB_CMDLINE_LINUX="\(.*\)"/GRUB_CMDLINE_LINUX="\1 console=ttyS0,115200n8"/' /etc/default/grub >/dev/null 2>&1 || true
    (update-grub || grub2-mkconfig -o /boot/grub2/grub.cfg) >/dev/null 2>&1 || true
  fi
