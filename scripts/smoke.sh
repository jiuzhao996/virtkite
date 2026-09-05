#!/bin/bash
# vmops E2E 一键回归：覆盖核心 API 全链路（只读优先，写操作用 smoke- 前缀并清理）。
# 用法：BASE=http://127.0.0.1:8080 ./scripts/smoke.sh
# 依赖：curl、python3。失败即停（set -e），每步打印 PASS/FAIL。
set -euo pipefail

BASE="${BASE:-http://127.0.0.1:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-password}"
PASS=0
FAIL=0

ok()   { PASS=$((PASS+1)); echo "  PASS $1"; }
fail() { FAIL=$((FAIL+1)); echo "  FAIL $1 -- $2"; exit 1; }

# 登录拿 token
TOKEN=$(curl -s -m 8 -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['access_token'])")
[ -n "$TOKEN" ] && ok "login" || fail "login" "empty token"
AUTH=(-H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json')

# code==200 断言
expect200() { # $1=描述 $2...=curl参数
  local desc="$1"; shift
  local code
  code=$(curl -s -m 10 -o /tmp/smoke_body.txt -w "%{http_code}" "$@" || true)
  [ "$code" = "200" ] && ok "$desc" || fail "$desc" "http=$code body=$(head -c 200 /tmp/smoke_body.txt)"
}
# 含 data.task_id 的 202 断言，输出 task_id
expect_task() {
  local desc="$1"; shift
  local out http
  out=$(curl -s -m 10 -w "\n%{http_code}" "$@" || true)
  http=$(echo "$out" | tail -1)
  [ "$http" = "202" ] || fail "$desc" "http=$http"
  local tid
  tid=$(echo "$out" | head -1 | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['task_id'])")
  echo "$tid"
}
# 轮询任务到终态，输出 status
wait_task() { # $1=task_id $2=超时秒
  for _ in $(seq 1 $(( $2 / 2 ))); do
    sleep 2
    local st
    st=$(curl -s -m 8 "$BASE/api/tasks/$1" "${AUTH[@]}" | python3 -c "import sys,json;t=json.load(sys.stdin)['data'];print(t['status'])")
    if [ "$st" = "success" ] || [ "$st" = "failed" ]; then echo "$st"; return 0; fi
  done
  echo "timeout"
}

echo "== 只读接口 =="
expect200 "health" "$BASE/api/health"
expect200 "vms list" "$BASE/api/vms" "${AUTH[@]}"
expect200 "vms options" "$BASE/api/vms/options" "${AUTH[@]}"
expect200 "storage pools" "$BASE/api/storage/pools" "${AUTH[@]}"
expect200 "networks" "$BASE/api/networks" "${AUTH[@]}"
expect200 "images" "$BASE/api/images" "${AUTH[@]}"
expect200 "dashboard overview" "$BASE/api/dashboard/overview" "${AUTH[@]}"
expect200 "dashboard host-stats" "$BASE/api/dashboard/host-stats" "${AUTH[@]}"
expect200 "dashboard vm-perf" "$BASE/api/dashboard/vm-perf" "${AUTH[@]}"
expect200 "tasks list" "$BASE/api/tasks" "${AUTH[@]}"
expect200 "sessions list" "$BASE/api/sessions" "${AUTH[@]}"
expect200 "settings" "$BASE/api/settings" "${AUTH[@]}"
expect200 "audit list" "$BASE/api/audit?page_size=1" "${AUTH[@]}"
expect200 "audit actions" "$BASE/api/audit/actions" "${AUTH[@]}"

echo "== /metrics exposition =="
curl -s -m 12 "$BASE/metrics" | grep -q "^vmops_vm_running" && ok "metrics vmops_*" || fail "metrics" "no vmops_ series"

echo "== 写流程（task 异步） =="
VN="smoke-e2e-$(date +%s)"
TID=$(expect_task "create VM" -X POST "$BASE/api/vms" "${AUTH[@]}" -d "{\"name\":\"$VN\",\"storage_pool\":\"base\",\"vcpu\":1,\"memory_mb\":1024,\"disks\":[{\"create_gb\":2}],\"network\":\"default\"}")
echo "  task=$TID"
[ "$(wait_task "$TID" 120)" = "success" ] && ok "create task success" || fail "create task" "not success"
VID=$(curl -s -m 8 "$BASE/api/vms" "${AUTH[@]}" | python3 -c "
import sys,json
for v in json.load(sys.stdin)['data']['items']:
    if v['name']=='$VN': print(v['id'])")
[ -n "$VID" ] && ok "vm visible id=$VID" || fail "vm visible" "not found"

echo "== 硬件管理（停机态） =="
expect200 "set cpu" -X PUT "$BASE/api/vms/$VID/cpu" "${AUTH[@]}" -d '{"vcpu":2}'
expect200 "snapshot list" "$BASE/api/vms/$VID/snapshots" "${AUTH[@]}"
expect200 "vm spec" "$BASE/api/vms/$VID/spec" "${AUTH[@]}"

echo "== 删除（task） =="
TID2=$(expect_task "delete VM" -X DELETE "$BASE/api/vms/$VID" "${AUTH[@]}")
[ "$(wait_task "$TID2" 120)" = "success" ] && ok "delete task success" || fail "delete task" "not success"
LEFT=$(curl -s -m 8 "$BASE/api/vms" "${AUTH[@]}" | python3 -c "
import sys,json
print(any(v['name']=='$VN' for v in json.load(sys.stdin)['data']['items']))")
[ "$LEFT" = "False" ] && ok "vm cleaned" || fail "vm cleaned" "still present"

echo
echo "RESULT: PASS=$PASS FAIL=$FAIL"
[ "$FAIL" = "0" ]
