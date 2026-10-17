package handler

// image_manage.go 镜像的写入运维：multipart 上传磁盘镜像、登记既有存储卷为云镜像、
// 删除镜像（软删除 DB 记录 + 引用守卫后删磁盘文件）。镜像库池约定与列表见 image.go。
//
// 三个入口的共同安全底线：
//   - 落盘/登记的路径必须位于已登记的存储池内（pathInAnyPool），否则任意宿主机
//     文件都能被登记后挂进 VM 读出来；
//   - 文件名/路径做穿越清洗，池路径比较带 "/" 边界（防 images-evil 命中 images）；
//   - 删除前查在用引用（VM 挂载 + 子卷 backing），宁可删不掉也不损坏在用磁盘。

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/virt"
)

// UploadImage 上传镜像（multipart；form 字段：name/file/os_version/is_template/pool）。
// 上传文件落到 pool 指定池（默认 img）的目标路径下，直接作为池卷被 libvirt 识别，
// 不强制走 StorageVolCreateXML（目录池扫描路径即见卷，注释说明）。
// 文件名清洗 + 时间戳防冲突，沿用既有安全命名逻辑。
//
// 流程与失败处理：
//  1. 只自动创建 img 池；显式指定其他池时必须已存在（避免上传动作隐式建池）；
//  2. 文件名取 Base 后清洗，再用纳秒时间戳前缀化，避免同名覆盖；
//  3. io.Copy 流式落盘，不在内存缓存整个镜像；写失败立即删除半成品；
//  4. DB 记录创建失败也删除刚落盘的文件，保证「有记录必有文件」；
//  5. 格式按扩展名推断（raw/vmdk/qcow2），未知扩展名按 qcow2 记录。
func (h *ImageHandler) UploadImage(c *gin.Context) {
	name := c.PostForm("name")
	if name == "" {
		Fail(c, http.StatusBadRequest, "镜像名称不能为空")
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		Fail(c, http.StatusBadRequest, "未找到上传文件")
		return
	}

	osVersion := c.PostForm("os_version")
	isTemplate := c.PostForm("is_template") == "true" || c.PostForm("is_template") == "1"

	// 目标池：默认 img；不存在则自动建目录池（镜像统一存 img 池）。
	// 显式指定其他池时只校验存在性，不自动创建——建池是管理动作，不应由一次
	// 上传请求隐式完成。
	pool := c.PostForm("pool")
	if pool == "" {
		pool = imagePool
	}
	poolPath, err := h.ensureImagePool()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	if pool != imagePool {
		// 用户显式指定其他池：检查存在，不存在则报错（仅 img 池自动创建）
		pools, err := h.Virt.ListPools()
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
		exists := false
		for _, p := range pools {
			if p == pool {
				exists = true
				break
			}
		}
		if !exists {
			Fail(c, http.StatusBadRequest, "存储池 "+pool+" 不存在（默认自动创建 img 池）")
			return
		}
		if poolPath, err = h.Virt.GetPoolPath(pool); err != nil {
			ErrorResponse(c, http.StatusInternalServerError, err)
			return
		}
	}

	// 确保池目录存在（目录池路径若尚未创建则补建）
	if err := os.MkdirAll(poolPath, 0755); err != nil {
		Fail(c, http.StatusInternalServerError, "创建镜像池目录失败")
		return
	}

	// 安全文件名：仅保留基名并清洗，避免目录穿越；加时间戳防冲突
	base := sanitizeFileName(filepath.Base(file.Filename))
	if base == "" {
		base = sanitizeFileName(name) + ".qcow2"
	}
	ext := strings.ToLower(filepath.Ext(base))
	stem := strings.TrimSuffix(base, ext)
	storedName := fmt.Sprintf("%s_%d%s", stem, time.Now().UnixNano(), ext)
	dst := filepath.Join(poolPath, storedName)

	// 流式写入目标文件
	src, err := file.Open()
	if err != nil {
		Fail(c, http.StatusInternalServerError, "打开上传文件失败")
		return
	}
	defer src.Close()

	out, err := os.Create(dst)
	if err != nil {
		Fail(c, http.StatusInternalServerError, "创建目标文件失败")
		return
	}
	defer out.Close()

	written, err := io.Copy(out, src)
	if err != nil {
		os.Remove(dst)
		Fail(c, http.StatusInternalServerError, "写入文件失败")
		return
	}

	// 计算大小(GB)与格式
	sizeGB := float64(written) / (1024.0 * 1024.0 * 1024.0)
	format := "qcow2"
	switch ext {
	case ".raw":
		format = "raw"
	case ".vmdk":
		format = "vmdk"
	case ".qcow2":
		format = "qcow2"
	}

	img := model.Image{
		Name:       name,
		Path:       dst,
		OSVersion:  osVersion,
		SizeGB:     sizeGB,
		Format:     format,
		IsTemplate: isTemplate,
	}
	if err := h.DB.Create(&img).Error; err != nil {
		os.Remove(dst)
		Fail(c, http.StatusInternalServerError, "保存镜像记录失败")
		return
	}

	Created(c, "上传成功", img)
}

// pathInAnyPool 判断绝对路径是否落在任一已登记存储池目录内（带分隔符边界）。
// 包级函数：镜像登记与 AttachDisk/UpdateVMSpec 的磁盘源校验共用同一防线
// （任意绝对路径挂进 VM 等于把宿主机文件读进 guest，全量审计已论证）。
func pathInAnyPool(v *virt.Virt, path string) bool {
	names, err := v.ListPools()
	if err != nil {
		return false
	}
	for _, name := range names {
		if poolPath, err := v.GetPoolPath(name); err == nil && poolPath != "" {
			if strings.HasPrefix(path, strings.TrimRight(poolPath, "/")+"/") {
				return true
			}
		}
	}
	return false
}

// RegisterImage 登记既有存储卷为平台云镜像（POST /api/images/register）。
// 用于把 base 等池里已存在的模板/云镜像纳入镜像库，打通「基于云镜像创建」链路——
// 这些文件是 VM 正在引用或将被 backing 的共享盘，登记只是建目录索引，不复制不移动。
// body: {name*, path*, os_version?, description?, is_template?}；同路径重复登记返回 409。
//
// 去重查询用 Unscoped：软删除记录也参与匹配。若命中已软删的同一路径，视为
// 「恢复登记」（清空 deleted_at 复用原记录），而不是新建一条——否则同一文件会
// 在镜像库里出现两条历史记录。格式按扩展名推断（iso/raw，其余按 qcow2）。
func (h *ImageHandler) RegisterImage(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		Path        string `json:"path" binding:"required"`
		OSVersion   string `json:"os_version"`
		Description string `json:"description"`
		IsTemplate  bool   `json:"is_template"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorWithMessage(c, http.StatusBadRequest, "参数错误", err)
		return
	}
	// path 必须是存在的常规文件：登记的是磁盘索引，指向不存在文件的记录只会制造悬挂引用
	if !filepath.IsAbs(req.Path) || !poolPathRegex.MatchString(req.Path) {
		Fail(c, http.StatusBadRequest, "路径必须是绝对路径且只含合法字符")
		return
	}
	// 路径必须落在已登记存储池内（全量审计：任意绝对路径登记后可挂进 VM 读宿主机文件）
	if !pathInAnyPool(h.Virt, req.Path) {
		Fail(c, http.StatusBadRequest, "路径必须位于已登记的存储池目录内")
		return
	}
	st, err := os.Stat(req.Path)
	if err != nil || st.IsDir() {
		Fail(c, http.StatusBadRequest, "文件不存在或不是常规文件："+req.Path)
		return
	}

	// 同路径去重（含软删除记录——重新登记视为恢复，复用原记录）
	var existing model.Image
	if err := h.DB.Unscoped().Where("path = ?", req.Path).First(&existing).Error; err == nil {
		if existing.DeletedAt.Valid {
			if err := h.DB.Unscoped().Model(&existing).Update("deleted_at", nil).Error; err != nil {
				ErrorWithMessage(c, http.StatusInternalServerError, "恢复镜像登记失败", err)
				return
			}
			Created(c, "已恢复登记", existing)
			return
		}
		Fail(c, http.StatusConflict, "该路径已登记为镜像「"+existing.Name+"」")
		return
	}

	ext := strings.ToLower(filepath.Ext(req.Path))
	format := "qcow2"
	switch ext {
	case ".iso":
		format = "iso"
	case ".raw":
		format = "raw"
	}
	sizeGB := float64(st.Size()) / (1024.0 * 1024.0 * 1024.0)

	img := model.Image{
		Name:        req.Name,
		Path:        req.Path,
		OSVersion:   req.OSVersion,
		SizeGB:      sizeGB,
		Format:      format,
		IsTemplate:  req.IsTemplate,
		Description: req.Description,
	}
	if err := h.DB.Create(&img).Error; err != nil {
		ErrorWithMessage(c, http.StatusInternalServerError, "保存镜像记录失败", err)
		return
	}
	Created(c, "登记成功", img)
}

// DeleteImage 删除镜像（DELETE /api/images/:id，admin）。
//
// 顺序是先软删 DB 记录、再尝试删磁盘文件：记录消失后镜像不再出现在列表/创建
// 选项中，即使文件删除失败也不会被继续使用（文件残留由运维清理）。只删
// img 池或配置的 ImageDir 目录内的文件。
//
// 注意（既有顺序，本注释批次不改行为）：引用检查在软删之后才执行。若命中在用
// 引用返回 409，此时 DB 记录已经被软删——前端看到「拒绝删除」，但镜像实际已
// 从列表消失。正确顺序应是「先查引用、再软删」，后续修复时需一并调整。
func (h *ImageHandler) DeleteImage(c *gin.Context) {
	id, ok := paramID(c, "id")
	if !ok {
		return
	}

	var img model.Image
	if err := h.DB.First(&img, id).Error; err != nil {
		Fail(c, http.StatusNotFound, "镜像不存在")
		return
	}

	// 删除磁盘文件（仅当位于镜像池/镜像目录下，防止误删系统文件）。
	// 删除前必须过引用守卫：该镜像可能正被 VM 以 source_image_id 直接引用（不拷贝），
	// 或是其他卷的 backing 父盘——os.Remove 绕过 libvirt，没有任何报错兜底，删了就是不可逆损坏。
	// 与 storage.DeleteVolume / shouldKeepVol 同一立场：宁可删不掉，不可损坏在用磁盘。
	//
	// ⚠️ 顺序很关键：**先算路径与引用守卫、再软删记录**。原实现先软删后查引用，命中 409 时
	// 记录已被软删除、文件却还在，列表里镜像消失但磁盘仍在（且 VM 的 source_image_id 指向已删记录）。
	poolPath := ""
	if p, err := h.Virt.GetPoolPath(imagePool); err == nil {
		poolPath = p
	}
	inPool, inDir := false, false
	if img.Path != "" {
		// 前缀必须带分隔符边界：否则 /var/lib/libvirt/images-evil/x.qcow2 会命中 /var/lib/libvirt/images（全量审计 P2）
		inPool = poolPath != "" && strings.HasPrefix(img.Path, strings.TrimRight(poolPath, "/")+"/")
		inDir = strings.HasPrefix(img.Path, strings.TrimRight(config.GlobalConfig.ImageDir, "/")+"/")
		if inPool || inDir {
			// 引用查询失败时当前实现按「无引用」放行（fail-open），与删卷守卫
			// shouldKeepVol 的 fail-closed 取向不同；这是既有取舍，记录在此便于审计。
			if refs, refErr := h.imageRefs(img.Path); refErr == nil && refs != "" {
				Fail(c, http.StatusConflict, "镜像正被使用，拒绝删除："+refs)
				return
			}
		}
	}

	// 引用守卫通过后，再软删除数据库记录
	if err := h.DB.Delete(&img).Error; err != nil {
		Fail(c, http.StatusInternalServerError, "删除镜像记录失败")
		return
	}

	// 记录删掉后再删磁盘文件（池内/镜像目录内才删）
	if img.Path != "" && (inPool || inDir) {
		if err := os.Remove(img.Path); err != nil && !os.IsNotExist(err) {
			// 文件删除失败不阻断主流程，但必须留痕（gin 无 Error Handler 消费 c.Errors，原写法=完全静默）
			log.Printf("[image] 删除镜像文件失败 path=%s err=%v", img.Path, err)
		}
	}

	Success(c, gin.H{"message": "镜像已删除"})
}

// imageRefs 计算文件路径的在用引用：被 VM 挂载 / 被任何池内卷当 backing 父盘。
// 返回空串 = 无引用。查不到的部分静默跳过（与删除守卫的保守取向一致——查不到
// 引用时放行删除）。注意：挂载查询失败会返回 error，而 DeleteImage 目前忽略该
// error 并按「无引用」继续（fail-open），见 DeleteImage 内的说明。
//
// 挂载查询覆盖全部域（ListAllDomainDiskSources）；backing 查询只扫 base/images/exten
// 三个常见池——增量克隆父盘基本都落在这里，全池扫描成本更高。若将来新增存放
// 增量卷的池，需要同步扩充这个列表，否则会漏判 backing 引用。
func (h *ImageHandler) imageRefs(path string) (string, error) {
	var refs []string
	sources, err := h.Virt.ListAllDomainDiskSources()
	if err != nil {
		return "", err
	}
	for dom, paths := range sources {
		for _, p := range paths {
			if p == path {
				refs = append(refs, "被虚拟机 "+dom+" 挂载")
			}
		}
	}
	// backing 依赖：扫描常见池（base/images/exten；镜像库池即 base，已在列）
	for _, pool := range []string{"base", "images", "exten"} {
		backing, err := h.Virt.ListBackingRefs(pool)
		if err != nil {
			continue
		}
		if children, ok := backing[path]; ok && len(children) > 0 {
			refs = append(refs, fmt.Sprintf("被 %d 个卷当 backing 父盘", len(children)))
			break
		}
	}
	return strings.Join(refs, "、"), nil
}
