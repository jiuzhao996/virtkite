---
title: "异步任务系统契约"
description: "耗时操作（创建/删除/克隆/优雅关机）转后台任务：Task 模型、Manager API、Executor 签名、REST 接口（Wave 唯一事实源）"
tags: [契约, task, 异步]
---

# 异步任务系统契约（唯一事实源）

> 背景：StopVM 同步轮询 15s 撞上 axios 15s 超时；大盘创建/克隆同样慢。JumpServer/PVE 均用 task 队列。

## Task 模型（model/task.go，T1 产出）

```go
type Task struct {
    ID        uint      `gorm:"primaryKey" json:"id"`
    Type      string    `gorm:"size:50;index" json:"type"`     // create_vm / delete_vm / clone_vm / clone_image_vm / stop_vm / cleanup_volumes
    Title     string    `gorm:"size:200" json:"title"`         // 如 "创建虚拟机 smoke-01"
    Status    string    `gorm:"size:20;index" json:"status"`   // pending / running / success / failed
    Progress  int       `gorm:"default:0" json:"progress"`    // 0-100
    Payload   string    `gorm:"type:text" json:"-"`           // 执行参数 JSON（内部用，不返回前端）
    Result    string    `gorm:"type:text" json:"result,omitempty"` // 成功结果 JSON，如 {"vm_id":123}；字段一览见下
    Error     string    `gorm:"size:500" json:"error,omitempty"`   // 失败中文原因
    UserID    *uint     `json:"user_id"`
    Username  string    `gorm:"size:100" json:"username"`
    VMID      *uint     `json:"vm_id,omitempty"`
    VMName    string    `gorm:"size:100" json:"vm_name,omitempty"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
func (Task) TableName() string { return "tasks" }
```

## Manager API（service/tasks/manager.go，T1 产出）

```go
type ProgressFunc func(pct int, msg string)
type ExecContext struct {
    DB      *gorm.DB
    Virt    *virt.Virt
    Task    *model.Task            // DB 记录（worker 内更新 Status/Progress/Result/Error）
    Payload map[string]interface{} // task.Payload 反序列化
    Report  ProgressFunc           // 上报进度（内部写 DB task.Progress）
}
type Executor func(ctx *ExecContext) error  // 返回 nil=成功（可写 ctx.Task.Result），error=失败（中文友好，写 Task.Error）

type Manager struct { /* DB, Virt, queue chan uint(taskID), workers, executors map[string]Executor */ }
func NewManager(db *gorm.DB) *Manager
// NewManager 内部 virt.New()、起 4 个 worker goroutine。Manager 生命周期与进程一致，
// 不提供单独关闭入口（无 quit/Close 字段，进程退出即弃）。
// worker 循环：`for id := range m.queue` → DB 读 task → 置 running → 查 executors[task.Type] → 执行 → 置 success/failed。
// Report(pct,msg)：UPDATE tasks SET progress=pct WHERE id（msg 忽略或记日志，暂不存）。
func (m *Manager) Register(taskType string, fn Executor)
func (m *Manager) Submit(taskType, title string, payload interface{}, userID *uint, username, vmName string, vmID *uint) (*model.Task, error)
// Submit：payload 序列化 JSON 存 Payload，Status=pending 入库后交 enqueue 有界入队（见下）。
func (m *Manager) Get(id uint) (*model.Task, error)
func (m *Manager) List(limit int, status string) ([]model.Task, error) // 按 id desc，status 为空查全部
```

### 入队策略（有界等待，勿回退）

```go
const enqueueTimeout = 3 * time.Second
func (m *Manager) enqueue(task *model.Task)
```

`enqueue` 先非阻塞 `select` 尝试入队；队列满则用**单个** `time.NewTimer(enqueueTimeout)`
最多等 3 秒（不在循环里反复 `time.After`）。等满仍入不了即判定积压严重：

1. 打日志 `[tasks] 入队超时（队列已满）id=.. type=.. vm=.. wait=3s`；
2. `m.markFailed(task.ID, msgQueueBusy)` 落库置 `failed`；
3. **同步回写返回给 handler 的 `task` 结构体**（`task.Status = "failed"`、`task.Error = "任务队列繁忙，请稍后重试"`），
   避免前端拿到 `pending` 却永远等不到执行。

原实现为 `go func(){ m.queue <- task.ID }()`：队列满时会随提交频率堆积**无上限且永不退出**的阻塞
goroutine。改为「调用方同步等待」的取舍——等待发生在 handler 自己的请求 goroutine 上
（本就存在、由连接数天然限流），goroutine 数量零增长，且能把背压如实传回客户端；
代价是队列满时 `Submit` 最多阻塞 3 秒。

### panic 兜底（两层 recover，勿回退）

gin 的 Recovery 中间件**只覆盖 HTTP 请求链，不覆盖后台 worker goroutine**，
worker 内一次 panic 会直接带走整个进程。故：

| 层 | 函数 | 行为 |
|---|---|---|
| executor | `runExecutor(fn, ctx)` | recover 后把 panic 值与 `debug.Stack()` 打进日志，转成 `errExecutorPanic` 走统一失败路径 |
| worker | `run(id)` 顶层 `defer` | 覆盖 executor 之外的裸奔点（DB 读写、JSON 反序列化），`markFailed(id, msgTaskPanic)` |
| 落库 | `markFailed(id, msg)` | 自带 recover，DB 层再出意外也不把进程带走；按字段 `Updates` 避免 `Save` 全行回写冲掉已上报 progress |

用户可见文案（常量）：

| 常量 | 文案 | 场景 |
|---|---|---|
| `msgTaskPanic` | `任务执行异常，请查看服务日志` | executor 或 worker panic |
| `msgQueueBusy` | `任务队列繁忙，请稍后重试` | 入队超时 |

**panic 值与堆栈只进服务端日志，绝不写入 `Task.Error`**（该字段会回显前端）。
同理 `friendlyError` 会丢掉原始错误链，因此 `run` 必须先把完整错误打进日志再落库。

状态机取值仍为 `pending / running / success / failed`，**未变**。

## Executors（service/tasks/vm_tasks.go，T2 产出）

```go
func RegisterVMTasks(m *Manager) // 注册 create_vm / delete_vm / clone_vm / clone_image_vm / stop_vm / cleanup_volumes
```

- **create_vm**：payload = 原 CreateVM 请求体 JSON（含 name/storage_pool/vcpu/memory_mb/disks/interfaces/network/iso_path/cloud_init/host_id）。逻辑从 `handler/vm.go CreateVM` 整体搬运：校验名称→查 host（host_id 缺省 firstHost）→默认值→UUID/MAC→组装 spec→逐盘落地（Report 10/30/50/70）→seed→BuildDomainXML→写 DB→DefineDomain。成功置 `ctx.Task.Result={"vm_id":id}`，并回填 Task.VMID/VMName。失败清理已建卷+seed（沿用原 cleanup）。
- **delete_vm**：payload `{vm_id}`。逻辑从 DeleteVM 搬运：查 VM→取 spec 枚举磁盘→UndefineDomain（Report 30）→逐卷清理（**过三重守卫**，Report 70）+seed→DB 软删除。成功 Result=`{"vm":name}`，有卷被守卫保留时追加 `kept_volumes`（见下节）。
- **clone_vm**：payload `{source_id,name,storage_pool,vcpu,memory_mb,network}`。从 CloneVM 搬运：查源→GetDomainSpec→覆盖→CloneVMFromSpec（Report 50，**内部重生成 UUID 与全部网卡 MAC**）→回读新域 UUID/MAC 同步进 DB→写 DB。Result=`{"vm_id":id}`。
- **clone_image_vm**：payload `{image_id,name,storage_pool,vcpu,memory_mb,network,cloud_init?}`。从 `handler/image.go CloneVM` 搬运：查镜像→LookupVolByPath→CloneVolumeFromVol（Report 40）→seed（Report 70）→define→DB。Result=`{"vm_id":id}`。
- **stop_vm**：payload `{vm_id}`。从 StopVM 搬运：ShutdownDomain→轮询 15s（每秒 Report 10+i*5）→超时 DestroyDomain→DB 置 shut off。Result=`{"vm":name}`。
- 通用：executor 内**禁止**引用 gin/handler；错误返回 `fmt.Errorf("中文: %w", err)`；payload 字段缺失返回中文参数错误；所有 `h.DB/h.Virt` 改为 `ctx.DB/ctx.Virt`；`randomUUID/randomMAC/validateVMName` 在 tasks 包内自实现小函数（copy 逻辑，勿跨包引用 handler 未导出函数）；未指定存储池时统一取包内常量 `defaultStoragePool = "vmops"`（与 `model.VM.StoragePool` 的 gorm 默认值一致），勿再散落字面量。

### Task.Result 字段一览（P2 新增 `kept_volumes`）

| 任务类型 | Result 字段 | 说明 |
|---|---|---|
| `create_vm` / `clone_vm` / `clone_image_vm` | `vm_id`（number） | 新建 VM 的 DB 主键，前端据此跳详情 |
| `stop_vm` | `vm`（string） | 域名 |
| `delete_vm` | `vm`（string） | 域名 |
| `delete_vm` | **`kept_volumes`（string[]，可选）** | **被守卫保留、刻意未删的卷**；仅在非空时出现。元素格式为 `"<卷名>（<中文原因>）"`，前端任务详情直接展示 |
| `cleanup_volumes` | `pool`（string）、`deleted`（string[]）、`kept`（{name,reason}[]） | 清理孤儿卷：与删卷守卫同一套判定反向使用（零引用才删）。触发端点 POST /api/storage/pools/:name/orphan-cleanup |

### delete_vm 的删卷三重守卫（`shouldKeepVol`，勿回退）

原实现只按「是否在存储池路径下」判断是否删卷，会误删两类共享文件：

- **增量克隆父盘**（真正实现 linked clone 后风险成立）→ backing chain 断裂，所有子机磁盘立刻不可读且不可恢复；
- **镜像库登记的共享基镜像**（「基于云镜像创建」走 `source_image_id` 是**直接引用不拷贝**，见 `execCreateVM`）→ 镜像文件被删而 `images` 表记录仍在，留下悬挂记录，其他引用它的 VM 一并损坏。

现由 `shouldKeepVol(src, volName) string` 判定，返回非空字符串即为「保留原因」，任一命中即跳过该卷：

| 序 | 条件 | 保留原因（逐字，会进 `kept_volumes` 与日志） |
|---|---|---|
| 1 | `poolPath != "" && !strings.HasPrefix(src, poolPath+"/")` | `不在存储池 <池名> 路径下` |
| 2 | `src` 命中 `images` 表登记的 `path` 集合 | `是镜像库登记的共享基镜像` |
| 3 | `ListBackingRefs(pool)[src]` 非空 | `是增量克隆父盘，仍被 N 个子卷依赖（子卷名以「、」连接）` |

- 守卫二的数据源：`ctx.DB.Select("path").Find(&imgs)`；查询失败只打日志 `[tasks] 读取镜像库路径失败，跳过基镜像守卫` 并降级（不阻断删除）。
- 守卫三的数据源：`virt.ListBackingRefs(pool)`（枚举池内所有卷的 `<backingStore><path>`）；失败同样降级为空 map 并打日志 `[tasks] 枚举 backing 引用失败，跳过父盘守卫`。
- 命中守卫时写日志 `[tasks] 保留卷（未删）vm=.. vol=.. 原因=..`，并把 `"<卷名>（<原因>）"` 追加进 `kept_volumes`。
- 进度文案随之分叉：有保留卷时 Report 70 为 `磁盘清理完成（保留 N 个共享卷）`，否则仍为 `磁盘清理完成`。
- 删卷失败不再静默：`DeleteVolume` 的错误会打日志 `[tasks] 删除卷失败 ...`（原为 `_ =` 丢弃）。
- **已知取舍**：父盘守卫会导致「先删父机、再删子机」的顺序下，父盘文件残留在池里成为孤儿文件（已无任何域引用它）。这是刻意选择——宁可留一个垃圾文件，也不能损坏正在使用的虚拟机磁盘。孤儿卷清理列入后续工作。

## REST（handler/task.go + handler/vm.go + handler/image.go + main.go，T3 产出）

| Method | Path | 说明 |
|---|---|---|
| POST | `/api/vms` | 改异步：校验基础参数后 Submit(create_vm)，**202 返回 `{task_id}`** |
| DELETE | `/api/vms/:id` | 改异步：Submit(delete_vm)，202 `{task_id}` |
| POST | `/api/vms/:id/clone` | 改异步：Submit(clone_vm)，202 `{task_id}` |
| POST | `/api/images/:id/clone` | 改异步：Submit(clone_image_vm)，202 `{task_id}` |
| POST | `/api/vms/:id/stop` | 改异步：Submit(stop_vm)，202 `{task_id}`（根治 15s 超时） |
| GET | `/api/tasks` | 列表 `?status=&limit=`（默认 50），返回 `{total, items}`（items 不含 payload） |
| GET | `/api/tasks/:id` | 单个任务（含 result/error，不含 payload） |
| DELETE | `/api/tasks/:id` | 仅允许删除 finished（success/failed）的记录 |

- Manager 单例：main.go 初始化 `taskMgr := tasks.NewManager(db)`，`tasks.RegisterVMTasks(taskMgr)`（T2 函数），VMHandler/ImageHandler 持有 `Tasks *tasks.Manager`（构造注入，main.go 接线）。
- database.go 的 AutoMigrate 加 `&model.Task{}`。
- 审计：middleware determineAction 已有 create_vm/delete_vm 等映射，POST/DELETE 路径不变，无需改审计。
- 契约锁定：前端（T4）依赖 `202 {task_id}` 与 `GET /api/tasks/:id → {id,type,title,status,progress,result,error,vm_id,vm_name,created_at}`。
- 路径参数：带 `:id` 的任务提交接口（`DELETE /api/vms/:id`、`POST /api/vms/:id/clone`、`POST /api/vms/:id/stop`、`POST /api/images/:id/clone`）已改用 `paramID` 解析，非法 ID 返回 **400 `ID 参数非法`**（不再落到 404）。`GET`/`DELETE /api/tasks/:id` 沿用自身的 `strconv.ParseUint`，非法 ID 返回 400 `任务 ID 不合法`。详见 api-contract.md 第 0.3 节。
- 队列满时 `Submit` 返回的 task 已是 `status=failed`、`error=任务队列繁忙，请稍后重试`；handler 仍按 202 + `{task_id}` 返回，前端首次轮询即拿到终态与中文原因。

## 前端（T4 产出，文件见任务）

- `api/index.js` 加 `getTask(id)`、`listTasks(params)`；createVM/deleteVM/cloneVM/cloneImage/stopVM 返回改为 `{task_id}`。
- 新建 `web/src/utils/task.js`：`pollTask(taskId, {interval=2000, timeout=300000, onProgress}) → Promise<task>`，轮询到 success resolve、failed reject(Error(message))。
- 调用方改造：VmList.vue（action/bulkAction）、VmDetail.vue（act/doDelete）、CreateVmWizard.vue（create）——提交后禁用按钮+进度提示（ElMessage 或行内 progress），成功刷新列表/跳转，失败展示 error。
