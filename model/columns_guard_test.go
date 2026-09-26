package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

// 本文件是一道护栏测试，锁死一类「静默失败」缺陷：
//
// GORM 的字段名与列名并不总是一致——VCPU 字段对应的列名是 v_cpu（蛇形命名）。
// 因此 db.Model(&vm).Update("vcpu", n) 每次都会报 1054 未知列，而调用处若只
// LogError 吞掉，表现就是「libvirt 改了、DB 没改」的持久配置漂移，没人看得见。
// 这类 bug 在 handler/vm.go（SetVcpu/SetMemory）与 handler/vm_xml.go 各咬过一次。
//
// 护栏做法：解析所有模型的 schema 得到合法列名/字段名集合，再用 AST 扫
// handler/ 与 service/ 里手写的 Update/Updates/Pluck 列名，凡不在集合内即失败。

// guardModels 与 database/database.go 的 AutoMigrate 清单保持一致（新增模型请同步此处）。
var guardModels = []interface{}{
	&User{}, &Host{}, &VM{}, &Image{}, &AuditLog{}, &Task{}, &ConsoleSession{},
	&Setting{}, &Alert{}, &PoolMeta{}, &VMGrant{}, &CronRun{}, &VMCredential{},
	&ScheduledTask{}, &CloudInitTemplate{}, &HostKey{},
	&UserGroup{}, &UserGroupMember{}, &VMGroupGrant{}, &GrantRequest{},
}

// guardNameSet 收集全部模型的合法列名（DBName）与字段名（Name）——GORM 的
// Update/Updates 两者都接受，因此都要算合法。
func guardNameSet(t *testing.T) map[string]bool {
	t.Helper()
	ns := schema.NamingStrategy{}
	cache := &sync.Map{}
	valid := make(map[string]bool)
	for _, m := range guardModels {
		s, err := schema.Parse(m, cache, ns)
		if err != nil {
			t.Fatalf("解析模型 %T 的 schema 失败: %v", m, err)
		}
		for _, f := range s.Fields {
			valid[f.DBName] = true
			valid[f.Name] = true
		}
	}
	return valid
}

// guardColumnUses 用 AST 扫描给定目录下手写进 Update/Updates/Pluck 的列名，
// 返回 列名 -> 首次出现位置（文件:行:列）。跳过 SQL 表达式与非常规标识符。
//
// 需要认三种写法（第一种最容易被漏掉，实测 vm_xml.go 就是它）：
//  1. Updates(m) —— m 是先前赋值的 map[string]interface{} 变量；
//  2. Updates(map[string]interface{}{"col": v}) —— 直接传字面量；
//  3. Update("col", v) / Pluck("col", &x) —— 直接传列名字符串。
func guardColumnUses(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	uses := make(map[string]string)
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("扫描目录 %s 失败: %v", dir, err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			astFile, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				continue // 解析不了就跳过，不因个别文件误报
			}
			collectGuardUses(astFile, fset, uses)
		}
	}
	return uses
}

// collectGuardUses 单文件两遍扫描：先记住哪些变量是 map[string]interface{}，
// 再看这些变量最终有没有被送进 Updates（含后续的 m["col"] = v 追加写法）。
func collectGuardUses(astFile *ast.File, fset *token.FileSet, uses map[string]string) {
	// 第一遍：登记所有 map[string]interface{} 变量（先不记列名——大量 map 是任务
	// payload / 请求体，键是业务字段名而非 DB 列名，一律记会淹没真实告警）
	mapVars := make(map[string]*guardMapVar)
	recordMapVar := func(name string, expr ast.Expr) {
		cl, ok := expr.(*ast.CompositeLit)
		if !ok || !isStringInterfaceMap(cl.Type) {
			return
		}
		v := &guardMapVar{}
		for _, elt := range cl.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				v.keys = append(v.keys, kv.Key)
			}
		}
		mapVars[name] = v
	}
	ast.Inspect(astFile, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				if i >= len(node.Rhs) {
					continue
				}
				if id, ok := lhs.(*ast.Ident); ok {
					recordMapVar(id.Name, node.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, name := range node.Names {
				if i >= len(node.Values) {
					continue
				}
				recordMapVar(name.Name, node.Values[i])
			}
		}
		return true
	})

	// 第二遍：列名出现点（Update/Updates/Pluck 调用 + 目标 map 变量的后续下标赋值）
	ast.Inspect(astFile, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "Update", "Pluck":
					if len(node.Args) > 0 {
						addGuardUse(uses, fset, node.Args[0])
					}
				case "Updates":
					if len(node.Args) > 0 {
						switch arg := node.Args[0].(type) {
						case *ast.Ident: // Updates(m)：只在该变量确为 map[string]interface{} 时取键
							if v, ok := mapVars[arg.Name]; ok {
								v.used = true // 键延到第三遍记，此处只标记
							}
						case *ast.CompositeLit:
							for _, elt := range arg.Elts {
								if kv, ok := elt.(*ast.KeyValueExpr); ok {
									addGuardUse(uses, fset, kv.Key)
								}
							}
						case *ast.BasicLit:
							addGuardUse(uses, fset, arg)
						}
					}
				}
			}
		case *ast.AssignStmt: // updates["disk_gb"] = gb：只认已确认进 Updates 的变量
			for _, lhs := range node.Lhs {
				idx, ok := lhs.(*ast.IndexExpr)
				if !ok {
					continue
				}
				id, ok := idx.X.(*ast.Ident)
				if !ok {
					continue
				}
				if v, ok := mapVars[id.Name]; ok && v.used {
					addGuardUse(uses, fset, idx.Index)
				}
			}
		}
		return true
	})

	// 第三遍：补记「确认进 Updates 的 map 变量」的初始键
	for _, v := range mapVars {
		if !v.used {
			continue
		}
		for _, k := range v.keys {
			addGuardUse(uses, fset, k)
		}
	}
}

// guardMapVar 一个 map[string]interface{} 变量的登记信息。
type guardMapVar struct {
	keys []ast.Expr
	used bool // 是否被送进 Updates（决定其键要不要当列名校验）
}

// isStringInterfaceMap 判断复合字面量的类型是否为 map[string]interface{}（gin.H 亦然，
// 但 gin.H 不会进 Updates，故不会误报）。
func isStringInterfaceMap(expr ast.Expr) bool {
	mt, ok := expr.(*ast.MapType)
	if !ok {
		return false
	}
	key, ok := mt.Key.(*ast.Ident)
	if !ok || key.Name != "string" {
		return false
	}
	_, ok = mt.Value.(*ast.InterfaceType)
	return ok
}

func addGuardUse(uses map[string]string, fset *token.FileSet, expr ast.Expr) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil {
		name = strings.Trim(lit.Value, `"`) // 反引号字符串等退化处理
	}
	// 只校验「看起来像列名」的标识符：含空格/逗号/括号/点/? 的是 SQL 表达式，跳过
	if name == "" || strings.ContainsAny(name, " ,()?.`*=<>|") {
		return
	}
	if _, exists := uses[name]; !exists {
		uses[name] = fset.Position(lit.Pos()).String()
	}
}

// TestHandWrittenColumnNamesExist 手写列名必须真实存在，否则整条 UPDATE 会静默失败。
func TestHandWrittenColumnNamesExist(t *testing.T) {
	valid := guardNameSet(t)
	uses := guardColumnUses(t, filepath.Join("..", "handler"), filepath.Join("..", "service"))
	for name, pos := range uses {
		if !valid[name] {
			t.Errorf("列名 %q 在任何模型中都不存在（字段名/列名写错会让整条 UPDATE 报 1054 后静默失败），首次出现于 %s",
				name, pos)
		}
	}
}

// TestVMVCPUColumnName 针对已发作过两次的真实 bug 做定点回归：
// VCPU 字段的列名是 v_cpu，写成 vcpu 会每次回写失败。
func TestVMVCPUColumnName(t *testing.T) {
	s, err := schema.Parse(&VM{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("解析 VM schema 失败: %v", err)
	}
	if _, ok := s.FieldsByDBName["v_cpu"]; !ok {
		t.Fatal("VM 的 v_cpu 列不存在：若字段已改名请同步更新本护栏与相关 Update 调用")
	}
	if _, ok := s.FieldsByDBName["vcpu"]; ok {
		t.Fatal("VM 出现了 vcpu 列：命名策略变更，请复核所有手写的 CPU 列名")
	}
}
