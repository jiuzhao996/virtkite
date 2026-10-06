package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// execCleanupVolumes 清理存储池孤儿卷（cleanup_volumes 任务）。
// 判定与 execDeleteVM 的 shouldKeepVol 三重守卫同一套数据、反向使用：
// 一个卷【既不被虚拟机挂载、也未登记镜像库、也不是任何子卷的 backing 父盘】即为孤儿，予以删除；
// 任一命中引用则保留并记录原因。宁可漏删，不可错删。
func execCleanupVolumes(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}
	var p struct {
		Pool string `json:"pool"`
	}
	if raw, err := json.Marshal(ctx.Payload); err == nil && len(ctx.Payload) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	pool := p.Pool
	if pool == "" {
		pool = DefaultStoragePoolResolver()
	}

	reportProgress(ctx, 5, "枚举存储池卷")
	poolInfo, err := ctx.Virt.GetPoolInfo(pool)
	if err != nil {
		return fmt.Errorf("获取存储池 %s 信息失败: %w", pool, err)
	}
	// 守卫数据拿不全时拒绝清理（宁可不删，不可错删）：枚举失败直接让任务失败，
	// 按空数据继续会把「未查到的引用」误判成孤儿卷，把仍被依赖的卷删掉。
	backing, err := ctx.Virt.ListBackingRefs(pool)
	if err != nil {
		return fmt.Errorf("枚举 backing 引用失败，守卫数据不完整拒绝清理: %w", err)
	}
	disks, err := ctx.Virt.ListAllDomainDiskSources()
	if err != nil {
		return fmt.Errorf("枚举域磁盘清单失败，守卫数据不完整拒绝清理: %w", err)
	}
	var imgPaths []string
	if err := ctx.DB.Model(&model.Image{}).Pluck("path", &imgPaths).Error; err != nil {
		return fmt.Errorf("查询镜像库路径失败: %w", err)
	}
	imgSet := make(map[string]bool, len(imgPaths))
	for _, path := range imgPaths {
		imgSet[path] = true
	}

	total := len(poolInfo.Volumes)
	deleted := []string{}
	kept := []map[string]string{}
	for i, vol := range poolInfo.Volumes {
		var reasons []string
		for domName, paths := range disks {
			hit := false
			for _, dp := range paths {
				if dp == vol.Path {
					reasons = append(reasons, "仍被虚拟机挂载（"+domName+"）")
					hit = true
					break
				}
			}
			if hit {
				break
			}
		}
		if imgSet[vol.Path] {
			reasons = append(reasons, "已登记为镜像库镜像")
		}
		if kids := backing[vol.Path]; len(kids) > 0 {
			reasons = append(reasons, fmt.Sprintf("是增量克隆父盘，仍被 %d 个子卷依赖", len(kids)))
		}

		if len(reasons) > 0 {
			kept = append(kept, map[string]string{"name": vol.Name, "reason": strings.Join(reasons, "；")})
		} else if err := ctx.Virt.DeleteVolume(pool, vol.Name); err != nil {
			kept = append(kept, map[string]string{"name": vol.Name, "reason": "删除失败: " + err.Error()})
		} else {
			deleted = append(deleted, vol.Name)
		}
		if total > 0 {
			reportProgress(ctx, 10+80*(i+1)/total, fmt.Sprintf("已处理 %d/%d 个卷", i+1, total))
		}
	}

	if ctx.Task != nil {
		if b, err := json.Marshal(map[string]interface{}{
			"pool":    pool,
			"deleted": deleted,
			"kept":    kept,
		}); err == nil {
			ctx.Task.Result = string(b)
		}
	}
	if len(deleted) == 0 && len(kept) > 0 {
		// 没删任何东西但全部有引用：任务成功，原因在 result.kept
		reportProgress(ctx, 100, fmt.Sprintf("%d 个卷全部有引用，无需清理", len(kept)))
		return nil
	}
	reportProgress(ctx, 100, fmt.Sprintf("清理完成：删除 %d 个孤儿卷，保留 %d 个", len(deleted), len(kept)))
	return nil
}

// execDeleteVM 删除虚拟机（对应 virsh undefine + virsh vol-delete）。
// payload：{vm_id*}。
func execDeleteVM(ctx *ExecContext) error {
	if err := checkExecContext(ctx); err != nil {
		return err
	}

	vmID, ok := intParam(ctx.Payload, "vm_id")
	if !ok || vmID <= 0 {
		return errors.New("缺少虚拟机 ID 参数")
	}

	var vm model.VM
	if err := ctx.DB.First(&vm, uint(vmID)).Error; err != nil {
		return fmt.Errorf("虚拟机不存在: %w", err)
	}
	reportProgress(ctx, 10, "开始删除虚拟机")

	// 1. 先取完整磁盘清单（含多盘/克隆卷/seed 盘），再删除域定义
	//    （spec 解析失败不阻断删除，域仍按既有流程清理）。
	var diskSources []string
	if spec, err := ctx.Virt.GetDomainSpec(vm.Name); err == nil && spec != nil {
		for _, d := range spec.Disks {
			if d.Source != "" {
				diskSources = append(diskSources, d.Source)
			}
		}
	}

	// 2. 删除域定义（对应 virsh undefine）。
	// 域在 libvirt 已不存在时容错继续：回收站「恢复」只清 deleted_at（域删除时已 undefine），
	// 恢复过的 VM 再次删除时 undefine 必报「不存在」——若因此中止，该记录删除失败、
	// purge 又报「不在回收站中」，成为用户无解的僵尸行。仅对 VIR_ERR_NO_DOMAIN 放行，
	// 其余错误（连接失败、权限等）仍中止删除。
	if err := ctx.Virt.UndefineDomain(vm.Name); err != nil {
		if !virt.IsDomainNotFound(err) {
			// 失败上下文进 Result（manager 失败路径会落库）：此时域与磁盘都未动、
			// 状态一致，直接重试即可——用户看到失败往往不敢重试，说明这一点
			setTaskResultVM(ctx, map[string]interface{}{
				"stage": "undefine",
				"note":  "域定义删除失败，磁盘与记录均未清理，状态完整，可直接重试删除",
			}, vm.ID, vm.Name)
			return fmt.Errorf("删除虚拟机定义失败: %w", err)
		}
		log.Printf("[tasks] 域定义已不存在，按已 undefine 处理（回收站恢复后的二次删除场景） vm=%s", vm.Name)
	}
	reportProgress(ctx, 30, "虚拟机定义已删除（对应 virsh undefine），开始清理磁盘")

	// 3. 删除存储卷（对应 virsh vol-delete）：枚举的磁盘源 + 默认系统盘兜底。
	//    删除前有三重守卫，任一命中即跳过该卷并记录原因（见 shouldKeepVol）。
	pool := vm.StoragePool
	if pool == "" {
		pool = DefaultStoragePoolResolver()
	}
	// 守卫数据完整性标记（fail-safe）：三重守卫任一数据源获取失败即整体放弃删卷。
	// 此前失败后按空数据继续等于守卫整体失效——最坏情况增量克隆父盘被当孤儿删掉，
	// 所有子机磁盘立即不可读且不可恢复。守卫不可用 ⇒ 全部卷保守保留进 kept_volumes，
	// 域与 DB 记录照常清理，残留卷留给孤儿清理任务或人工处理。
	guardsReady := true

	// 池路径前缀（用于判定卷是否属于平台托管，避免删池外文件）。
	poolPath, pathErr := ctx.Virt.GetPoolPath(pool)
	if pathErr != nil || poolPath == "" {
		log.Printf("[tasks] 获取池路径失败（err=%v path=%q），本次保守保留全部卷 vm=%s pool=%s", pathErr, poolPath, vm.Name, pool)
		guardsReady = false
	}

	// 守卫二的数据：平台镜像库登记的文件路径集合。
	// 「基于云镜像创建」是直接引用不拷贝（见 execCreateVM 的 source_image_id 分支），
	// 若镜像与 VM 同池，按池路径判定会把基镜像本体当成该 VM 的盘删掉，
	// 而 images 表记录仍在 —— 留下悬挂记录且其他引用它的 VM 一并损坏。
	managedImagePaths := map[string]bool{}
	var imgs []model.Image
	if err := ctx.DB.Select("path").Find(&imgs).Error; err != nil {
		log.Printf("[tasks] 读取镜像库路径失败，本次保守保留全部卷 vm=%s err=%v", vm.Name, err)
		guardsReady = false
	}
	for _, img := range imgs {
		if img.Path != "" {
			managedImagePaths[img.Path] = true
		}
	}

	// 守卫三的数据：池内 qcow2 backing file 引用（父卷路径 → 依赖它的子卷）。
	backingRefs, err := ctx.Virt.ListBackingRefs(pool)
	if err != nil {
		log.Printf("[tasks] 枚举 backing 引用失败，本次保守保留全部卷 vm=%s pool=%s err=%v", vm.Name, pool, err)
		backingRefs = map[string][]string{}
		guardsReady = false
	}

	var keptVols []string
	// shouldKeepVol 判断某个磁盘源是否必须保留，返回保留原因（空串表示可删）。
	shouldKeepVol := func(src, volName string) string {
		// 守卫零（fail-safe）：守卫数据源获取失败时不得删除任何卷，理由进 kept_volumes 供人工确认
		if !guardsReady {
			return "守卫数据不可用（池路径/镜像库/backing 引用获取失败），保守保留待人工确认"
		}
		// 守卫一：池外文件不属于平台托管，一律不动（如挂载的宿主机 ISO）
		if poolPath != "" && !strings.HasPrefix(src, poolPath+"/") {
			return "不在存储池 " + pool + " 路径下"
		}
		// 守卫二：镜像库登记的共享基镜像
		if managedImagePaths[src] {
			return "是镜像库登记的共享基镜像"
		}
		// 守卫三：仍被子卷当作 qcow2 backing file（增量克隆父盘）
		if children := backingRefs[src]; len(children) > 0 {
			return fmt.Sprintf("是增量克隆父盘，仍被 %d 个子卷依赖（%s）",
				len(children), strings.Join(children, "、"))
		}
		return ""
	}

	seen := map[string]bool{}
	tryDeleteVol := func(src string) {
		if src == "" {
			return
		}
		volName := filepath.Base(src)
		if seen[volName] {
			return
		}
		seen[volName] = true
		if reason := shouldKeepVol(src, volName); reason != "" {
			log.Printf("[tasks] 保留卷（未删）vm=%s vol=%s 原因=%s", vm.Name, volName, reason)
			keptVols = append(keptVols, volName+"（"+reason+"）")
			return
		}
		// libvirt 卷（克隆卷等 root 属主）走 vol-delete；seed 等直接落盘文件 libvirt 不认作卷，os 兜底删文件。
		if err := ctx.Virt.DeleteVolume(pool, volName); err != nil {
			log.Printf("[tasks] 删除卷失败 vm=%s pool=%s vol=%s err=%v", vm.Name, pool, volName, err)
		}
		if poolPath != "" {
			_ = os.Remove(filepath.Join(poolPath, volName))
		}
	}
	for _, src := range diskSources {
		tryDeleteVol(src)
	}
	if poolPath != "" {
		// 域配置拿不到（已 undefine 的二次删除）或解析失败时，按建卷命名约定兜底找系统盘：
		// 新建机 <vm名>.qcow2 / 克隆机 <vm名>-diska.qcow2（CloneVMFromSpec）/ 镜像建机 <vm名>-sys.qcow2
		// （execCloneImageVM）。文件不存在时 DeleteVolume 与 os.Remove 均静默无副作用；
		// 盘有归属时 seen 去重 + shouldKeepVol 守卫照常生效。
		for _, cand := range []string{
			vm.Name + ".qcow2",
			vm.Name + "-diska.qcow2",
			vm.Name + "-sys.qcow2",
		} {
			tryDeleteVol(filepath.Join(poolPath, cand))
		}
	}
	if len(keptVols) > 0 {
		reportProgress(ctx, 70, "磁盘清理完成（保留 "+strconv.Itoa(len(keptVols))+" 个共享卷）")
	} else {
		reportProgress(ctx, 70, "磁盘清理完成")
	}

	// 4. 清理 cloud-init seed 镜像（独立 seed 目录，非池卷）。
	if seedDir := taskSeedDir(); seedDir != "" {
		_ = os.Remove(filepath.Join(seedDir, vm.Name+"-seed.iso"))
	}

	// 5. 软删除数据库记录，并回收该 VM 的全部授权（授权随资产消亡，不悬挂）。
	if err := ctx.DB.Delete(&vm).Error; err != nil {
		// 失败上下文进 Result：域与磁盘已清理、仅记录未删，重试会走
		//「域不存在容错」路径继续收尾——不写明的话用户以为失败后不能重试
		setTaskResultVM(ctx, map[string]interface{}{
			"stage": "db_record",
			"note":  "域定义与磁盘已清理，仅数据库记录删除失败；重试删除可继续完成收尾",
		}, vm.ID, vm.Name)
		return fmt.Errorf("删除虚拟机记录失败: %w", err)
	}
	if err := ctx.DB.Where("vm_id = ?", vm.ID).Delete(&model.VMGrant{}).Error; err != nil {
		log.Printf("[tasks] 回收授权失败（VM 已删，授权悬挂）vm=%s err=%v", vm.Name, err)
	}
	// 组级授权同批回收（授权随资产消亡）
	if err := ctx.DB.Where("vm_id = ?", vm.ID).Delete(&model.VMGroupGrant{}).Error; err != nil {
		log.Printf("[tasks] 回收组授权失败（VM 已删，授权悬挂）vm=%s err=%v", vm.Name, err)
	}

	// 结果里带上被守卫保留的卷，让用户知道哪些共享文件刻意没删（前端任务详情可见）
	result := map[string]interface{}{"vm": vm.Name}
	if len(keptVols) > 0 {
		result["kept_volumes"] = keptVols
	}
	setTaskResultVM(ctx, result, vm.ID, vm.Name)
	return nil
}
