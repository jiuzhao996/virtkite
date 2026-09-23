// Command credential-rekey 把库里已存的 VM SSH 凭据从旧主密钥重加密到新的独立主密钥。
//
// 为什么要它：早期实现把凭据主密钥直接复用 JWT_SECRET_KEY（见 handler.credentialMasterSecret）。
// 部署配置独立的 CREDENTIAL_MASTER_KEY 之后，历史凭据仍是旧密钥（即旧 JWT 密钥）加密的，
// 一旦按安全规范轮换 JWT_SECRET_KEY 就会集体解密失败。本工具「旧密钥解密 → 新密钥重加密」，
// 跑完之后二者彻底解耦：轮换 JWT 不会再动到任何凭据。
//
// 用法（在仓库根目录执行，与主线服务一样读 .env）：
//
//	CREDENTIAL_MASTER_KEY=<新密钥> CREDENTIAL_OLD_MASTER_KEY=<旧密钥> go run ./scripts/credential-rekey
//	CREDENTIAL_MASTER_KEY=<新密钥> CREDENTIAL_OLD_MASTER_KEY=<旧密钥> go run ./scripts/credential-rekey --apply
//
// 环境变量：
//   - CREDENTIAL_MASTER_KEY：必填。迁移目标主密钥，必须与线上服务即将使用的值完全一致。
//   - CREDENTIAL_OLD_MASTER_KEY：可选。旧主密钥；省略时回落 JWT_SECRET_KEY（即历史行为）。
//   - DB_* / SERVER_MODE：与主线服务同款，由 database.Init 读取。
//
// 安全边界：① 默认 dry-run，写库必须显式 --apply；② 单条旧密钥解密失败只跳过不动原记录
// （绝不覆写坏数据）；③ 落库前用新密钥自校验解回同一明文，校验不过就不写；④ 明文只在
// 本进程内存出现，不进日志；⑤ 幂等——已迁移的记录旧密钥解不开，重跑时会被安全跳过。
package main

import (
	"flag"
	"log"
	"os"

	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/database"
	"github.com/jiuzhao/vmops/model"
	"github.com/jiuzhao/vmops/service/secretbox"
	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "真正写库；默认 dry-run，只统计可迁移条数")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		log.Println("警告: .env文件不存在或加载失败")
	}
	config.Init()

	newKey := config.GlobalConfig.CredentialMasterKey
	if newKey == "" {
		log.Fatal("必须设置 CREDENTIAL_MASTER_KEY（要迁移到的新主密钥，须与线上服务使用的值一致）")
	}
	oldKey := os.Getenv("CREDENTIAL_OLD_MASTER_KEY")
	if oldKey == "" {
		// 存量部署从未配过独立主密钥，旧密文就是用当时的 JWT 密钥加密的
		oldKey = config.GlobalConfig.JWTSecretKey
		log.Printf("未设置 CREDENTIAL_OLD_MASTER_KEY，按历史行为取 JWT_SECRET_KEY 作为旧主密钥")
	}
	if newKey == oldKey {
		log.Fatal("新旧主密钥相同，无需迁移：请为 CREDENTIAL_MASTER_KEY 配置一个新值")
	}

	database.Init()
	db := database.GetDB()

	var recs []model.VMCredential
	if err := db.Order("id").Find(&recs).Error; err != nil {
		log.Fatalf("读取凭据记录失败: %v", err)
	}
	log.Printf("共 %d 条凭据（apply=%v）", len(recs), *apply)

	var done, skipped, failed int
	for i := range recs {
		rec := recs[i]
		plain, err := secretbox.OpenWithMaster(oldKey, rec.Salt, rec.PasswordEnc)
		if err != nil {
			// 旧密钥解不开：旧密钥给错，或该条本就是更早遗留的坏记录（也可能是本工具已迁移过）。
			// 一律原样保留——覆写等于把唯一次机会也丢掉。
			skipped++
			log.Printf("跳过 vm_id=%d：旧主密钥解密失败（旧密钥不对 / 记录已损坏 / 已迁移过），原记录不动", rec.VMID)
			continue
		}
		if !*apply {
			done++
			continue
		}
		cipherB64, saltHex, err := secretbox.SealWithMaster(newKey, string(plain))
		if err != nil {
			failed++
			log.Printf("重加密失败 vm_id=%d: %v", rec.VMID, err)
			continue
		}
		// 落库前自校验：新密钥必须能把刚生成的密文解回同一明文，否则宁可不写
		// （写进去解不回来 = 凭据永久丢失，只能让用户重新保存）
		back, err := secretbox.OpenWithMaster(newKey, saltHex, cipherB64)
		if err != nil || string(back) != string(plain) {
			failed++
			log.Printf("重加密自校验失败 vm_id=%d，放弃写入", rec.VMID)
			continue
		}
		if err := db.Model(&model.VMCredential{}).Where("id = ?", rec.ID).
			Updates(map[string]interface{}{"password_enc": cipherB64, "salt": saltHex}).Error; err != nil {
			failed++
			log.Printf("写入失败 vm_id=%d: %v", rec.VMID, err)
			continue
		}
		done++
	}

	if *apply {
		log.Printf("✅ 迁移结束：重加密 %d 条，跳过 %d 条，失败 %d 条", done, skipped, failed)
	} else {
		log.Printf("dry-run：可重加密 %d 条，跳过 %d 条（加 --apply 才写库）", done, skipped)
	}
	if failed > 0 {
		log.Fatalf("有 %d 条处理失败：请按日志排查后重跑（本工具幂等，已迁移的记录会被安全跳过）", failed)
	}
}
