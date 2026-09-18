package secretbox

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// 固定测试向量（生产主密钥来自运行时注入，测试用固定值保证可复现）。
const (
	testMaster1 = "unit-test-master-secret-1"
	testMaster2 = "unit-test-master-secret-2"
	// 32 字节盐的 hex（64 字符），对应 SealWithMaster 的盐规格
	testSaltHex  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	otherSaltHex = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

// TestDeriveKey 派生确定性：同输入同密钥（32 字节），任一输入不同则密钥不同。
func TestDeriveKey(t *testing.T) {
	t.Parallel()
	salt := mustUnhex(t, testSaltHex)
	k1 := DeriveKey(testMaster1, salt)
	k2 := DeriveKey(testMaster1, salt)
	if len(k1) != 32 {
		t.Fatalf("AES-256 密钥应为 32 字节，实际 %d", len(k1))
	}
	if string(k1) != string(k2) {
		t.Fatal("相同主密钥与盐必须派生出相同密钥")
	}
	if string(k1) == string(DeriveKey(testMaster2, salt)) {
		t.Fatal("不同主密钥不得派生出相同密钥")
	}
	if string(k1) == string(DeriveKey(testMaster1, mustUnhex(t, otherSaltHex))) {
		t.Fatal("不同盐不得派生出相同密钥")
	}
}

// TestEncryptDecryptRoundTrip 加密解密往返（含空串、中文、长明文）。
func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		plain string
	}{
		{"常规口令", "p@ssw0rd-123"},
		{"含中文与空格", "密码 pass word 123"},
		{"单字符", "a"},
		{"空串（GCM 允许，产出 nonce+tag）", ""},
		{"长串 4KB（无长度假设）", strings.Repeat("x", 4096)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ct, salt, err := SealWithMaster(testMaster1, tc.plain)
			if err != nil {
				t.Fatalf("SealWithMaster: %v", err)
			}
			if salt == "" || len(salt) != 64 {
				t.Fatalf("盐应为 64 字符 hex，实际 %q", salt)
			}
			got, err := OpenWithMaster(testMaster1, salt, ct)
			if err != nil {
				t.Fatalf("OpenWithMaster: %v", err)
			}
			if string(got) != tc.plain {
				t.Fatalf("往返不一致: got %q want %q", got, tc.plain)
			}
		})
	}
}

// TestEncryptNoncePrefixAndRandomness 底层格式契约：base64 解开后 = nonce(12) + 密文体 + tag(16)；
// 同一明文两次加密密文必不同（随机 nonce 生效），但两次都能解回同一明文。
func TestEncryptNoncePrefixAndRandomness(t *testing.T) {
	t.Parallel()
	key := DeriveKey(testMaster1, mustUnhex(t, testSaltHex))
	ct1, err := Encrypt(key, []byte("same-plaintext"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ct2, err := Encrypt(key, []byte("same-plaintext"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ct1 == ct2 {
		t.Fatal("同一明文两次加密的密文必须不同（随机 nonce）")
	}
	raw, err := base64.StdEncoding.DecodeString(ct1)
	if err != nil {
		t.Fatalf("密文应为合法 base64: %v", err)
	}
	// GCM-128 标准参数：nonce 12 字节 + 认证标签 16 字节
	if len(raw) < 12+16 {
		t.Fatalf("密文长度应 ≥ nonce(12)+tag(16)，实际 %d", len(raw))
	}
	for _, ct := range []string{ct1, ct2} {
		got, err := Decrypt(key, ct)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if string(got) != "same-plaintext" {
			t.Fatalf("解密结果不符: %q", got)
		}
	}
}

// TestDecryptTamperedCiphertext 篡改密文体/认证标签后解密必须失败（GCM 完整性校验）。
func TestDecryptTamperedCiphertext(t *testing.T) {
	t.Parallel()
	ct, salt, err := SealWithMaster(testMaster1, "correct horse battery staple")
	if err != nil {
		t.Fatalf("SealWithMaster: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(ct)
	if err != nil {
		t.Fatalf("SealWithMaster 产出应为合法 base64: %v", err)
	}
	cases := []struct {
		name   string
		offset int
	}{
		{"篡改密文体（nonce 之后）", 13},
		{"篡改认证标签（末字节）", len(raw) - 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bad := make([]byte, len(raw))
			copy(bad, raw)
			bad[tc.offset] ^= 0xFF
			if _, err := OpenWithMaster(testMaster1, salt, base64.StdEncoding.EncodeToString(bad)); err == nil {
				t.Fatal("篡改后的密文必须解密失败")
			}
		})
	}
}

// TestDecryptWrongKey 错误主密钥 / 盐错配必须解密失败（覆盖「主密钥已轮换」场景）。
func TestDecryptWrongKey(t *testing.T) {
	t.Parallel()
	ct, salt, err := SealWithMaster(testMaster1, "secret")
	if err != nil {
		t.Fatalf("SealWithMaster: %v", err)
	}
	if _, err := OpenWithMaster(testMaster2, salt, ct); err == nil {
		t.Fatal("错误主密钥必须解密失败")
	}
	if _, err := OpenWithMaster(testMaster1, otherSaltHex, ct); err == nil {
		t.Fatal("盐错配（拿别条记录的盐解密）必须解密失败")
	}
}

// TestDecryptInvalidInput 非法入参（盐非 hex / 密文非 base64 / 密文过短）必须报错而非 panic。
func TestDecryptInvalidInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		saltHex string
		ct      string
	}{
		{"盐不是合法 hex", "zz-not-hex", base64.StdEncoding.EncodeToString(make([]byte, 40))},
		{"密文不是合法 base64", testSaltHex, "not-base64!!!"},
		{"密文过短（放不下 nonce+tag）", testSaltHex, base64.StdEncoding.EncodeToString([]byte("short"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := OpenWithMaster(testMaster1, tc.saltHex, tc.ct); err == nil {
				t.Fatal("非法入参必须返回错误")
			}
		})
	}
}

// mustUnhex 测试辅助：hex 解码，失败即终止用例。
func mustUnhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("测试向量盐非法: %v", err)
	}
	return b
}
