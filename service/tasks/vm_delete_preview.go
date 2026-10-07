// vm_delete_preview.go：删除预检（A 批次）——把「删这台会发生什么」在动手前说清楚。
//
// 为什么单独抽一层：删卷的三重守卫（shouldKeepVol）原先内联在 execDeleteVM 里，
// 用户侧完全看不见（守卫只防平台自伤，不是交互）。预检与真删必须同一套判定，
// 否则会出现「说删 3 块实际删 2 块」的文案漂移——故把判定抽成共享函数，
// 预览与执行共用，只有一处真相。
package tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
	"gorm.io/gorm"
)

// PreviewVolume 单个卷的去向。
type PreviewVolume struct {
	Volume string  `json:"volume"`  // 卷名
	SizeGB float64 `json:"size_gb"` // 容量 GB（拿不到为 0）
	Keep   bool    `json:"keep"`    // true=守卫保留，false=将被物理删除
	Reason string  `json:"reason"`  // 保留原因（守卫命中时）
}

// DeletePreview 删除预检结果（前端确认框直出）。
type DeletePreview struct {
	VMName    string          `json:"vm_name"`
	Pool      string          `json:"pool"`
	RunState  string          `json:"run_state"` // 运行中会在删除时先被强制关机
	Snapshots []string        `json:"snapshots"` // 将被一并丢弃的快照名（域 undefine 时丢失元数据）
	Volumes   []PreviewVolume `json:"volumes"`
	DeleteGB  float64         `json:"delete_gb"` // 将被删除的卷容量合计
	KeepGB    float64         `json:"keep_gb"`   // 被守卫保留的容量合计
}

// VolumeGuard 一次性备齐三重守卫所需数据（与 execDeleteVM 同源口径）。
// 返回的判定函数：reason 为空串表示可删。
type VolumeGuard struct {
	PoolPath    string
	imagePaths  map[string]bool
	backingRefs map[string][]string
	guardsReady bool // 数据源任一失败即 false → 全部保守保留（fail-safe，与执行器同款）
}

// NewVolumeGuard 读取守卫数据。db/v 为 nil 时按「守卫不可用」处理（保守保留）。
func NewVolumeGuard(db *gorm.DB, v *virt.Virt, pool string) *VolumeGuard {
	g := &VolumeGuard{imagePaths: map[string]bool{}, backingRefs: map[string][]string{}, guardsReady: true}
	if v == nil {
		g.guardsReady = false
		return g
	}
	poolPath, err := v.GetPoolPath(pool)
	if err != nil || poolPath == "" {
		g.guardsReady = false
		return g
	}
	g.PoolPath = poolPath
	if db != nil {
		var imgs []model.Image
		if err := db.Select("path").Find(&imgs).Error; err != nil {
			g.guardsReady = false
		} else {
			for _, img := range imgs {
				if img.Path != "" {
					g.imagePaths[img.Path] = true
				}
			}
		}
	} else {
		g.guardsReady = false
	}
	if refs, err := v.ListBackingRefs(pool); err != nil {
		g.guardsReady = false
	} else {
		g.backingRefs = refs
	}
	return g
}

// KeepReason 判定某磁盘源是否必须保留（空串=可删）。语义与 execDeleteVM 的
// shouldKeepVol 完全一致——改动务必同步两处（这是唯一的重复点，故紧邻放置）。
func (g *VolumeGuard) KeepReason(src string) string {
	if !g.guardsReady {
		return "守卫数据不可用（池路径/镜像库/backing 引用获取失败），保守保留待人工确认"
	}
	if g.PoolPath != "" && !strings.HasPrefix(src, g.PoolPath+"/") {
		return "不在存储池路径下"
	}
	if g.imagePaths[src] {
		return "是镜像库登记的共享基镜像"
	}
	if children := g.backingRefs[src]; len(children) > 0 {
		return fmt.Sprintf("是增量克隆父盘，仍被 %d 个子卷依赖（%s）", len(children), strings.Join(children, "、"))
	}
	return ""
}

// BuildDeletePreview 组装删除预检：卷去向（含容量）+ 快照清单 + 运行态。
// 卷枚举口径与 execDeleteVM 一致：域磁盘源优先，再按建卷命名约定兜底三个候选
// （文件不存在时会被容量查询与存在性检查自然滤除）。
func BuildDeletePreview(db *gorm.DB, v *virt.Virt, vm model.VM) *DeletePreview {
	pool := vm.StoragePool
	if pool == "" {
		pool = DefaultStoragePoolResolver()
	}
	pv := &DeletePreview{VMName: vm.Name, Pool: pool, Snapshots: []string{}, Volumes: []PreviewVolume{}}

	// 卷容量索引：跨全部池建索引（VM.StoragePool 字段可能为空或与实际盘位不符——
	// 只查单个池会出现「盘在 images、按 default 查 → 容量未知」，而预检的全部意义
	// 就是要把大小说准；池数量级个位数，多几次 libvirt 调用可接受）
	sizeByVol := map[string]uint64{}
	if v != nil {
		if pools, err := v.ListPools(); err == nil {
			for _, pn := range pools {
				if info, perr := v.GetPoolInfo(pn); perr == nil {
					for _, vol := range info.Volumes {
						if vol.Capacity > sizeByVol[vol.Name] {
							sizeByVol[vol.Name] = vol.Capacity
						}
					}
				}
			}
		}
		if snaps, err := v.ListSnapshots(vm.Name); err == nil {
			for _, s := range snaps {
				pv.Snapshots = append(pv.Snapshots, s.Name)
			}
		}
		if state, err := v.GetDomainState(vm.Name); err == nil {
			pv.RunState = state
		}
	}

	guard := NewVolumeGuard(db, v, pool)

	// 待判定源集合：域磁盘 + 命名兜底候选
	srcs := []string{}
	if v != nil {
		// 全量枚举一次再取本域（virt 层只有跨域版本，与 execDeleteVM 同口径）
		if all, err := v.ListAllDomainDiskSources(); err == nil {
			srcs = append(srcs, all[vm.Name]...)
		}
	}
	if guard.PoolPath != "" {
		for _, cand := range []string{
			vm.Name + ".qcow2",
			vm.Name + "-diska.qcow2",
			vm.Name + "-sys.qcow2",
		} {
			p := filepath.Join(guard.PoolPath, cand)
			if _, err := os.Stat(p); err == nil {
				srcs = append(srcs, p)
			}
		}
	}

	seen := map[string]bool{}
	for _, src := range srcs {
		if src == "" {
			continue
		}
		volName := filepath.Base(src)
		if seen[volName] {
			continue
		}
		seen[volName] = true
		reason := guard.KeepReason(src)
		gb := float64(sizeByVol[volName]) / (1024 * 1024 * 1024)
		pv.Volumes = append(pv.Volumes, PreviewVolume{Volume: volName, SizeGB: gb, Keep: reason != "", Reason: reason})
		if reason != "" {
			pv.KeepGB += gb
		} else {
			pv.DeleteGB += gb
		}
	}
	return pv
}
