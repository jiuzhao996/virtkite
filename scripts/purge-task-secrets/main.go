// Command purge-task-secrets 清洗 tasks 表里已经落库的 SSH 明文口令。
//
// # 背景
//
// 历史版本的应用安装任务把 SSH 明文口令直接写进 tasks.payload（{"password": "xxx"}），
// vm_create 任务的 cloud_init 子项也带过明文初始口令。代码层面已经修好（新任务只落
// credential_id 或 HTTP 边界加密后的 password_enc + salt），但历史记录仍在库里——
// 数据库一旦泄露，全网虚拟机的 SSH 口令一起泄露。本工具负责清掉这批存量明文。
//
// # 风险（动手前务必读完）
//
//  1. 不可逆：明文从 payload 删除后无法从库内重建，唯一回滚途径是执行前的表备份。
//  2. 历史任务重放会失败：清洗后这些任务只剩地址/端口，没有可用凭据，重跑必然以
//     「缺少 SSH 凭据参数」告终。这是预期的 fail-closed 行为，且现状本就如此——
//     现行 executor（service/tasks/app_tasks.go resolveSSHCredential）已明确拒收明文
//     password，留着明文也换不来重放成功，只是白白留一个泄露面。
//  3. 备份文件本身含明文口令：备份等于把泄露面复制一份，必须 chmod 600、离线保存，
//     清洗完成确认无误后按保密流程销毁。
//  4. 并发改写：执行期间请勿让服务同时重放这批任务。建议先停服或暂停任务 worker，
//     清洗并复查通过后再拉起。
//
// # 建议操作顺序
//
//  0. 停服 / 暂停任务 worker，避免与清洗并发改写同一行。
//  1. 备份 tasks 单表（备份含明文，按涉密件保管）：
//     mysqldump -h <DB_HOST> -u <DB_USER> -p --single-transaction \
//     --set-gtid-purged=OFF --default-character-set=utf8mb4 vmops tasks \
//     > tasks-backup-$(date +%F).sql && chmod 600 tasks-backup-$(date +%F).sql
//  2. 只读体检（默认 dry-run，不写库）：
//     go run ./scripts/purge-task-secrets
//  3. 小批量试写，确认影响面与日志：
//     go run ./scripts/purge-task-secrets --apply --limit 50
//  4. 全量清洗（--limit 分批跑，出错可从 --after 续跑）：
//     go run ./scripts/purge-task-secrets --apply
//  5. 复查：再跑一次 dry-run，命中数应为 0。
//  6. 拉起服务；确认无误后按保密流程销毁备份。
//
// # 回滚方式
//
//   - 整表回滚（推荐，事故时使用）：
//     mysql -h <DB_HOST> -u <DB_USER> -p vmops < tasks-backup-<日期>.sql
//     注意会覆盖清洗期间新增/变更的任务行。
//   - 定点回滚：先按清洗日志里的 ID 列表导出，再导入——
//     mysqldump -h <DB_HOST> -u <DB_USER> -p --where="id IN (<ID 列表>)" vmops tasks > hit.sql
//   - 无备份时无解：明文不可重建，只能让用户重新提交安装任务（走 credential_id 通道）。
//
// 环境变量：DB_HOST / DB_PORT / DB_USER / DB_PASSWORD / DB_NAME（与主线服务同款，
// 同时读仓库根目录的 .env），由 config.Init + database.Init 消费；口令不会打进日志。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/database"
	"github.com/jiuzhao/vmops/model"
	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

const (
	// scanBatch 游标分批扫描时每批读取的条数（只读，与写库批量无关）。
	scanBatch = 500
	// sampleMax dry-run 打印的样例条数上限。
	sampleMax = 5
	// idListMax 变更计划里打印的 ID 条数上限，超出只打印计数与提示。
	idListMax = 50
)

// plainSecretKeys 判定为「明文口令」的键名。password_enc / salt 是现行加密通道的合法字段，
// 不在列表内；isNeverTouch 再做一层兜底，防止将来误加。
var plainSecretKeys = []string{"password", "ssh_password", "passwd", "pwd"}

// nestedSecretScopes payload 里内嵌过明文口令的子对象键（vm_create 的 cloud-init 配置）。
var nestedSecretScopes = []string{"cloud_init"}

// hit 一条命中记录：payload 里确实含明文口令的任务。
type hit struct {
	ID        uint
	Type      string
	Status    string
	CreatedAt string
	Paths     []string       // 命中的键路径：顶层 "password"、嵌套 "cloud_init.password"
	Lens      map[string]int // 键路径 -> 口令字符数（只用于打码展示，不存明文）
	Payload   string         // 库里的原始 payload
	Cleaned   string         // 删除明文键后的 payload
}

func main() {
	apply := flag.Bool("apply", false, "真正写库清除明文口令；默认 dry-run，只扫描统计")
	limit := flag.Int("limit", 0, "本次最多处理的命中条数，0=不限（建议先小批量试写）")
	after := flag.Uint("after", 0, "只扫描/处理 id 大于该值的任务，用于从上次中断处续跑")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		log.Println("警告: .env 文件不存在或加载失败，改用进程环境变量")
	}
	config.Init()
	cfg := config.GlobalConfig
	log.Printf("数据库连接目标: %s:%d/%s (user=%s)", cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBUser)

	database.Init()
	db := database.GetDB()

	hits, scanned, unparsable := scan(db, *after)
	report(hits, scanned, unparsable, *after)
	auditCredentials(db)

	if !*apply {
		log.Println("dry-run 结束：未做任何修改。确认无误后加 --apply 执行清除（先备份 tasks 表）。")
		return
	}
	if len(hits) == 0 {
		log.Println("没有需要清洗的记录，退出。")
		return
	}

	plan := hits
	if *limit > 0 && len(plan) > *limit {
		log.Printf("--limit=%d：本次只处理前 %d 条命中记录，剩余 %d 条留待下一批",
			*limit, *limit, len(plan)-*limit)
		plan = plan[:*limit]
	}
	printPlan(plan)

	done, lastID := purge(db, plan)
	log.Printf("清洗完成：成功 %d 条，最后处理成功的任务 id=%d", done, lastID)
	if *limit > 0 && len(hits) > *limit {
		log.Printf("仍有 %d 条未处理：可再次执行 --apply，或用 --after %d 从断点继续", len(hits)-*limit, lastID)
	}
}

// scan 按 id 升序游标分批扫描 tasks 表，挑出 payload 含明文口令的记录。
// 只读，不做任何改写。
func scan(db *gorm.DB, after uint) (hits []*hit, scanned, unparsable int) {
	lastID := after
	for {
		var rows []model.Task
		if err := db.Where("id > ?", lastID).Order("id").Limit(scanBatch).Find(&rows).Error; err != nil {
			log.Fatalf("扫描 tasks 表失败（已扫 %d 条，游标 id>%d）: %v", scanned, lastID, err)
		}
		if len(rows) == 0 {
			return hits, scanned, unparsable
		}
		for i := range rows {
			row := rows[i]
			scanned++
			lastID = row.ID
			if strings.TrimSpace(row.Payload) == "" {
				continue
			}
			payload, err := decodePayload(row.Payload)
			if err != nil || payload == nil {
				// 非 JSON 对象（空串/数组/历史脏数据）：看不懂就不动，避免写坏
				unparsable++
				log.Printf("跳过 id=%d：payload 不是 JSON 对象，未做任何解析（err=%v）", row.ID, err)
				continue
			}
			paths, lens := stripPlainSecrets(payload)
			if len(paths) == 0 {
				continue
			}
			cleaned, err := json.Marshal(payload)
			if err != nil {
				log.Fatalf("id=%d 的 payload 重新序列化失败，放弃本次运行: %v", row.ID, err)
			}
			hits = append(hits, &hit{
				ID:        row.ID,
				Type:      row.Type,
				Status:    row.Status,
				CreatedAt: row.CreatedAt.Format("2006-01-02 15:04:05"),
				Paths:     paths,
				Lens:      lens,
				Payload:   row.Payload,
				Cleaned:   string(cleaned),
			})
		}
		if len(rows) < scanBatch {
			return hits, scanned, unparsable
		}
	}
}

// decodePayload 把 payload 文本解析成 map。UseNumber 保留数字原始字面量，
// 避免大整数经 float64 往返后精度失真（payload 里有过 vcpu/disk 大数值）。
func decodePayload(raw string) (map[string]interface{}, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var m map[string]interface{}
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// stripPlainSecrets 就地删除 payload（含 cloud_init 子对象）里的明文口令键，
// 返回被删键的路径与各口令长度。密文与盐一律不碰。
func stripPlainSecrets(m map[string]interface{}) (paths []string, lens map[string]int) {
	lens = make(map[string]int)
	for _, key := range plainSecretKeys {
		if isNeverTouch(key) {
			continue
		}
		s, ok := plainString(m[key])
		if !ok {
			continue
		}
		paths = append(paths, key)
		lens[key] = len([]rune(s))
		delete(m, key)
	}
	for _, scope := range nestedSecretScopes {
		sub, ok := m[scope].(map[string]interface{})
		if !ok || sub == nil {
			continue
		}
		for _, key := range plainSecretKeys {
			if isNeverTouch(key) {
				continue
			}
			s, ok := plainString(sub[key])
			if !ok {
				continue
			}
			path := scope + "." + key
			paths = append(paths, path)
			lens[path] = len([]rune(s))
			delete(sub, key)
		}
	}
	return paths, lens
}

// plainString 判定值是否为「非空明文字符串」——只有字符串才可能是明文口令，
// 数字/布尔/null 直接放过，避免误删结构化字段。
func plainString(v interface{}) (string, bool) {
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// isNeverTouch 兜底：密文与盐永不处理，防止将来往 plainSecretKeys 里误加同名键。
func isNeverTouch(key string) bool {
	return key == "salt" || strings.HasSuffix(key, "_enc")
}

// mask 口令打码：只保留长度，绝不以任何前缀/后缀形式把真实口令写进终端或日志。
func mask(n int) string {
	return fmt.Sprintf("***(长度%d)", n)
}

// report 打印扫描统计与打码样例。
func report(hits []*hit, scanned, unparsable int, after uint) {
	log.Printf("扫描 tasks 表：共 %d 条（id>%d），其中 %d 条命中明文口令，%d 条 payload 非 JSON 对象已跳过",
		scanned, after, len(hits), unparsable)
	if len(hits) == 0 {
		log.Println("未发现明文口令，历史 payload 已是干净状态。")
		return
	}
	byType := make(map[string]int)
	byPath := make(map[string]int)
	for _, h := range hits {
		byType[h.Type]++
		for _, p := range h.Paths {
			byPath[p]++
		}
	}
	log.Printf("按任务类型分组（%d 类）:", len(byType))
	for _, t := range sortedKeys(byType) {
		log.Printf("  type=%-16s 命中 %d 条", t, byType[t])
	}
	log.Printf("按明文键路径分组（%d 类）:", len(byPath))
	for _, p := range sortedKeys(byPath) {
		log.Printf("  %-24s 命中 %d 条", p, byPath[p])
	}
	log.Printf("样例（最多 %d 条，口令一律打码）:", sampleMax)
	for i, h := range hits {
		if i >= sampleMax {
			break
		}
		segs := make([]string, 0, len(h.Paths))
		for _, p := range h.Paths {
			segs = append(segs, p+"="+mask(h.Lens[p]))
		}
		log.Printf("  #%d id=%d type=%s status=%s 创建于 %s 命中: %s",
			i+1, h.ID, h.Type, h.Status, h.CreatedAt, strings.Join(segs, ", "))
	}
}

// printPlan 在写库前把将要影响的行数与 ID 列表打出来。
func printPlan(plan []*hit) {
	log.Printf("即将清除 %d 条任务 payload 中的明文口令，任务 ID 如下（最多显示 %d 个）:", len(plan), idListMax)
	ids := make([]string, 0, len(plan))
	for _, h := range plan {
		ids = append(ids, fmt.Sprintf("%d", h.ID))
	}
	if len(ids) > idListMax {
		log.Printf("  %s …（其余 %d 个省略）", strings.Join(ids[:idListMax], ", "), len(ids)-idListMax)
		return
	}
	log.Printf("  %s", strings.Join(ids, ", "))
}

// purge 逐条改写：单条一个小事务，改完立刻回读校验；任何一条出错即中止并汇报进度。
// 返回成功条数与最后成功的任务 ID（供 --after 续跑）。
func purge(db *gorm.DB, plan []*hit) (done int, lastID uint) {
	for _, h := range plan {
		// 乐观锁：带上原始 payload，避免覆盖执行期间被别的进程改过的行
		res := db.Model(&model.Task{}).
			Where("id = ? AND payload = ?", h.ID, h.Payload).
			Update("payload", h.Cleaned)
		if res.Error != nil {
			log.Fatalf("中止：更新 id=%d 失败（已成功 %d 条，最后成功 id=%d）: %v",
				h.ID, done, lastID, res.Error)
		}
		if res.RowsAffected != 1 {
			log.Fatalf("中止：id=%d 未命中或已被其他进程改写（已成功 %d 条，最后成功 id=%d，可用 --after %d 续跑）",
				h.ID, done, lastID, lastID)
		}
		// 回读校验：确认明文键真的没了，而不是写了个空转
		var after model.Task
		if err := db.Select("id", "payload").Where("id = ?", h.ID).First(&after).Error; err != nil {
			log.Fatalf("中止：回读 id=%d 失败（已成功 %d 条，最后成功 id=%d）: %v",
				h.ID, done, lastID, err)
		}
		recheck, err := decodePayload(after.Payload)
		if err != nil {
			log.Fatalf("中止：id=%d 回读的 payload 不是合法 JSON（已成功 %d 条）: %v", h.ID, done, err)
		}
		if paths, _ := stripPlainSecrets(recheck); len(paths) != 0 {
			log.Fatalf("中止：id=%d 回读仍含明文键 %v，清除未生效（已成功 %d 条，最后成功 id=%d）",
				h.ID, paths, done, lastID)
		}
		done++
		lastID = h.ID
		if done%100 == 0 {
			log.Printf("进度：已清洗 %d/%d 条（当前 id=%d）", done, len(plan), h.ID)
		}
	}
	return done, lastID
}

// auditCredentials 只读自检 vm_credentials：该表按设计只存 AES-256-GCM 密文与盐，
// 本脚本不处理它，这里只确认没有「空密文 / 盐长度异常」的坏行需要人工介入。
func auditCredentials(db *gorm.DB) {
	var total int64
	if err := db.Model(&model.VMCredential{}).Count(&total).Error; err != nil {
		log.Printf("跳过 vm_credentials 自检（读取失败）: %v", err)
		return
	}
	var bad int64
	if err := db.Model(&model.VMCredential{}).
		Where("password_enc = '' OR CHAR_LENGTH(salt) <> 64").Count(&bad).Error; err != nil {
		log.Printf("跳过 vm_credentials 坏行统计（查询失败）: %v", err)
		return
	}
	log.Printf("vm_credentials 只读自检：共 %d 条凭据，按设计仅存密文+盐，本脚本不改写该表；异常行 %d 条", total, bad)
	if bad > 0 {
		log.Printf("  提示：上述 %d 条凭据密文为空或盐长度异常，需人工确认（可能来自更早的坏数据）", bad)
	}
}

// sortedKeys 把统计表的键排序后输出，保证多次运行日志顺序稳定可比对。
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
