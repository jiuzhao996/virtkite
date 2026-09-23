package handler

import (
	"testing"

	"github.com/jiuzhao/vmops/service/vnc"
)

// 临时验证：同一份 Deps 装配出的两个 VNCHandler（签发侧/解析侧）必须共用同一 TokenStore。
func TestVNCTokenStoreSharedAcrossHandlers(t *testing.T) {
	deps := Deps{VNCTokens: vnc.NewTokenStore()}
	issue := NewVNCHandler(nil, nil, deps.VNCTokens)   // 认证组：签发
	resolve := NewVNCHandler(nil, nil, deps.VNCTokens) // 公开组：解析

	if issue.Token != resolve.Token {
		t.Fatalf("签发与解析未共用同一 TokenStore：%p vs %p", issue.Token, resolve.Token)
	}
	tok, err := issue.Token.Generate(7, "127.0.0.1", 5900)
	if err != nil {
		t.Fatalf("签发失败：%v", err)
	}
	if host, port, ok := resolve.Token.Lookup(tok); !ok || host != "127.0.0.1" || port != 5900 {
		t.Fatalf("解析侧查不到签发侧 token：ok=%v host=%q port=%d", ok, host, port)
	}
}

// 临时验证：构造期未注入共享实例时立刻暴露（不允许带病装配）。
func TestVNCHandlerNilTokenStorePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil TokenStore 未触发装配期告警")
		}
	}()
	NewVNCHandler(nil, nil, nil)
}
