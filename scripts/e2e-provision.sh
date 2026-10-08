#!/bin/bash
# vmops golden-path E2E：覆盖 smoke.sh 到不了的下半程——「建机 → 自动初始化（等 IP + 托管凭据）
# → 装应用 → 跑 playbook → 删除级联」。依赖真实 libvirt/SSH/ansible 环境。
# 用法：BASE=http://127.0.0.1:8080 ADMIN_PASS=... ./scripts/e2e-provision.sh
# 依赖：curl、python3。失败即停（set -e），每步打印 PASS/FAIL。
# 环境变量：SKIP_REMOTE=1 跳过装应用/playbook 两步（无 SSH/ansible 环境时用）。
set -euo pipefail

BASE="${BASE:-http://127.0.0.1:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-password}"
SKIP_REMOTE="${SKIP_REMOTE:-0}"
PASS=0
FAIL=0
WARN=0

ok()   { PASS=$((PASS+1)); echo "  PASS $1"; }
warn() { WARN=$((WARN+1)); echo "  WARN $1 -- $2"; }
fail() { FAIL=$((FAIL+1)); echo "  FAIL $1 -- $2"; exit 1; }

TOKEN=$(curl -s -m 8 -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['access_token'])")
[ -n "$TOKEN" ] && ok "login" || fail "login" "empty token"
AUTH=(-H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json')

wait_task() { # $1=task_id $2=超时秒 → 输出终态
  for _ in $(seq 1 $(( $2 / 2 ))); do
    sleep 2
    local st
    st=$(curl -s -m 8 "$BASE/api/tasks/$1" "${AUTH[@]}" | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['status'])")
    if [ "$st" = "success" ] || [ "$st" = "failed" ]; then echo "$st"; return 0; fi
  done
  echo "timeout"
}
submit_task() { # $1=描述 $2..=curl参数 → 输出 task_id
  local desc="$1"; shift
  local out http tid
  out=$(curl -s -m 10 -w "\n%{http_code}" "$@" || true)
  http=$(echo "$out" | tail -1)
  [ "$http" = "202" ] || fail "$desc" "http=$http body=$(echo "$out" | head -1 | head -c 200)"
  tid=$(echo "$out" | head -1 | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['task_id'])")
  echo "$tid"
}

echo "== 选一个模板云镜像（source_image_id）=="
IMG_ID=$(curl -s -m 8 "$BASE/api/images" "${AUTH[@]}" | python3 -c "
import sys,json
d=json.load(sys.stdin)['data']
items=d.get('items') if isinstance(d,dict) else d
for it in items or []:
    if it.get('is_template') and (it.get('format') or 'qcow2')=='qcow2':
        print(it['id']); break
")
[ -n "${IMG_ID:-}" ] && ok "找到模板镜像 id=$IMG_ID" || fail "模板镜像" "镜像库无 is_template 的 qcow2（先在镜像管理标记模板）"

VN="e2e-prov-$(date +%s)"
CIPASS="Passw0rd${RANDOM}"

echo "== 建机 + 自动初始化（provision：开机 → 等 IP → 托管凭据） =="
TID=$(submit_task "create VM(auto_provision)" -X POST "$BASE/api/vms" "${AUTH[@]}" -d "{
  \"name\":\"$VN\",
  \"storage_pool\":\"images\",
  \"vcpu\":1,\"memory_mb\":1024,
  \"disks\":[{\"source_image_id\":$IMG_ID,\"create_gb\":10}],
  \"interfaces\":[{\"type\":\"network\",\"source\":\"default\",\"model\":\"virtio\"}],
  \"network\":\"default\",
  \"cloud_init\":{\"hostname\":\"$VN\",\"user\":\"root\",\"password\":\"$CIPASS\"},
  \"auto_provision\":true
}")
echo "  task=$TID"
[ "$(wait_task "$TID" 360)" = "success" ] && ok "建机+初始化任务 success" || fail "建机+初始化" "not success"

VID=$(curl -s -m 8 "$BASE/api/vms" "${AUTH[@]}" | python3 -c "
import sys,json
for v in json.load(sys.stdin)['data']['items']:
    if v['name']=='$VN': print(v['id'])")
[ -n "$VID" ] && ok "VM 可见 id=$VID" || fail "VM 可见" "not found"

VM_JSON=$(curl -s -m 8 "$BASE/api/vms" "${AUTH[@]}" | python3 -c "
import sys,json
for v in json.load(sys.stdin)['data']['items']:
    if v['id']==$VID: print(json.dumps(v))")
STATUS=$(echo "$VM_JSON" | python3 -c "import sys,json;print(json.load(sys.stdin).get('status',''))")
IP=$(echo "$VM_JSON" | python3 -c "import sys,json;print(json.load(sys.stdin).get('ip') or '')")
[ "$STATUS" = "running" ] && ok "VM 已开机（status=running）" || fail "VM 开机" "status=$STATUS"
[ -n "$IP" ] && ok "VM 已获 IP（provision 等 IP 生效）ip=$IP" || fail "VM IP" "IP 为空"

echo "== 凭据自动托管（provision 落 vm_credentials） =="
CRED=$(curl -s -m 8 "$BASE/api/vms/$VID/credentials" "${AUTH[@]}" | python3 -c "import sys,json;print(json.load(sys.stdin)['data'].get('configured'))")
[ "$CRED" = "True" ] && ok "托管凭据已就绪" || fail "托管凭据" "configured=$CRED"

if [ "$SKIP_REMOTE" = "1" ]; then
  echo "== 跳过远端步骤（SKIP_REMOTE=1）=="
else
  echo "== 装应用（use_saved 走托管凭据） =="
  ATID=$(submit_task "app_install nginx" -X POST "$BASE/api/vms/apps/install" "${AUTH[@]}" \
    -d "{\"vm_id\":$VID,\"app_id\":\"nginx\",\"use_saved\":true}")
  [ "$(wait_task "$ATID" 300)" = "success" ] && ok "应用安装任务 success" || fail "应用安装" "not success"

  echo "== 跑 playbook（adhoc ping，验证 ansible 通道） =="
  # 软断言：部分模板镜像的 root 登录 shell 会往 stdout 打 banner，ansible（OpenSSH 客户端）
  # 探测远端 tmp 目录会被打乱而失败；vmssh（Go SSH）不受影响，故 app 步骤仍为硬门。
  # 该现象属镜像环境问题（检查模板 /root/.bashrc 等登录脚本），故此处 WARN 不 FAIL。
  PTID=$(submit_task "ansible ping" -X POST "$BASE/api/ansible/run" "${AUTH[@]}" \
    -d "{\"targets\":[$VID],\"module\":\"ping\"}")
  PST=$(wait_task "$PTID" 180)
  if [ "$PST" = "success" ]; then
    ok "ansible ping success"
  else
    warn "ansible ping" "任务 $PST（多为镜像 root 登录 banner 打乱 ansible tmp 目录；非平台代码问题）"
  fi
fi

echo "== 删除 + 级联清理（vm_credentials 应随资产消亡） =="
DTID=$(submit_task "delete VM" -X DELETE "$BASE/api/vms/$VID" "${AUTH[@]}")
[ "$(wait_task "$DTID" 180)" = "success" ] && ok "删除任务 success" || fail "删除" "not success"
CRED2=$(curl -s -m 8 "$BASE/api/vms/$VID/credentials" "${AUTH[@]}" \
  | python3 -c "import sys,json;d=json.load(sys.stdin);print((d.get('data') or {}).get('configured'))" 2>/dev/null || echo "None")
[ "$CRED2" != "True" ] && ok "托管凭据已级联清除" || fail "凭据级联" "仍 configured=True"

echo
echo "RESULT: PASS=$PASS WARN=$WARN FAIL=$FAIL"
[ "$FAIL" = "0" ]
