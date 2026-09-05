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
    Type      string    `gorm:"size:50;index" json:"type"`     // create_vm / delete_vm / clone_vm / clone_image_vm / stop_vm
    Title     string    `gorm:"size:200" json:"title"`         // 如 "创建虚拟机 smoke-01"
    Status    string    `gorm:"size:20;index" json:"status"`   // pending / running / success / failed
    Progress  int       `gorm:"default:0" json:"progress"`    // 0-100
    Payload   string    `gorm:"type:text" json:"-"`           // 执行参数 JSON（内部用，不返回前端）
    Result    string    `gorm:"type:text" json:"result,omitempty"` // 成功结果 JSON，如 {"vm_id":123}
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

type Manager struct { /* DB, Virt, queue chan uint(taskID), workers, executors map[string]Executor, quit */ }
func NewManager(db *gorm.DB) *Manager
// NewManager 内部 virt.New()、起 4 个 worker goroutine、注册 shutdown hook 无需（进程退出即弃）。
// worker 循环：取 taskID → DB 读 task → 置 running → 查 executors[task.Type] → 执行 → 置 success/failed。
// Report(pct,msg)：UPDATE tasks SET progress=pct WHERE id（msg 忽略或记日志，暂不存）。
func (m *Manager) Register(taskType string, fn Executor)
func (m *Manager) Submit(taskType, title string, payload interface{}, userID *uint, username, vmName string, vmID *uint) (*model.Task, error)
// Submit：payload 序列化 JSON 存 Payload，Status=pending，入 queue（queue 满则直接起 goroutine 执行，保证不阻塞）。
func (m *Manager) Get(id uint) (*model.Task, error)
func (m *Manager) List(limit int, status string) ([]model.Task, error) // 按 id desc，status 为空查全部
```

## Executors（service/tasks/vm_tasks.go，T2 产出）

```go
func RegisterVMTasks(m *Manager) // 注册 create_vm / delete_vm / clone_vm / clone_image_vm / stop_vm
```

- **create_vm**：payload = 原 CreateVM 请求体 JSON（含 name/storage_pool/vcpu/memory_mb/disks/interfaces/network/iso_path/cloud_init/host_id）。逻辑从 `handler/vm.go CreateVM` 整体搬运：校验名称→查 host（host_id 缺省 firstHost）→默认值→UUID/MAC→组装 spec→逐盘落地（Report 10/30/50/70）→seed→BuildDomainXML→写 DB→DefineDomain。成功置 `ctx.Task.Result={"vm_id":id}`，并回填 Task.VMID/VMName。失败清理已建卷+seed（沿用原 cleanup）。
- **delete_vm**：payload `{vm_id}`。逻辑从 DeleteVM 搬运：查 VM→取 spec 枚举磁盘→UndefineDomain（Report 30）→逐卷清理+seed（Report 70）→DB 软删除。成功 Result=`{"vm":name}`。
- **clone_vm**：payload `{source_id,name,storage_pool,vcpu,memory_mb,network}`。从 CloneVM 搬运：查源→GetDomainSpec→覆盖→CloneVMFromSpec（Report 50）→查新域 UUID/MAC→写 DB。Result=`{"vm_id":id}`。
- **clone_image_vm**：payload `{image_id,name,storage_pool,vcpu,memory_mb,network,cloud_init?}`。从 `handler/image.go CloneVM` 搬运：查镜像→LookupVolByPath→CloneVolumeFromVol（Report 40）→seed（Report 70）→define→DB。Result=`{"vm_id":id}`。
- **stop_vm**：payload `{vm_id}`。从 StopVM 搬运：ShutdownDomain→轮询 15s（每秒 Report 10+i*5）→超时 DestroyDomain→DB 置 shut off。Result=`{"vm":name}`。
- 通用：executor 内**禁止**引用 gin/handler；错误返回 `fmt.Errorf("中文: %w", err)`；payload 字段缺失返回中文参数错误；所有 `h.DB/h.Virt` 改为 `ctx.DB/ctx.Virt`；`randomUUID/randomMAC/validateVMName` 在 tasks 包内自实现小函数（copy 逻辑，勿跨包引用 handler 未导出函数）。

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

## 前端（T4 产出，文件见任务）

- `api/index.js` 加 `getTask(id)`、`listTasks(params)`；createVM/deleteVM/cloneVM/cloneImage/stopVM 返回改为 `{task_id}`。
- 新建 `web/src/utils/task.js`：`pollTask(taskId, {interval=2000, timeout=300000, onProgress}) → Promise<task>`，轮询到 success resolve、failed reject(Error(message))。
- 调用方改造：VmList.vue（action/bulkAction）、VmDetail.vue（act/doDelete）、CreateVmWizard.vue（create）——提交后禁用按钮+进度提示（ElMessage 或行内 progress），成功刷新列表/跳转，失败展示 error。
