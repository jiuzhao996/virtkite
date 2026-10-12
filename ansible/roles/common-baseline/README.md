# common-baseline

节点基线（时区 / 时间同步 / 基础工具 / 登录欢迎语）。

## 用法

放在**共享 role 库**里，任何 playbook 直接引用即可，不需要复制：

```yaml
- hosts: all
  become: true
  roles:
    - common-baseline
```

## 变量（见 `defaults/main.yml`）

| 变量 | 默认 | 说明 |
|---|---|---|
| `baseline_timezone` | `Asia/Shanghai` | 时区 |
| `baseline_manage_timezone` | `true` | 是否托管时区 |
| `baseline_manage_motd` | `true` | 是否渲染 `/etc/motd` |
| `baseline_packages` | vim/curl/wget/tar/lsof/net-tools | 基础工具包 |

调用方覆盖（playbook 的 `vars` 或执行时的 `-e` 表单）：

```yaml
- hosts: all
  roles:
    - role: common-baseline
      vars:
        baseline_timezone: UTC
```

## 特化

某个 playbook 想改这个 role 的行为又不想动全平台，就在**它自己的目录**下放一份同名
`roles/common-baseline/`——playbook 本地 role 优先于共享库（已实测）。
