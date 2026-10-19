package handler

// vm_files_offline_guard.go 离线挂载的进程生命周期守卫：启动时扫描挂载基目录清理上次进程遗留的
// FUSE 挂载点，进程收到 SIGINT/SIGTERM 时统一卸载内存中登记的全部离线挂载，避免宿主机残留挂载点
// 占用 VM 磁盘（该 VM 的启动/迁移/删除会全线失败且原因不明）。挂载/卸载主流程见 vm_files_offline.go。

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

// StartOfflineMountGuard 离线挂载生命周期守卫：启动对账 + 退出钩子。
//
// 必须由路由注册（handler.RegisterAll）在任何挂载可能发生之前调用一次：
//   - reclaimStaleOfflineMounts：扫描 offlineMountBase，卸载上次进程遗留的 FUSE 挂载点；
//   - watchOfflineMountSignals：拦截 SIGINT/SIGTERM，退出前卸载内存里的全部离线挂载。
//
// 不做后台定时对账（宁可少一层魔法）：正常路径卸载走 OfflineUnmount，异常路径由这两处兜住。
func StartOfflineMountGuard() {
	reclaimStaleOfflineMounts()
	go watchOfflineMountSignals()
}

// watchOfflineMountSignals 进程退出钩子：收到 SIGINT/SIGTERM 先卸载全部离线挂载，
// 再把信号按默认语义重发给本进程（signal.Reset + syscall.Kill）。
//
// 为什么重发而不是直接 os.Exit：signal.Notify 会吞掉信号的默认终止行为，只清理不退出会让
// 服务变成「杀不死」；重发则保留原有退出语义（退出码 130/143），且不会抢占将来可能加入的
// 优雅停机逻辑。只处理一次即 signal.Stop，避免重发的信号又被自己接住。
func watchOfflineMountSignals() {
	// 后台 goroutine 的 panic 会终止进程；退出钩子异常时仍须把信号重发出去，
	// 否则服务会变成「杀不死」。
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[vm-files] 退出钩子 panic（已兜底）: %v\n%s", r, debug.Stack())
			signal.Reset(syscall.SIGINT, syscall.SIGTERM)
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		}
	}()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	sig := <-ch
	signal.Stop(ch)
	log.Printf("🧹 [vm-files] 收到信号 %v，卸载全部离线挂载（共 %d 个）", sig, offlineMountCount())
	unmountAllOfflineMounts()
	s, ok := sig.(syscall.Signal)
	if !ok {
		return
	}
	signal.Reset(syscall.SIGINT, syscall.SIGTERM)
	_ = syscall.Kill(syscall.Getpid(), s)
}

// unmountAllOfflineMounts 卸载内存态里的全部离线挂载（退出/回收共用），并清空登记表。
// 逐条独立尝试：一条失败不拖累其余（少残一条是一条），失败只留日志（退出路径不能卡住）。
func unmountAllOfflineMounts() {
	offlineMu.Lock()
	ms := make([]*offlineMount, 0, len(offlineMounts))
	for _, m := range offlineMounts {
		ms = append(ms, m)
	}
	offlineMounts = map[uint]*offlineMount{}
	offlineMu.Unlock()
	for _, m := range ms {
		forceUnmountOffline(m.Mountpoint)
	}
}

// offlineMountCount 当前登记数（仅日志用）。
func offlineMountCount() int {
	offlineMu.Lock()
	defer offlineMu.Unlock()
	return len(offlineMounts)
}

// forceUnmountOffline 无条件卸载一个挂载点（不经 HTTP、不管登记表）。
func forceUnmountOffline(mountpoint string) {
	if _, err := sudoRun(30*time.Second, "fusermount", "-u", mountpoint); err != nil {
		log.Printf("⚠️ [vm-files] 卸载离线挂载失败 %s: %v（残留需人工执行：sudo fusermount -u %s）", mountpoint, err, mountpoint)
		return
	}
	_ = os.RemoveAll(mountpoint)
	log.Printf("🧹 [vm-files] 已卸载离线挂载 %s", mountpoint)
}

// reclaimStaleOfflineMounts 启动对账：清理上次进程残留的挂载点。
// 判据是 /proc/self/mountinfo（无需 sudo、只读内核态事实），不是目录是否存在——
// 目录存在但未挂载的只清空目录，避免把正在使用的挂载点误删。
func reclaimStaleOfflineMounts() {
	mounted, err := mountedPoints()
	if err != nil {
		// 拿不到挂载表就放弃本轮：宁可留残（人工可清），不可误删在用挂载点
		log.Printf("⚠️ [vm-files] 读取挂载表失败，跳过离线挂载启动对账: %v", err)
		return
	}
	entries, err := os.ReadDir(offlineMountBase)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("⚠️ [vm-files] 扫描离线挂载目录失败，跳过启动对账: %v", err)
		}
		return
	}
	for _, e := range entries {
		mp := filepath.Join(offlineMountBase, e.Name())
		if !mounted[mp] {
			// 未挂载的空壳目录：清掉以免下次挂载撞上脏目录（os.Remove 遇非空会失败，天然安全）
			_ = os.Remove(mp)
			continue
		}
		log.Printf("⚠️ [vm-files] 发现上次进程遗留的离线挂载 %s，启动对账自动卸载", mp)
		forceUnmountOffline(mp)
	}
}

// mountedPoints 读 /proc/self/mountinfo 取当前挂载点集合（挂载点为第 5 个字段）。
// 只用于判断「某路径现在是否挂着东西」，因此只解析到第 5 段即止，不关心后续可选字段。
func mountedPoints() (map[string]bool, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, fmt.Errorf("打开 /proc/self/mountinfo: %w", err)
	}
	defer f.Close()
	points := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}
		// mountinfo 用 \040 转义路径中的空格（本项目的挂载点不含空格，仍照规矩还原）
		points[strings.ReplaceAll(fields[4], "\\040", " ")] = true
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取 /proc/self/mountinfo: %w", err)
	}
	return points, nil
}
