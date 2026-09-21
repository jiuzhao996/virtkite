package dockerx

import (
	"reflect"
	"strings"
	"testing"
)

// TestBuildRunArgs docker run 参数构造（纯函数）。用例覆盖：全字段、最简形态、
// restart 默认省略、command 拆分、空白项跳过。
func TestBuildRunArgs(t *testing.T) {
	t.Run("全字段", func(t *testing.T) {
		opts := ContainerOpts{
			Name:    "my-nginx",
			Image:   "nginx:1.27",
			Ports:   []string{"8080:80", "127.0.0.1:8443:443"},
			Volumes: []string{"/data/app:/usr/share/nginx/html:ro"},
			Envs:    []string{"TZ=Asia/Shanghai"},
			Restart: "always",
			Command: "nginx -g daemon",
		}
		want := []string{
			"run", "-d", "--name", "my-nginx",
			"--restart", "always",
			"-p", "8080:80",
			"-p", "127.0.0.1:8443:443",
			"-v", "/data/app:/usr/share/nginx/html:ro",
			"-e", "TZ=Asia/Shanghai",
			"nginx:1.27",
			"nginx", "-g", "daemon",
		}
		if got := BuildRunArgs(opts); !reflect.DeepEqual(got, want) {
			t.Errorf("全字段参数不符:\ngot  %v\nwant %v", got, want)
		}
	})

	t.Run("最简形态只有name与image", func(t *testing.T) {
		opts := ContainerOpts{Name: "box", Image: "busybox"}
		want := []string{"run", "-d", "--name", "box", "busybox"}
		got := BuildRunArgs(opts)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("最简形态不符: got %v want %v", got, want)
		}
		// 关键契约：最简形态不得输出 --restart / -p / -v / -e
		for _, banned := range []string{"--restart", "-p", "-v", "-e"} {
			if strings.Contains(strings.Join(got, " "), banned) {
				t.Errorf("最简形态不应输出 %s: %v", banned, got)
			}
		}
	})

	t.Run("restart为no或空均省略", func(t *testing.T) {
		for _, r := range []string{"", "no"} {
			got := BuildRunArgs(ContainerOpts{Name: "b", Image: "busybox", Restart: r})
			if strings.Contains(strings.Join(got, " "), "--restart") {
				t.Errorf("restart=%q 不应输出 --restart: %v", r, got)
			}
		}
	})

	t.Run("command按空白拆分连续空白视作一个", func(t *testing.T) {
		got := BuildRunArgs(ContainerOpts{Name: "b", Image: "busybox", Command: "  sh  -c   echo hi  "})
		want := []string{"run", "-d", "--name", "b", "busybox", "sh", "-c", "echo", "hi"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("command 拆分不符: got %v want %v", got, want)
		}
	})

	t.Run("空白列表项跳过", func(t *testing.T) {
		opts := ContainerOpts{
			Name:    "b",
			Image:   "busybox",
			Ports:   []string{"", "  "},
			Volumes: []string{""},
			Envs:    []string{""},
		}
		want := []string{"run", "-d", "--name", "b", "busybox"}
		if got := BuildRunArgs(opts); !reflect.DeepEqual(got, want) {
			t.Errorf("空白项应全部跳过: got %v want %v", got, want)
		}
	})
}

// TestValidateContainerOpts 校验规则表驱动：合法形态 + 各类非法输入。
func TestValidateContainerOpts(t *testing.T) {
	valid := ContainerOpts{
		Name:    "my-nginx",
		Image:   "nginx:1.27",
		Ports:   []string{"8080:80", "127.0.0.1:8443:443", "53:53/udp"},
		Volumes: []string{"/data/app:/usr/share/nginx/html:ro", "data-vol:/var/lib/data"},
		Envs:    []string{"TZ=Asia/Shanghai", "A=B=C"},
		Restart: "unless-stopped",
		Command: "nginx -g daemon",
	}

	cases := []struct {
		name    string
		mutate  func(*ContainerOpts)
		wantErr bool
	}{
		{"合法全字段", func(o *ContainerOpts) {}, false},
		{"合法最简形态", func(o *ContainerOpts) {
			*o = ContainerOpts{Name: "box", Image: "busybox"}
		}, false},
		{"restart为no合法", func(o *ContainerOpts) { o.Restart = "no" }, false},
		{"容器名为空", func(o *ContainerOpts) { o.Name = "" }, true},
		{"容器名以连字符开头", func(o *ContainerOpts) { o.Name = "-evil" }, true},
		{"容器名含斜杠", func(o *ContainerOpts) { o.Name = "a/b" }, true},
		{"容器名超64位", func(o *ContainerOpts) { o.Name = "a" + strings.Repeat("b", 64) }, true},
		{"镜像名为空", func(o *ContainerOpts) { o.Image = "" }, true},
		{"镜像名含空白", func(o *ContainerOpts) { o.Image = "nginx 1.27" }, true},
		{"restart白名单外", func(o *ContainerOpts) { o.Restart = "always2" }, true},
		{"端口为单段", func(o *ContainerOpts) { o.Ports = []string{"80"} }, true},
		{"端口越上界", func(o *ContainerOpts) { o.Ports = []string{"8080:65536"} }, true},
		{"端口越下界", func(o *ContainerOpts) { o.Ports = []string{"0:80"} }, true},
		{"端口非数字", func(o *ContainerOpts) { o.Ports = []string{"http:80"} }, true},
		{"端口三段式IP非法", func(o *ContainerOpts) { o.Ports = []string{"999.1.1.1:80:80"} }, true},
		{"端口协议白名单外", func(o *ContainerOpts) { o.Ports = []string{"8080:80/sctp"} }, true},
		{"端口四段", func(o *ContainerOpts) { o.Ports = []string{"1:2:3:4"} }, true},
		{"卷缺容器路径", func(o *ContainerOpts) { o.Volumes = []string{"/data"} }, true},
		{"卷容器路径非绝对", func(o *ContainerOpts) { o.Volumes = []string{"/data:rel/path"} }, true},
		{"卷读写模式非法", func(o *ContainerOpts) { o.Volumes = []string{"/data:/data:rx"} }, true},
		{"env无等号", func(o *ContainerOpts) { o.Envs = []string{"NOEQ"} }, true},
		{"env键为空", func(o *ContainerOpts) { o.Envs = []string{"=VALUE"} }, true},
		{"env键含空白", func(o *ContainerOpts) { o.Envs = []string{"BAD KEY=1"} }, true},
		{"env值含等号合法（按第一个=切）", func(o *ContainerOpts) { o.Envs = []string{"A=B=C"} }, false},
		{"端口udp合法", func(o *ContainerOpts) { o.Ports = []string{"53:53/udp"} }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := valid
			tc.mutate(&opts)
			err := ValidateContainerOpts(opts)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateContainerOpts err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// TestValidateContainerOptsBlankEntries 空白列表项应跳过而非报错（与 BuildRunArgs 行为对齐）。
func TestValidateContainerOptsBlankEntries(t *testing.T) {
	opts := ContainerOpts{
		Name:    "box",
		Image:   "busybox",
		Ports:   []string{"", "  "},
		Volumes: []string{""},
		Envs:    []string{""},
	}
	if err := ValidateContainerOpts(opts); err != nil {
		t.Errorf("空白列表项应跳过，实际报错: %v", err)
	}
}

// TestExtractContainerID 验证从 docker run -d 混合输出提取容器短 ID：
// 镜像本地缺失自动拉取时 stderr 噪音（"Unable to find image..."）会经
// CombinedOutput 混在 64 位 ID 之前，必须取最后一个 hex 形态的行。
func TestExtractContainerID(t *testing.T) {
	full := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2"
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"纯 ID 输出", full + "\n", full[:12]},
		{"拉取噪音在前", "Unable to find image 'busybox:latest' locally\nPulling library/busybox...\nPull complete\n" + full + "\n", full[:12]},
		{"无换行尾", "Unable to find...\n" + full, full[:12]},
		{"全噪音无 ID", "Unable to find image 'x'\nPulling...\n", ""},
		{"空输出", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractContainerID(c.out); got != c.want {
				t.Errorf("extractContainerID(%q) = %q, want %q", c.out, got, c.want)
			}
		})
	}
}
