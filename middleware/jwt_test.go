package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jiuzhao/vmops/config"
	"github.com/jiuzhao/vmops/model"
)

const (
	// testSecret 测试专用签名密钥（与生产默认值无关，避免测试意外依赖内置默认密钥）。
	testSecret = "vmops-unit-test-secret-key-2026"
	// testExpireMinutes 测试用的 token 有效期。
	testExpireMinutes = 60
)

// TestMain 初始化 middleware 包测试环境。
//
// GenerateToken/ParseToken 直接读 config.GlobalConfig，这里**手工赋值结构体**而不调用
// config.Init()：后者会读进程环境变量与 .env，测试结果就跟运行环境绑死了
// （CI 上没有 .env，本机上又可能设了 JWT_SECRET_KEY，同一份测试会得到两种行为）。
//
// 说明：本包测试全部不用 t.Parallel()。多个用例会临时改写 config.GlobalConfig 的字段
// （过期时间、密钥），并行执行必然互相打断，-race 下也会直接报数据竞争。
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	log.SetOutput(io.Discard)
	config.GlobalConfig = &config.Config{
		JWTSecretKey:     testSecret,
		JWTExpireMinutes: testExpireMinutes,
	}
	code := m.Run()
	log.SetOutput(os.Stderr)
	os.Exit(code)
}

// ============================ 口令哈希 ============================

// TestHashPasswordAndCheckPassword 覆盖 bcrypt 口令哈希与校验。
//
// 风险点：这是登录链路唯一的口令验证点。哈希必须能验回原口令（否则账号直接不可登录），
// 错误口令必须验不过（否则等于没有认证）。bcrypt 自带盐与工作因子，不需要额外加盐逻辑，
// 但它有一条容易踩的硬限制：**明文超过 72 字节直接返回错误**（见下方独立用例）。
func TestHashPasswordAndCheckPassword(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wrong    string // 用于反向验证的错误口令
	}{
		{"常规 ASCII 口令", "password", "Password"},
		{"纯数字口令", "123456", "1234567"},
		{"空口令（bcrypt 允许，业务侧另有非空校验）", "", "x"},
		{"含中文的口令", "口令中文测试", "口令中文测"},
		{"含特殊字符的口令", `p@ss w0rd!#$%^&*()_+-=[]{}|;':",./<>?`, `p@ss w0rd!#$%^&*()_+-=[]{}|;':",./<>`},
		{"含 emoji 的口令", "pa🚀ss", "pass"},
		{"含前后空格（不得被 Trim）", "  pass  ", "pass"},
		{"72 字节上界（bcrypt 允许的最长明文）", strings.Repeat("a", 72), strings.Repeat("a", 71)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := HashPassword(tc.password)
			if err != nil {
				t.Fatalf("HashPassword(%q) 失败: %v", tc.password, err)
			}
			if hash == tc.password {
				t.Fatal("哈希结果与明文相同，等于没做哈希")
			}
			if !strings.HasPrefix(hash, "$2a$") {
				t.Errorf("哈希不是 bcrypt 2a 格式：实际前缀 %.4q", hash)
			}
			if !CheckPassword(tc.password, hash) {
				t.Errorf("正确口令 %q 未通过校验，该账号将无法登录", tc.password)
			}
			if CheckPassword(tc.wrong, hash) {
				t.Errorf("错误口令 %q 通过了校验（正确口令为 %q）", tc.wrong, tc.password)
			}
		})
	}
}

// TestHashPasswordIsSalted 同一口令两次哈希结果必须不同（bcrypt 每次生成随机盐）。
// 风险点：若结果相同说明退化成了无盐哈希，一旦库被拖走可用彩虹表批量还原，
// 且能从库里直接看出「哪些用户用了同一个口令」。
func TestHashPasswordIsSalted(t *testing.T) {
	const pwd = "same-password"
	first, err := HashPassword(pwd)
	if err != nil {
		t.Fatalf("第一次哈希失败: %v", err)
	}
	second, err := HashPassword(pwd)
	if err != nil {
		t.Fatalf("第二次哈希失败: %v", err)
	}
	if first == second {
		t.Errorf("同一口令两次哈希结果相同，说明未加盐：%s", first)
	}
	// 两个哈希都必须能验回同一个口令
	if !CheckPassword(pwd, first) || !CheckPassword(pwd, second) {
		t.Error("加盐后两个哈希应都能验回原口令")
	}
}

// TestCheckPasswordRejectsBrokenHash 覆盖哈希被篡改/损坏时的行为。
//
// 风险点：CheckPassword 只返回 bool，任何解析错误都会被折叠成 false。
// 必须确认「解析失败」走的是拒绝而不是放行 —— 尤其是空哈希：
// 如果数据库里因为写入失败留下了空 password_hash，绝不能变成「任意口令都能登录」。
func TestCheckPasswordRejectsBrokenHash(t *testing.T) {
	valid, err := HashPassword("real-password")
	if err != nil {
		t.Fatalf("准备哈希失败: %v", err)
	}

	cases := []struct {
		name string
		hash string
	}{
		{"空哈希（写库失败留下的空值）", ""},
		{"纯空格", "   "},
		{"非 bcrypt 的明文", "real-password"},
		{"截断的哈希", valid[:len(valid)-5]},
		{"末位被改", valid[:len(valid)-1] + flipChar(valid[len(valid)-1])},
		{"盐段被改", valid[:10] + flipChar(valid[10]) + valid[11:]},
		{"工作因子被改成 04", "$2a$04$" + valid[7:]},
		{"随机垃圾串", "$2a$10$this-is-not-a-valid-bcrypt-hash-at-all-really"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if CheckPassword("real-password", tc.hash) {
				t.Errorf("哈希已损坏/被篡改却通过了校验：hash=%q", tc.hash)
			}
		})
	}
}

// TestCheckPasswordAcceptsCompatibleBcryptPrefixes 记录 bcrypt 次版本号不参与校验这一事实。
//
// x/crypto/bcrypt 对主版本 2 不校验次版本字母（$2a$/$2b$/$2y$ 是历史上不同实现留下的
// 兼容标记，摘要算法一致），因此把前缀改成 $2y$ 后同一口令仍能验过。
// 这不是缺陷：从 PHP、Node 等系统迁移过来的存量哈希可以直接用，无需重置口令。
// 但也意味着「前缀被改」不能当作篡改检测手段 —— 上面的篡改用例刻意不含这一项。
func TestCheckPasswordAcceptsCompatibleBcryptPrefixes(t *testing.T) {
	const pwd = "migrate-me"
	hash, err := HashPassword(pwd)
	if err != nil {
		t.Fatalf("准备哈希失败: %v", err)
	}

	for _, prefix := range []string{"$2a$", "$2b$", "$2y$"} {
		t.Run("前缀 "+prefix+" 仍可校验", func(t *testing.T) {
			variant := prefix + hash[4:]
			if !CheckPassword(pwd, variant) {
				t.Errorf("前缀 %s 的等价哈希校验失败：%s", prefix, variant)
			}
			if CheckPassword("wrong-password", variant) {
				t.Errorf("前缀 %s 的哈希放行了错误口令", prefix)
			}
		})
	}
}

// flipChar 把一个字符换成另一个合法 base64 字符（用于构造篡改后的哈希）。
func flipChar(c byte) string {
	if c == 'A' {
		return "B"
	}
	return "A"
}

// TestHashPasswordRejectsOver72Bytes 记录 bcrypt 的 72 字节硬限制。
//
// 这是 golang.org/x/crypto 的既有行为（v0.31 起明确返回 ErrPasswordTooLong），
// 影响真实用户：25 个汉字的口令就是 75 字节，创建用户/改密时会直接失败。
// 现状是 handler/user.go 把该错误折叠成 500「密码处理失败」（CreateUser）与
// 500「密码加密失败」（ChangeMyPassword）—— 空哈希不会被写库（这点是对的），
// 但用户看不出真正原因，只会以为是服务器故障。
// 已在回报中列为待改进项：应在入口按**字节长度**校验并给出中文提示。
func TestHashPasswordRejectsOver72Bytes(t *testing.T) {
	cases := []struct {
		name    string
		pwd     string
		wantErr bool
	}{
		{"72 字节 ASCII：允许", strings.Repeat("a", 72), false},
		{"73 字节 ASCII：报错", strings.Repeat("a", 73), true},
		{"24 个汉字（72 字节）：允许", strings.Repeat("中", 24), false},
		{"25 个汉字（75 字节）：报错", strings.Repeat("中", 25), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := HashPassword(tc.pwd)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错（%d 字节超过 bcrypt 上限），实际成功返回哈希 %q", len(tc.pwd), hash)
				}
				if hash != "" {
					t.Errorf("报错时不应返回哈希，实际返回 %q（若被误写入库将造成账号不可登录）", hash)
				}
				return
			}
			if err != nil {
				t.Fatalf("%d 字节口令应允许，实际报错: %v", len(tc.pwd), err)
			}
			if !CheckPassword(tc.pwd, hash) {
				t.Error("哈希无法验回原口令")
			}
		})
	}

	// 补一条容易被忽略的非对称行为：GenerateFromPassword 会拒绝超长明文，
	// 但 CompareHashAndPassword **不做长度检查**，bcrypt 算法本身只取前 72 字节。
	// 因此「前 72 字节相同」的两个不同口令会被判为同一个口令。
	// 当前不构成漏洞（本项目的哈希只能由 HashPassword 产生，超长明文根本进不了库），
	// 但若将来从别的系统迁入哈希、或有人把 cost/长度校验挪走，这条性质就会浮出水面。
	t.Run("bcrypt 只取前 72 字节：前缀相同的更长口令会被判为通过", func(t *testing.T) {
		base := strings.Repeat("a", 72)
		hash, err := HashPassword(base)
		if err != nil {
			t.Fatalf("准备 72 字节哈希失败: %v", err)
		}
		if !CheckPassword(base+"b", hash) {
			t.Error("现状变更：73 字节口令原本能通过 72 字节前缀的哈希校验（若已加长度校验，本用例应改为期望拒绝）")
		}
		if CheckPassword(strings.Repeat("a", 71), hash) {
			t.Error("前 72 字节不同（只有 71 个 a）的口令不应通过")
		}
	})
}

// ============================ JWT 签发与解析 ============================

// TestGenerateAndParseTokenRoundTrip 覆盖 token 签发→解析的往返一致性。
// 风险点：claims 还原错误会导致「登录成 A 用户、鉴权按 B 用户」，权限直接错位。
func TestGenerateAndParseTokenRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		user model.User
	}{
		{"管理员", model.User{ID: 1, Username: "admin", Role: "admin"}},
		{"只读用户", model.User{ID: 2, Username: "viewer", Role: "viewer"}},
		{"中文用户名", model.User{ID: 3, Username: "张三", Role: "viewer"}},
		{"大 ID", model.User{ID: 4294967295, Username: "u", Role: "admin"}},
		{"空角色（未设置角色的历史数据）", model.User{ID: 5, Username: "legacy", Role: ""}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token, err := GenerateToken(&tc.user)
			if err != nil {
				t.Fatalf("签发 token 失败: %v", err)
			}
			if strings.Count(token, ".") != 2 {
				t.Fatalf("token 不是三段式 JWT：%q", token)
			}

			claims, err := ParseToken(token)
			if err != nil {
				t.Fatalf("解析自己签发的 token 失败: %v", err)
			}
			if claims.UserID != tc.user.ID {
				t.Errorf("user_id 还原错误：期望 %d，实际 %d", tc.user.ID, claims.UserID)
			}
			if claims.Username != tc.user.Username {
				t.Errorf("username 还原错误：期望 %q，实际 %q", tc.user.Username, claims.Username)
			}
			if claims.Role != tc.user.Role {
				t.Errorf("role 还原错误：期望 %q，实际 %q", tc.user.Role, claims.Role)
			}
			if claims.ExpiresAt == nil {
				t.Fatal("token 缺少 exp，将永不过期")
			}
			// 有效期应约等于配置值（留 2 分钟余量吸收执行耗时）
			gotMinutes := time.Until(claims.ExpiresAt.Time).Minutes()
			if gotMinutes < testExpireMinutes-2 || gotMinutes > testExpireMinutes+1 {
				t.Errorf("有效期与配置不符：期望约 %d 分钟，实际 %.1f 分钟", testExpireMinutes, gotMinutes)
			}
			if claims.IssuedAt == nil {
				t.Error("token 缺少 iat")
			}
		})
	}
}

// b64url JWT 各段的编码方式（RawURLEncoding：URL 安全字母表且不带 = 填充）。
func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// forgeToken 手工拼装一个 JWT。
//
// 刻意不用 jwt 库的 SignedString：攻击者手上没有本项目的库配置，直接用库的便捷方法
// 可能被库自身的保护挡下（例如 alg=none 需要显式 UnsafeAllowNoneSignatureType），
// 那样测到的就不是 ParseToken 的防护而是签发侧的防护。这里用 encoding/base64 +
// crypto/hmac 从零构造，完全模拟外部伪造者的能力。
// mac 为 nil 表示不带签名（alg=none 的空签名段）。
func forgeToken(header, payload string, mac func(signingInput []byte) []byte) string {
	signingInput := b64url([]byte(header)) + "." + b64url([]byte(payload))
	if mac == nil {
		return signingInput + "."
	}
	return signingInput + "." + b64url(mac([]byte(signingInput)))
}

// testClaimsJSON 构造一份 admin 身份的 claims JSON（exp 为一小时后）。
func testClaimsJSON() string {
	return fmt.Sprintf(`{"user_id":1,"username":"admin","role":"admin","exp":%d,"iat":%d}`,
		time.Now().Add(time.Hour).Unix(), time.Now().Unix())
}

// TestParseTokenRejectsAlgorithmConfusion 覆盖算法混淆攻击（P1 安全批次的重点成果）。
//
// 风险点：jwt 库默认「按 token 自己声明的 alg 选择验签路径」。若不锁定算法：
//   - alg=none 的 token 在部分实现里被当作「无需验签」直接放行，任何人都能伪造 admin；
//   - alg=HS512 用同一个对称密钥也能签出合法签名，等于绕开了「本项目只签 HS256」的假设
//     （对 RS256→HS256 型的混淆更致命：公钥当 HMAC 密钥即可伪造）。
//
// 修复手段是 ParseToken 里的 jwt.WithValidMethods([]string{"HS256"})。
// 本用例同时给出**反证**：去掉该选项后同一个 HS512 token 会被判定为合法，
// 证明这行代码是真正在承压，而不是可有可无的装饰。
func TestParseTokenRejectsAlgorithmConfusion(t *testing.T) {
	payload := testClaimsJSON()

	// 控制组：手工按 HS256 + 正确密钥签名，必须被接受。
	// 没有这一条，下面的「拒绝」断言可能只是因为伪造流程本身写错了。
	t.Run("控制组：手工构造的合法 HS256 token 必须被接受", func(t *testing.T) {
		token := forgeToken(`{"alg":"HS256","typ":"JWT"}`, payload, func(in []byte) []byte {
			m := hmac.New(sha256.New, []byte(testSecret))
			m.Write(in)
			return m.Sum(nil)
		})
		claims, err := ParseToken(token)
		if err != nil {
			t.Fatalf("手工构造的合法 HS256 token 被拒，说明伪造流程本身有误: %v", err)
		}
		if claims.Role != "admin" || claims.UserID != 1 {
			t.Errorf("claims 还原错误：实际 user_id=%d role=%q", claims.UserID, claims.Role)
		}
	})

	t.Run("alg=none 空签名伪造必须被拒", func(t *testing.T) {
		token := forgeToken(`{"alg":"none","typ":"JWT"}`, payload, nil)
		claims, err := ParseToken(token)
		if err == nil {
			t.Fatalf("alg=none 的伪造 token 被接受，任何人都能伪造管理员身份：claims=%+v", claims)
		}
		if claims != nil {
			t.Errorf("解析失败时应返回 nil claims，实际返回 %+v", claims)
		}
		if !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
			t.Errorf("错误类型不符：期望 %v 系列，实际 %v", jwt.ErrTokenSignatureInvalid, err)
		}
		if !strings.Contains(err.Error(), "none") {
			t.Errorf("错误信息未点明算法问题：%v", err)
		}
	})

	t.Run("alg=HS512（同密钥签名）必须被拒", func(t *testing.T) {
		token := forgeToken(`{"alg":"HS512","typ":"JWT"}`, payload, func(in []byte) []byte {
			m := hmac.New(sha512.New, []byte(testSecret))
			m.Write(in)
			return m.Sum(nil)
		})
		if _, err := ParseToken(token); err == nil {
			t.Fatal("alg=HS512 的 token 被接受，WithValidMethods 未生效")
		} else if !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
			t.Errorf("错误类型不符：期望签名/算法非法，实际 %v", err)
		}

		// 反证：同一个 token，去掉 WithValidMethods 就会被判为合法。
		// 说明防护完全来自那一个选项，删掉即回归到可伪造状态。
		unsafeParsed, err := jwt.ParseWithClaims(token, &Claims{}, func(*jwt.Token) (interface{}, error) {
			return []byte(testSecret), nil
		})
		if err != nil || !unsafeParsed.Valid {
			t.Fatalf("反证失败：未锁定算法时本应接受该 HS512 token（err=%v），"+
				"若库行为已变更请重新评估 ParseToken 的防护", err)
		}
	})

	t.Run("alg=HS384（同密钥签名）必须被拒", func(t *testing.T) {
		token := forgeToken(`{"alg":"HS384","typ":"JWT"}`, payload, func(in []byte) []byte {
			m := hmac.New(sha512.New384, []byte(testSecret))
			m.Write(in)
			return m.Sum(nil)
		})
		if _, err := ParseToken(token); err == nil {
			t.Fatal("alg=HS384 的 token 被接受，WithValidMethods 未覆盖全部 HMAC 变体")
		}
	})
}

// TestParseTokenRejectsExpiredToken 过期 token 必须被拒。
// 风险点：过期不生效等于 token 永久有效，一次泄漏永久可用。
// 这里把配置里的有效期改成负数来签发「出生即过期」的 token（用完立刻恢复）。
func TestParseTokenRejectsExpiredToken(t *testing.T) {
	original := config.GlobalConfig.JWTExpireMinutes
	defer func() { config.GlobalConfig.JWTExpireMinutes = original }()

	config.GlobalConfig.JWTExpireMinutes = -10 // 10 分钟前就过期
	token, err := GenerateToken(&model.User{ID: 1, Username: "admin", Role: "admin"})
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}

	claims, err := ParseToken(token)
	if err == nil {
		t.Fatalf("过期 token 被接受：claims=%+v", claims)
	}
	if !errors.Is(err, jwt.ErrTokenExpired) {
		t.Errorf("错误类型不符：期望 %v，实际 %v", jwt.ErrTokenExpired, err)
	}
	if claims != nil {
		t.Errorf("解析失败时应返回 nil claims，实际 %+v", claims)
	}
}

// TestParseTokenRejectsWrongSecret 换密钥签的 token 必须被拒。
// 风险点：这是「他人签发的 token 不被本平台接受」的基本保证；
// 同时也验证了改 JWT_SECRET_KEY 能立刻吊销全部存量 token。
func TestParseTokenRejectsWrongSecret(t *testing.T) {
	// 用另一个密钥签发（临时改配置，签完立刻恢复）
	original := config.GlobalConfig.JWTSecretKey
	config.GlobalConfig.JWTSecretKey = "attacker-guessed-secret"
	token, err := GenerateToken(&model.User{ID: 1, Username: "admin", Role: "admin"})
	config.GlobalConfig.JWTSecretKey = original
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}

	if claims, err := ParseToken(token); err == nil {
		t.Fatalf("其他密钥签发的 token 被接受：claims=%+v", claims)
	} else if !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
		t.Errorf("错误类型不符：期望 %v，实际 %v", jwt.ErrTokenSignatureInvalid, err)
	}

	// 内置默认密钥签的 token 也不该被接受（release 模式禁用默认密钥的配套保证）
	config.GlobalConfig.JWTSecretKey = "vmops-jwt-secret-key-change-in-production"
	defaultKeyToken, err := GenerateToken(&model.User{ID: 1, Username: "admin", Role: "admin"})
	config.GlobalConfig.JWTSecretKey = original
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}
	if _, err := ParseToken(defaultKeyToken); err == nil {
		t.Error("用内置默认密钥签发的 token 被接受了，说明当前密钥就是默认值")
	}
}

// TestParseTokenRejectsMalformedToken 覆盖畸形 token。
// 风险点：解析器必须对任意垃圾输入返回错误而不是 panic —— 该输入直接来自
// Authorization 头或 ?token= 查询参数，完全由外部控制。
func TestParseTokenRejectsMalformedToken(t *testing.T) {
	validToken, err := GenerateToken(&model.User{ID: 1, Username: "admin", Role: "admin"})
	if err != nil {
		t.Fatalf("准备 token 失败: %v", err)
	}
	parts := strings.Split(validToken, ".")

	cases := []struct {
		name  string
		token string
	}{
		{"空串", ""},
		{"只有一段", "abc"},
		{"只有两段", parts[0] + "." + parts[1]},
		{"四段", validToken + ".extra"},
		{"全是点", "..."},
		{"非 base64 乱码", "!!!.???.###"},
		{"header 段非法 base64", "x." + parts[1] + "." + parts[2]},
		{"payload 段非法 base64", parts[0] + ".!!!." + parts[2]},
		{"签名段被截断", validToken[:len(validToken)-6]},
		{"签名段被替换", parts[0] + "." + parts[1] + "." + b64url([]byte("forged-signature"))},
		{"payload 被改（签名不再匹配）", parts[0] + "." +
			b64url([]byte(`{"user_id":1,"username":"admin","role":"admin"}`)) + "." + parts[2]},
		{"带 Bearer 前缀（调用方忘记切分）", "Bearer " + validToken},
		{"首尾有空格", " " + validToken + " "},
		{"超长垃圾串", strings.Repeat("A", 8192)},
		{"header 是合法 base64 但不是 JSON", b64url([]byte("not-json")) + "." + parts[1] + "." + parts[2]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := ParseToken(tc.token)
			if err == nil {
				t.Fatalf("畸形 token 被接受：token=%.40q claims=%+v", tc.token, claims)
			}
			if claims != nil {
				t.Errorf("解析失败时应返回 nil claims，实际 %+v", claims)
			}
		})
	}
}

// TestClaimsFromRequest 覆盖审计中间件用的身份提取（支持 Header 与查询参数两种来源）。
//
// 风险点：浏览器发起 WebSocket 时无法设置请求头，所以必须支持 ?token=；
// 但这条旁路不能放宽校验 —— 空 token、格式错误、非 Bearer 方案都必须失败。
func TestClaimsFromRequest(t *testing.T) {
	token, err := GenerateToken(&model.User{ID: 8, Username: "auditor", Role: "admin"})
	if err != nil {
		t.Fatalf("准备 token 失败: %v", err)
	}

	cases := []struct {
		name    string
		header  string
		query   string
		wantErr bool
	}{
		{"标准 Bearer 头", "Bearer " + token, "", false},
		{"查询参数（WebSocket 场景）", "", token, false},
		{"两者都没有", "", "", true},
		{"查询参数为空串", "", "", true},
		{"缺少 Bearer 前缀", token, "", true},
		{"方案名错误", "Token " + token, "", true},
		{"方案名小写", "bearer " + token, "", true},
		{"只有 Bearer 没有 token", "Bearer ", "", true},
		{"Bearer 后跟垃圾", "Bearer not-a-token", "", true},
		{"Header 优先于查询参数：Header 非法则失败", "Bearer bad", token, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := "/api/vms"
			if tc.query != "" {
				target += "?token=" + tc.query
			}
			req := httptest.NewRequest(http.MethodGet, target, nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}

			claims, err := claimsFromRequest(req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望失败，实际取到身份 %+v", claims)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望成功，实际失败: %v", err)
			}
			if claims.Username != "auditor" || claims.UserID != 8 {
				t.Errorf("身份还原错误：期望 user_id=8 username=auditor，实际 user_id=%d username=%q",
					claims.UserID, claims.Username)
			}
		})
	}
}

// ============================ RBAC 中间件 ============================

// newGuardedRouter 注册与 main.go 同形的路由并挂上指定权限中间件。
//
// 必须走**真实路由注册**：OperatorMiddleware 依赖 c.FullPath() 拿路由模板
// （如 /api/vms/:id/terminal），而 gin.CreateTestContext 造出的裸上下文 FullPath() 恒为空串，
// isGuestWriteChannel/isVNCTokenPath 就会永远返回 false，测出来的是假的「放行」。
// setRole=false 模拟上下文里根本没有 role（鉴权中间件未执行或未写入）。
func newGuardedRouter(guard gin.HandlerFunc, role interface{}, setRole bool) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if setRole {
			c.Set("role", role)
		}
		c.Next()
	})
	r.Use(guard)

	pass := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "success", "data": "reached"})
	}
	api := r.Group("/api")
	api.GET("/vms", pass)
	api.POST("/vms", pass)
	api.GET("/vms/:id", pass)
	api.PUT("/vms/:id", pass)
	api.DELETE("/vms/:id", pass)
	api.GET("/vms/:id/stats", pass)
	api.POST("/vms/:id/start", pass)
	api.PUT("/vms/:id/xml", pass)
	api.GET("/vms/:id/terminal", pass)
	api.GET("/vms/:id/serial", pass)
	api.POST("/vms/:id/vnc-token", pass)
	api.GET("/users", pass)
	api.POST("/users", pass)
	api.DELETE("/users/:id", pass)
	api.GET("/audit/logs", pass)
	return r
}

// doRequest 发一次请求并返回响应记录器。
func doRequest(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// assertForbiddenShape 校验 403 响应是全站统一格式 {code, message, data}。
//
// 风险点：middleware 不能复用 handler.Fail（handler 已依赖 middleware，反向引用成导入环），
// 所以包内单独实现了 abortJSON。格式一旦漂移，前端拦截器读不到 message，
// 用户只会看到「未知错误」。注意 abortJSON **写 data:null**，与 handler.Fail 不同。
func assertForbiddenShape(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("HTTP 状态码错误：期望 %d，实际 %d（响应体 %s）",
			http.StatusForbidden, rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应体不是合法 JSON：err=%v 内容=%q", err, rec.Body.String())
	}
	if got, want := body["code"], float64(http.StatusForbidden); got != want {
		t.Errorf("响应 code 错误：期望 %v，实际 %v", want, got)
	}
	msg, ok := body["message"].(string)
	if !ok || msg == "" {
		t.Errorf("message 缺失或类型错误：实际 %#v", body["message"])
	}
	if v, exists := body["data"]; !exists || v != nil {
		t.Errorf("data 键必须存在且为 null：exists=%v value=%#v", exists, v)
	}
	if strings.Contains(rec.Body.String(), "reached") {
		t.Error("被拒的请求仍执行到了业务处理器，说明未 Abort")
	}
}

// TestAdminMiddleware 覆盖管理员权限门控（/users、/audit、/settings 三组在用）。
//
// 风险点：role 是 c.Get 取出的 interface{}，类型断言必须带 ok。
// P0 批次前这里写的是 role.(string) 裸断言，一旦上下文里的 role 不是 string
// （比如别的中间件写了别的类型）就会 panic 掉整条请求链。
func TestAdminMiddleware(t *testing.T) {
	cases := []struct {
		name    string
		role    interface{}
		setRole bool
		wantOK  bool
	}{
		{"admin 放行", "admin", true, true},
		{"viewer 拒绝", "viewer", true, false},
		{"operator（不存在的角色名）拒绝", "operator", true, false},
		{"空角色拒绝", "", true, false},
		{"大写 Admin 拒绝（角色名大小写敏感）", "Admin", true, false},
		{"带空格的 admin 拒绝", " admin", true, false},
		{"上下文未设置 role 拒绝", nil, false, false},
		{"role 为 nil 值拒绝", nil, true, false},
		{"role 类型为 int：按无权限处理且不 panic", 1, true, false},
		{"role 类型为 bool：按无权限处理且不 panic", true, true, false},
		{"role 类型为切片：按无权限处理且不 panic", []string{"admin"}, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newGuardedRouter(AdminMiddleware(), tc.role, tc.setRole)
			rec := doRequest(r, http.MethodGet, "/api/users")

			if tc.wantOK {
				if rec.Code != http.StatusOK {
					t.Fatalf("期望放行，实际状态码 %d，响应体 %s", rec.Code, rec.Body.String())
				}
				if !strings.Contains(rec.Body.String(), "reached") {
					t.Errorf("放行时应执行到业务处理器，实际响应体 %s", rec.Body.String())
				}
				return
			}
			assertForbiddenShape(t, rec)
			if got := gjsonMessage(t, rec); got != "需要管理员权限" {
				t.Errorf("拒绝文案错误：期望 %q，实际 %q", "需要管理员权限", got)
			}
		})
	}
}

// gjsonMessage 取响应体里的 message 字段。
func gjsonMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应体不是合法 JSON：%v", err)
	}
	msg, _ := body["message"].(string)
	return msg
}

// TestOperatorMiddleware 覆盖 RBAC 第一阶段语义：admin 全放行、viewer 真只读。
//
// 风险点（P1 批次收紧的核心）：
//   - viewer 能到达全部 GET 接口，所以路径参数注入面对最低权限角色同样开放（见 handler/param.go）；
//   - GET /terminal 与 GET /serial 虽是 GET，但建立的是对 guest 的**双向写入**通道
//     （串口在多数云镜像上直接是 root TTY），必须对 viewer 关闭，否则「只读」名不副实；
//   - POST /vnc-token 是唯一放行给 viewer 的写方法请求（图形控制台以 view_only 打开）。
func TestOperatorMiddleware(t *testing.T) {
	cases := []struct {
		name       string
		role       interface{}
		setRole    bool
		method     string
		path       string
		wantStatus int
	}{
		// —— admin：不区分方法与路径，全放行 ——
		{"admin GET 列表", "admin", true, http.MethodGet, "/api/vms", http.StatusOK},
		{"admin POST 创建", "admin", true, http.MethodPost, "/api/vms", http.StatusOK},
		{"admin DELETE 删除", "admin", true, http.MethodDelete, "/api/vms/1", http.StatusOK},
		{"admin GET SSH 终端", "admin", true, http.MethodGet, "/api/vms/1/terminal", http.StatusOK},
		{"admin GET 串口", "admin", true, http.MethodGet, "/api/vms/1/serial", http.StatusOK},
		{"admin PUT 改 XML", "admin", true, http.MethodPut, "/api/vms/1/xml", http.StatusOK},

		// —— viewer：GET 放行 ——
		{"viewer GET 列表放行", "viewer", true, http.MethodGet, "/api/vms", http.StatusOK},
		{"viewer GET 详情放行", "viewer", true, http.MethodGet, "/api/vms/1", http.StatusOK},
		{"viewer GET 性能数据放行", "viewer", true, http.MethodGet, "/api/vms/1/stats", http.StatusOK},

		// —— viewer：交互式写入通道虽是 GET 也要拒 ——
		{"viewer GET SSH 终端拒绝", "viewer", true, http.MethodGet, "/api/vms/1/terminal", http.StatusForbidden},
		{"viewer GET 串口拒绝", "viewer", true, http.MethodGet, "/api/vms/1/serial", http.StatusForbidden},

		// —— viewer：变更操作一律拒，唯一例外是 vnc-token ——
		{"viewer POST vnc-token 放行（只读观看）", "viewer", true, http.MethodPost, "/api/vms/1/vnc-token", http.StatusOK},
		{"viewer POST 创建拒绝", "viewer", true, http.MethodPost, "/api/vms", http.StatusForbidden},
		{"viewer POST 开机拒绝", "viewer", true, http.MethodPost, "/api/vms/1/start", http.StatusForbidden},
		{"viewer PUT 改 XML 拒绝", "viewer", true, http.MethodPut, "/api/vms/1/xml", http.StatusForbidden},
		{"viewer DELETE 删除拒绝", "viewer", true, http.MethodDelete, "/api/vms/1", http.StatusForbidden},

		// —— 未知角色与异常类型：一律按 viewer（最小权限）处理，且不能 panic ——
		{"未知角色 GET 放行（等同 viewer）", "guest", true, http.MethodGet, "/api/vms", http.StatusOK},
		{"未知角色 POST 拒绝", "guest", true, http.MethodPost, "/api/vms", http.StatusForbidden},
		{"未设置 role GET 放行（等同 viewer）", nil, false, http.MethodGet, "/api/vms", http.StatusOK},
		{"未设置 role POST 拒绝", nil, false, http.MethodPost, "/api/vms", http.StatusForbidden},
		{"role 类型为 int：GET 放行且不 panic", 1, true, http.MethodGet, "/api/vms", http.StatusOK},
		{"role 类型为 int：POST 拒绝且不 panic", 1, true, http.MethodPost, "/api/vms", http.StatusForbidden},
		{"role 类型为 int：SSH 终端拒绝", 1, true, http.MethodGet, "/api/vms/1/terminal", http.StatusForbidden},
		{"role 为 nil 值：串口拒绝", nil, true, http.MethodGet, "/api/vms/1/serial", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newGuardedRouter(OperatorMiddleware(), tc.role, tc.setRole)
			rec := doRequest(r, tc.method, tc.path)

			if tc.wantStatus == http.StatusOK {
				if rec.Code != http.StatusOK {
					t.Fatalf("期望放行 %s %s，实际状态码 %d，响应体 %s",
						tc.method, tc.path, rec.Code, rec.Body.String())
				}
				if !strings.Contains(rec.Body.String(), "reached") {
					t.Errorf("放行时应执行到业务处理器，实际响应体 %s", rec.Body.String())
				}
				return
			}
			assertForbiddenShape(t, rec)
		})
	}

	// 拒绝文案要区分场景，让用户知道「不是没登录，而是角色不够」以及「换图形控制台」
	t.Run("SSH 终端与串口的拒绝文案指向图形控制台", func(t *testing.T) {
		r := newGuardedRouter(OperatorMiddleware(), "viewer", true)
		for _, path := range []string{"/api/vms/1/terminal", "/api/vms/1/serial"} {
			rec := doRequest(r, http.MethodGet, path)
			msg := gjsonMessage(t, rec)
			if !strings.Contains(msg, "只读角色") || !strings.Contains(msg, "图形控制台") {
				t.Errorf("%s 的拒绝文案未说明原因与替代方案：%q", path, msg)
			}
		}
	})
}

// TestIsVNCTokenPath 覆盖 VNC token 路径判定。
//
// 风险点：判定用的是**后缀匹配**，与路由命名强耦合 —— 将来若新增任何以 /vnc-token 结尾
// 的写接口，会自动对 viewer 放行。本用例把当前语义钉住，改动路由命名时会立刻失败。
func TestIsVNCTokenPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"实际注册的路由模板", "/api/vms/:id/vnc-token", true},
		{"仅后缀本身", "/vnc-token", true},
		{"复数形式不匹配", "/api/vms/:id/vnc-tokens", false},
		{"后面还有段不匹配", "/api/vms/:id/vnc-token/refresh", false},
		{"下划线写法不匹配", "/api/vms/:id/vnc_token", false},
		{"普通详情路由", "/api/vms/:id", false},
		{"空串（路由未匹配时 FullPath 的取值）", "", false},
		{"大小写不同不匹配", "/api/vms/:id/VNC-TOKEN", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isVNCTokenPath(tc.path); got != tc.want {
				t.Errorf("isVNCTokenPath(%q) 判定错误：期望 %v，实际 %v", tc.path, tc.want, got)
			}
		})
	}
}

// TestIsGuestWriteChannel 覆盖「对 guest 的交互式写入通道」判定。
//
// 风险点：这两个接口注册为 GET（浏览器 WebSocket 只能发 GET），因此不能只靠 HTTP 方法
// 判断读写语义 —— 这正是 viewer 曾经能拿到 root TTY 的原因。同样是后缀匹配，
// 新增同类通道（例如 /console-ws）必须记得同步本函数，否则默认按只读放行。
func TestIsGuestWriteChannel(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"SSH 终端路由模板", "/api/vms/:id/terminal", true},
		{"串口路由模板", "/api/vms/:id/serial", true},
		{"仅后缀 terminal", "/terminal", true},
		{"仅后缀 serial", "/serial", true},
		{"复数 terminals 不匹配", "/api/vms/:id/terminals", false},
		{"serial 后还有段不匹配", "/api/vms/:id/serial/logs", false},
		{"VNC token 不属于写入通道", "/api/vms/:id/vnc-token", false},
		{"普通详情路由", "/api/vms/:id", false},
		{"空串（路由未匹配）", "", false},
		{"大小写不同不匹配", "/api/vms/:id/Terminal", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isGuestWriteChannel(tc.path); got != tc.want {
				t.Errorf("isGuestWriteChannel(%q) 判定错误：期望 %v，实际 %v", tc.path, tc.want, got)
			}
		})
	}
}
