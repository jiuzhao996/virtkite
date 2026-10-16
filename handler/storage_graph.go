package handler

// storage_graph.go 存储卷克隆家谱（跨池血缘图）：节点/边/汇总类型与全库图谱聚合端点。
// 供前端回收工作台画「模板/基盘 → 克隆链 → 挂载 VM」血缘图谱并给出孤儿回收候选。

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/model"
)

// LineageNode 克隆家谱节点（一个卷一行）：容量口径双轨——Capacity 是 qcow2 虚拟容量
// （读写上限），Allocation 是实际落盘占用；增量克隆的「虚占」就是两者之差按链聚合。
type LineageNode struct {
	Name         string   `json:"name"`
	Pool         string   `json:"pool"` // 所属池；池外父盘（已删/不在任何激活池）为「(池外)」
	Path         string   `json:"path"`
	CapacityGB   float64  `json:"capacity_gb"`
	AllocationGB float64  `json:"allocation_gb"`
	BackingFile  string   `json:"backing_file,omitempty"` // 非空 = 自己是增量克隆子卷
	VMs          []string `json:"vms"`                    // 直接挂载该卷的虚拟机
	ImageNames   []string `json:"image_names"`            // 镜像库登记名
	IsTemplate   bool     `json:"is_template"`            // 镜像库登记且勾选模板
	Children     []string `json:"children"`               // 以该卷为 backing 父盘的子卷（显示口径 "池名/卷名"，跨池不歧义）
	InUse        bool     `json:"in_use"`
	Phantom      bool     `json:"phantom,omitempty"` // 池外父盘占位节点（卷文件不在任何激活池，容量未知）
}

// LineageEdge 家谱边：parent 卷路径 → child 卷路径（backing 依赖，可跨池；路径即全局节点 id）。
type LineageEdge struct {
	Parent string `json:"parent"`
	Child  string `json:"child"`
}

// LineageSummary 家谱汇总：回收工作台的聚合口径。
type LineageSummary struct {
	TotalVirtualGB float64  `json:"total_virtual_gb"` // Σ 虚拟容量
	TotalActualGB  float64  `json:"total_actual_gb"`  // Σ 实际占用
	SavingsGB      float64  `json:"savings_gb"`       // 虚占节省 = 虚拟 - 实际（增量克隆链的收益）
	OrphanActualGB float64  `json:"orphan_actual_gb"` // 零引用卷的实际占用（可回收）
	Orphans        []string `json:"orphans"`          // 回收候选（"池名/卷名" 全局唯一写法）
	CloneCount     int      `json:"clone_count"`      // 增量克隆子卷数
}

// GetVolumeGraph 全库克隆家谱（GET /api/storage/volume-graph，跨池）。
// 克隆链天然跨池（base 池基盘 → images 池增量子卷），按池建图会丢边，故一次聚合全部激活池。
// 节点以卷路径为全局唯一 id（边用路径连接，规避跨池重名）；数据与删卷守卫同源
// （域磁盘挂载 + 镜像库登记 + ListBackingRefs 依赖图），前端画
// 「模板/基盘 → 克隆链 → 挂载 VM」血缘图谱 + 回收候选聚合。池外父盘（基盘已删但子卷
// 尚在、或父盘在未激活池）生成 phantom 占位节点，图谱不断链。
func (h *StorageHandler) GetVolumeGraph(c *gin.Context) {
	poolNames, err := h.Virt.ListPools()
	if err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "枚举存储池失败", err)
		return
	}

	const gb = 1024.0 * 1024.0 * 1024.0
	nodes := []LineageNode{}
	edges := []LineageEdge{}
	summary := LineageSummary{Orphans: []string{}}
	nodeIdxByPath := map[string]int{}            // 卷路径 → 节点下标（边与池外父盘解析用）
	nameIdxByPool := map[string]map[string]int{} // 池名 → 卷名 → 节点下标（子卷名反查用）
	// 全局域磁盘挂载（一次查询跨池共用）；镜像库登记即全局索引，跨池匹配
	allDisks, _ := h.Virt.ListAllDomainDiskSources()
	imgByPath := map[string][]string{}
	tplByPath := map[string]bool{}
	var images []model.Image
	if h.DB != nil {
		if err := h.DB.Find(&images).Error; err == nil {
			for _, im := range images {
				imgByPath[im.Path] = append(imgByPath[im.Path], im.Name)
				if im.IsTemplate {
					tplByPath[im.Path] = true
				}
			}
		}
	}

	for _, poolName := range poolNames {
		pool, err := h.Virt.GetPoolInfo(poolName)
		if err != nil || !pool.Active {
			continue // 单池失败/未激活不拖垮全局
		}
		if nameIdxByPool[poolName] == nil {
			nameIdxByPool[poolName] = map[string]int{}
		}
		for _, vol := range pool.Volumes {
			vms := []string{}
			for domName, paths := range allDisks {
				for _, p := range paths {
					if p == vol.Path {
						vms = append(vms, domName)
						break
					}
				}
			}
			node := LineageNode{
				Name:         vol.Name,
				Pool:         poolName,
				Path:         vol.Path,
				CapacityGB:   float64(vol.Capacity) / gb,
				AllocationGB: float64(vol.Allocation) / gb,
				BackingFile:  vol.BackingFile,
				VMs:          vms,
				ImageNames:   imgByPath[vol.Path],
				IsTemplate:   tplByPath[vol.Path],
				Children:     []string{},
			}
			if node.ImageNames == nil {
				node.ImageNames = []string{}
			}
			summary.TotalVirtualGB += node.CapacityGB
			summary.TotalActualGB += node.AllocationGB
			if node.BackingFile != "" {
				summary.CloneCount++
			}
			nameIdxByPool[poolName][vol.Name] = len(nodes)
			nodeIdxByPath[vol.Path] = len(nodes)
			nodes = append(nodes, node)
		}
	}

	// 跨池边：逐池解析 backing 依赖（键=父盘路径，值=子卷名）。子卷在本池按名反查得路径；
	// 父端经路径索引跨池解析，解析不到（基盘已删/在未激活池）生成 phantom 占位节点，图谱不断链。
	seenEdge := map[string]bool{}
	for _, poolName := range poolNames {
		backing, err := h.Virt.ListBackingRefs(poolName)
		if err != nil {
			continue
		}
		for parentPath, children := range backing {
			parentIdx, ok := nodeIdxByPath[parentPath]
			if !ok {
				base := parentPath
				if i := strings.LastIndex(base, "/"); i >= 0 {
					base = base[i+1:]
				}
				parentIdx = len(nodes)
				node := LineageNode{
					Name: base, Pool: "(池外)", Path: parentPath,
					ImageNames: imgByPath[parentPath], IsTemplate: tplByPath[parentPath],
					Children: []string{}, Phantom: true,
				}
				if node.ImageNames == nil {
					node.ImageNames = []string{}
				}
				nodes = append(nodes, node)
				nodeIdxByPath[parentPath] = parentIdx
			}
			for _, child := range children {
				childIdx, ok := nameIdxByPool[poolName][child]
				if !ok {
					continue
				}
				key := parentPath + "->" + nodes[childIdx].Path
				if seenEdge[key] {
					continue
				}
				seenEdge[key] = true
				edges = append(edges, LineageEdge{Parent: parentPath, Child: nodes[childIdx].Path})
				// 父节点 Children 补全（显示口径带池名前缀，跨池不歧义）
				nodes[parentIdx].Children = append(nodes[parentIdx].Children, poolName+"/"+child)
			}
		}
	}

	// InUse 收口：三重引用 = 挂载 VM / 镜像库登记 / 是任何子卷的 backing 父盘（跨池口径）
	isParent := map[string]bool{}
	for _, e := range edges {
		isParent[e.Parent] = true
	}
	for i := range nodes {
		n := &nodes[i]
		n.InUse = len(n.VMs) > 0 || len(n.ImageNames) > 0 || isParent[n.Path]
		// ISO 安装介质（img 池装机盘）三重引用天然为零，但属刻意留存——不进回收候选，
		// 避免工作台诱导用户删掉数 GB 的装机镜像（重新下载成本高）。
		isISO := strings.HasSuffix(strings.ToLower(n.Name), ".iso")
		if !n.InUse && !n.Phantom && !isISO {
			summary.Orphans = append(summary.Orphans, n.Pool+"/"+n.Name)
			summary.OrphanActualGB += n.AllocationGB
		}
	}
	summary.SavingsGB = summary.TotalVirtualGB - summary.TotalActualGB

	Success(c, gin.H{"nodes": nodes, "edges": edges, "summary": summary})
}
