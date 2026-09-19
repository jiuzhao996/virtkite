package handler

// container_terminal.go 容器终端（v3.2 批次 R2，对标 1Panel 容器终端）：
// 浏览器 xterm.js --WebSocket--> 本桥 --Docker Engine API exec(TTY)--> 容器内 shell。
// 桥接模式与 handler/terminal.go（SSH 终端桥）严格对齐：
//   - WS 升级后一律经 console.Conn 包装（输出转发、pong 帧、管理员强断多路并发写
//     裸 *websocket.Conn 会触发 gorilla panic 带走整个进程）；
//   - 自起协程逐个 defer recover（gin Recovery 只覆盖 HTTP 请求链，项目规范 10）；
//   - 完整错误只进服务端日志，WS 错误帧一律固定中文文案，不泄漏内部细节。
//
// 为什么走 Engine API（docker.sock）而不是 `docker exec -it` CLI：
// 实测 docker CLI 对非 TTY 的 stdin 直接拒绝（"the input device is not a TTY"——
// 该检查在 CLI 内部，-t 与本进程侧的管道天然互斥），CLI 方案根本无法工作；
// Engine API 没有这道检查，且 /exec/{id}/resize 原生支持运行中调窗（CLI 没有该通道）。
// 1Panel/Portainer 的容器终端同走此路。1Panel 参照实现即「exec(Tty)+attach 劫持流」。
//
// 生命周期：exec 进程运行在容器内、由 dockerd 托管，本地无子进程（无进程组可杀、无孤儿可泄漏）；
// WS 断开时先发 EOF 促使空闲 shell 自退，再关闭劫持流（详见 Connect 内 finalize 注释）——
// 与原生 docker exec 断开即分离的语义一致，容器内残留仅限「前台正跑程序时断开」的少数场景。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/jiuzhao/vmops/service/console"
)

const (
	// dockerSockPath dockerd 官方默认 unix socket 路径
	dockerSockPath = "/var/run/docker.sock"
	// dockerAPIVerFallback API 版本协商失败时的回退值（docker 26~29 均支持 1.44；
	// docker 29.x 起强制最低 1.44，写死更旧版本会被 400 拒绝——实测踩中）
	dockerAPIVerFallback = "1.44"
)

var (
	dockerAPIOnce sync.Once
	dockerAPIVer  string
)

// dockerAPIVersion 与 dockerd 协商 API 版本（GET /_ping 响应头 Api-Version，进程内缓存一次）。
// Engine API 的路径必须带版本前缀，且高版本 daemon 拒绝过旧版本，故不能写死。
func dockerAPIVersion() string {
	dockerAPIOnce.Do(func() {
		dockerAPIVer = dockerAPIVerFallback
		conn, err := net.DialTimeout("unix", dockerSockPath, 3*time.Second)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		req, rerr := http.NewRequest(http.MethodGet, "http://docker/_ping", nil)
		if rerr != nil {
			return
		}
		if werr := req.Write(conn); werr != nil {
			return
		}
		resp, rerr := http.ReadResponse(bufio.NewReader(conn), req)
		if rerr != nil {
			return
		}
		defer resp.Body.Close()
		if v := resp.Header.Get("Api-Version"); v != "" {
			dockerAPIVer = v
		}
	})
	return dockerAPIVer
}

// dockerExecShells 容器内 shell 白名单。shell 直接进入 exec 的 Cmd 且取自浏览器首帧，
// 不设白名单等于把「进入容器终端」扩大成「以任意程序在容器内执行任意命令」。
var dockerExecShells = map[string]bool{
	"/bin/sh":   true,
	"/bin/bash": true,
	"sh":        true,
	"bash":      true,
}

// ContainerTerminalHandler 容器终端（WebSocket ↔ docker exec 桥）处理器。
// 复用全局 console.Registry：docker-exec 会话在「控制台会话」页可见、可被管理员强制断开。
type ContainerTerminalHandler struct {
	Registry *console.Registry
}

// NewContainerTerminalHandler 创建容器终端处理器。
func NewContainerTerminalHandler(registry *console.Registry) *ContainerTerminalHandler {
	return &ContainerTerminalHandler{Registry: registry}
}

// wsControlFrame 容器终端 WS 控制帧。
type wsControlFrame struct {
	Type  string `json:"type"`  // input / resize / ping / shell
	Data  string `json:"data"`  // input：终端输入内容
	Value string `json:"value"` // shell：shell 路径
	Cols  int    `json:"cols"`  // resize：列数
	Rows  int    `json:"rows"`  // resize：行数
}

// Connect 处理容器终端 WebSocket 连接（GET /api/docker/containers/:id/terminal）。
// 挂载在 docker 路由组（middleware.NonViewerMiddleware），viewer 在组上即被 403。
//
// 消息协议：
//
//	客户端 → 服务端：
//	  首帧约定为控制帧 {"type":"resize","cols":N,"rows":N} 声明尺寸，
//	  或 {"type":"shell","value":"/bin/bash"} 选择 shell（缺省 /bin/sh）；
//	  其余消息一律作为输入字节写入容器 stdin（含首帧非控制帧时的初始输入）。
//	服务端 → 客户端：
//	  {"type":"connected","container":...,"shell":...} exec 就绪（收到首帧后才启动并回本帧）；
//	  二进制帧 = 容器内 TTY 输出（Tty 模式下 stdout/stderr 已在 daemon 侧合流）；
//	  {"type":"resized","cols":N,"rows":N} resize 应答（经 /exec/{id}/resize 实时生效）；
//	  {"type":"pong"} 心跳应答；
//	  {"type":"error","msg":"固定中文文案"} 致命错误，随后连接关闭。
func (h *ContainerTerminalHandler) Connect(c *gin.Context) {
	rawConn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	// 输出转发、读循环 pong、管理员强制断开多路并发写，必须经 console.Conn 串行化
	conn := console.NewConn(rawConn)
	defer conn.Close()
	id := c.Param("id")
	log.Printf("[docker-terminal] WS 已连接 from=%s container=%s", c.ClientIP(), id)

	// 容器 ID 白名单校验（同 handler/docker.go）：ID 进入 Engine API 的请求路径，
	// 不校验等于可对宿主机上任意容器/镜像路径发起操作（WS 已升级，错误只能走 WS 帧）
	if !safeDockerID(id) {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "容器 ID 非法"})
		return
	}

	// 校验容器存在且运行中（等价 docker inspect --format '{{json .State}}' <容器>）
	running, paused, err := dockerContainerState(id)
	if err != nil {
		log.Printf("[docker-terminal] 容器状态查询失败 container=%s from=%s err=%v", id, c.ClientIP(), err)
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "容器不存在或 docker 不可用"})
		return
	}
	if paused {
		// docker pause 后 State.Running 仍为 true，须单独拦截
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "容器已暂停，无法打开终端"})
		return
	}
	if !running {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "容器未运行"})
		return
	}

	// 读取首帧：约定为控制帧（声明尺寸 / 选择 shell）；非控制帧内容留作启动后的初始输入。
	// 与 terminal.go 的首帧 auth 等同对待：客户端不发首帧则连接挂起，不做超时兜底。
	shell := "/bin/sh"
	cols, rows := 0, 0
	var pendingInput []byte
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	if f, ok := parseControlFrame(raw); ok {
		switch f.Type {
		case "shell":
			if !dockerExecShells[f.Value] {
				log.Printf("[docker-terminal] shell 未过白名单 container=%s value=%q from=%s", id, f.Value, c.ClientIP())
				_ = conn.WriteJSON(gin.H{"type": "error", "msg": "不支持的 shell（仅允许 sh / bash）"})
				return
			}
			shell = f.Value
			cols, rows = clampTermSize(f.Cols, f.Rows)
		case "resize":
			cols, rows = clampTermSize(f.Cols, f.Rows)
		case "ping":
			_ = conn.WriteJSON(gin.H{"type": "pong"})
		}
	} else {
		pendingInput = raw
	}

	// 容器内 shell 存在性探测（等价 docker exec <容器> <shell> -c "exit 0"）。
	// exec 实例的创建/启动本身不校验 Cmd 是否存在（错误要等启动后的 OCI runtime 阶段才暴露），
	// 先探测一次即可确定可用的 shell：sh 不可用回退 bash（对应原「启动失败回退试 bash」语义）。
	if !probeContainerShell(id, shell) {
		if shell != "/bin/bash" && probeContainerShell(id, "/bin/bash") {
			log.Printf("[docker-terminal] shell %s 不可用，回退 /bin/bash container=%s", shell, id)
			shell = "/bin/bash"
		} else {
			log.Printf("[docker-terminal] 容器内无可用 shell container=%s shell=%s", id, shell)
			_ = conn.WriteJSON(gin.H{"type": "error", "msg": "容器内没有可用的 shell（需包含 sh 或 bash）"})
			return
		}
	}

	// 创建 exec 实例并建立 TTY 交互流（POST /containers/:id/exec + POST /exec/:id/start 劫持）
	env := []string{"TERM=xterm-256color"}
	if cols > 0 && rows > 0 {
		// 尽力声明初始尺寸：TERM 尺寸由下方 resize 精确设置，env 兜底给不回读窗口的 shell（如 dash）
		env = append(env, "COLUMNS="+strconv.Itoa(cols), "LINES="+strconv.Itoa(rows))
	}
	execID, err := dockerExecCreate(id, shell, env)
	if err != nil {
		log.Printf("[docker-terminal] 创建 exec 失败 container=%s err=%v", id, err)
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "容器终端会话创建失败"})
		return
	}
	stream, err := dockerExecAttachStart(execID)
	if err != nil {
		log.Printf("[docker-terminal] 启动 exec 失败 container=%s exec=%s err=%v", id, execID, err)
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "容器终端启动失败（确认容器内 shell 可用）"})
		return
	}
	if cols > 0 && rows > 0 {
		// 初始尺寸经 resize 精确设置（成功与否不影响会话，daemon 会给 TTY 一个默认尺寸）
		if rerr := dockerExecResize(execID, cols, rows); rerr != nil {
			log.Printf("[docker-terminal] 初始 resize 失败 container=%s exec=%s err=%v", id, execID, rerr)
		}
	}
	log.Printf("[docker-terminal] exec 已启动 container=%s shell=%s exec=%s from=%s", id, shell, execID, c.ClientIP())

	_ = conn.WriteJSON(gin.H{"type": "connected", "container": id, "shell": shell})

	// 登记控制台会话（type=docker-exec）：Registry.Open 的 vmID 只是普通索引列（无外键），
	// 容器会话没有 VM 归属故落 0、容器 ID 存 VMName 列，无需改 console 包。
	// 登记失败不影响终端可用性（Open 内部已留痕返回 nil）；登记成功则管理员可强断。
	if h.Registry != nil {
		regName := id
		if len(regName) > 100 { // ConsoleSession.VMName 列宽 size:100
			regName = regName[:100]
		}
		uid, uname := taskUserFromContext(c)
		if s := h.Registry.Open(0, regName, "docker-exec", uname, uid, c.ClientIP(), conn); s != nil {
			defer h.Registry.Close(s.ID)
		}
	}

	var wg sync.WaitGroup

	// 容器内 TTY 流 → WebSocket（二进制帧，xterm 直接写入；daemon 侧已合流 stdout/stderr）。
	// 自起协程的 panic 无法被 gin Recovery 拦截，会直接终止进程，必须自兜底。
	// 流结束 = shell 退出（用户敲 exit / 容器侧终止 / dockerd 回收），反关 WS 唤醒读循环走统一收尾。
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[docker-terminal] 输出转发 panic container=%s panic=%v\n%s", id, rec, debug.Stack())
			}
		}()
		buf := make([]byte, 4096)
		for {
			n, rerr := stream.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				_ = conn.Close()
				return
			}
		}
	}()

	// 终端收尾（幂等）：先发 EOF（C-d）促使空闲 shell 自行退出，再关闭到 dockerd 的劫持流。
	// TTY 模式下仅关流等价 docker 原生「分离」——daemon 不保证向 shell 送达 SIGHUP，
	// 且本宿主机安全策略下容器内 root kill 亦 EPERM（实测连自有子进程都拒），无法从外部强杀；
	// C-d 在空行处即 EOF（shell 自退），在前后台程序中只是无害按键，此类会话退化为分离语义，
	// 残留进程随容器停止消亡。
	finalize := func() {
		_, _ = stream.Write([]byte{0x04})
		time.Sleep(150 * time.Millisecond)
		_ = stream.Close()
		wg.Wait()
	}

	if len(pendingInput) > 0 {
		if _, werr := stream.Write(pendingInput); werr != nil {
			// 首帧即输入且写入失败，统一走收尾（不进读循环）
			log.Printf("[docker-terminal] 初始输入写入失败 container=%s err=%v", id, werr)
			finalize()
			return
		}
	}

	// WebSocket → 容器 stdin。JSON 控制帧按语义处理，其余消息（普通按键字节流）原样写 TTY。
readLoop:
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		f, isControl := parseControlFrame(data)
		switch {
		case !isControl, f.Type == "input":
			payload := data
			if isControl {
				payload = []byte(f.Data)
			}
			if _, werr := stream.Write(payload); werr != nil {
				// 带标签跳出外层 for：裸 break 只能跳出 switch（同 terminal.go 的教训）
				break readLoop
			}
		case f.Type == "resize":
			// resize 经 /exec/{id}/resize 实时生效——这是走 Engine API 相对 docker CLI 的能力增益
			if rerr := dockerExecResize(execID, f.Cols, f.Rows); rerr != nil {
				log.Printf("[docker-terminal] resize 失败 container=%s exec=%s err=%v", id, execID, rerr)
			}
			_ = conn.WriteJSON(gin.H{"type": "resized", "cols": f.Cols, "rows": f.Rows})
		case f.Type == "ping":
			_ = conn.WriteJSON(gin.H{"type": "pong"})
		case f.Type == "shell":
			// shell 选择仅在首帧（exec 启动前）生效，运行中收到直接忽略
		}
	}

	finalize()
}

// dockerExecStream 与 dockerd 劫持后的 TTY 双向流。
// Tty 模式下为裸字节流（无 stdcopy 多路复用帧），读走 br（先排空 HTTP 解析残留缓冲），
// 写直用底层连接；net.Conn 允许读写并发，输出转发协程与输入写循环互不干扰。
type dockerExecStream struct {
	id   string        // exec 实例 ID（resize 用）
	conn net.Conn      // 底层 unix 连接
	br   *bufio.Reader // 响应头解析后的残留读缓冲
}

// Read 从容器 TTY 读输出。
func (s *dockerExecStream) Read(p []byte) (int, error) { return s.br.Read(p) }

// Write 向容器 TTY 写输入。
func (s *dockerExecStream) Write(p []byte) (int, error) { return s.conn.Write(p) }

// Close 关闭流（dockerd 检测到断开后回收 exec 进程）。
func (s *dockerExecStream) Close() error { return s.conn.Close() }

// dockerExecCreate 创建 exec 实例（POST /v1.24/containers/{id}/exec，201 返回 Id）。
// 完整错误（含 daemon 响应体）由调用方进服务端日志，这里原样带回。
func dockerExecCreate(id, shell string, env []string) (string, error) {
	var payload struct {
		AttachStdin  bool     `json:"AttachStdin"`
		AttachStdout bool     `json:"AttachStdout"`
		AttachStderr bool     `json:"AttachStderr"`
		Tty          bool     `json:"Tty"`
		Env          []string `json:"Env,omitempty"`
		Cmd          []string `json:"Cmd"`
	}
	payload.AttachStdin = true
	payload.AttachStdout = true
	payload.AttachStderr = true
	payload.Tty = true
	payload.Env = env
	payload.Cmd = []string{shell}
	body, err := json.Marshal(&payload)
	if err != nil {
		return "", fmt.Errorf("序列化 exec 参数: %w", err)
	}

	conn, err := net.Dial("unix", dockerSockPath)
	if err != nil {
		return "", fmt.Errorf("连接 docker.sock: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req, err := http.NewRequest(http.MethodPost, "http://docker/v"+dockerAPIVersion()+"/containers/"+id+"/exec", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("构造 exec 请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if werr := req.Write(conn); werr != nil {
		return "", fmt.Errorf("发送 exec 请求: %w", werr)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return "", fmt.Errorf("读取 exec 响应: %w", err)
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("创建 exec 失败: HTTP %d %s", resp.StatusCode, string(respBody))
	}
	var created struct {
		ID string `json:"Id"`
	}
	if uerr := json.Unmarshal(respBody, &created); uerr != nil || created.ID == "" {
		return "", fmt.Errorf("解析 exec 响应: %w", uerr)
	}
	return created.ID, nil
}

// dockerExecAttachStart 启动 exec 并劫持连接为 TTY 双向流（POST /v1.24/exec/{id}/start）。
// 容器内 shell 不存在等 OCI runtime 错误在此阶段以非 2xx 暴露。
func dockerExecAttachStart(execID string) (*dockerExecStream, error) {
	conn, err := net.Dial("unix", dockerSockPath)
	if err != nil {
		return nil, fmt.Errorf("连接 docker.sock: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req, err := http.NewRequest(http.MethodPost, "http://docker/v"+dockerAPIVersion()+"/exec/"+execID+"/start",
		bytes.NewReader([]byte(`{"Detach":false,"Tty":true}`)))
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("构造 start 请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// 劫持为双向裸流：daemon 以 101（或兼容 200）应答，此后该连接不再是 HTTP 语义
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	if werr := req.Write(conn); werr != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("发送 start 请求: %w", werr)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("读取 start 响应: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = conn.Close()
		return nil, fmt.Errorf("启动 exec 失败: HTTP %d %s", resp.StatusCode, string(respBody))
	}
	_ = conn.SetDeadline(time.Time{}) // 劫持流为长连接，撤销请求期超时
	return &dockerExecStream{id: execID, conn: conn, br: br}, nil
}

// dockerExecResize 调整运行中 exec 的 TTY 尺寸（POST /v1.24/exec/{id}/resize）。
// 尺寸越界视为未声明直接跳过；daemon 侧错误（如 exec 已退出）由调用方记日志后忽略。
func dockerExecResize(execID string, cols, rows int) error {
	cols, rows = clampTermSize(cols, rows)
	if cols == 0 || rows == 0 {
		return nil
	}
	conn, err := net.Dial("unix", dockerSockPath)
	if err != nil {
		return fmt.Errorf("连接 docker.sock: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	path := "http://docker/v" + dockerAPIVersion() + "/exec/" + execID + "/resize?w=" + strconv.Itoa(cols) + "&h=" + strconv.Itoa(rows)
	req, err := http.NewRequest(http.MethodPost, path, nil)
	if err != nil {
		return fmt.Errorf("构造 resize 请求: %w", err)
	}
	if werr := req.Write(conn); werr != nil {
		return fmt.Errorf("发送 resize 请求: %w", werr)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return fmt.Errorf("读取 resize 响应: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("resize 失败: HTTP %d", resp.StatusCode)
	}
	return nil
}

// dockerContainerState 查询容器状态（等价 docker inspect --format '{{json .State}}' <容器>）。
// 完整错误由调用方进服务端日志，这里只回传运行/暂停两个布尔位。
func dockerContainerState(id string) (running, paused bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .State}}", id).Output()
	if err != nil {
		return false, false, err
	}
	var st struct {
		Running bool `json:"Running"`
		Paused  bool `json:"Paused"`
	}
	if uerr := json.Unmarshal(out, &st); uerr != nil {
		return false, false, fmt.Errorf("解析 docker inspect 输出: %w", uerr)
	}
	return st.Running, st.Paused, nil
}

// probeContainerShell 探测容器内 shell 是否可执行（等价 docker exec <容器> <shell> -c "exit 0"）。
// 二进制缺失时 docker CLI 非零退出（OCI runtime exec failed），据此判断；带超时防 daemon 卡死拖住请求。
func probeContainerShell(id, shell string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "exec", id, shell, "-c", "exit 0").Run() == nil
}

// parseControlFrame 识别 JSON 控制帧：仅当消息是 JSON 对象且 type 命中已知控制类型时成立。
// 其余字节流（含恰好能被反序列化的裸数字等）一律视为终端输入，防止用户键入被控制层吞掉。
func parseControlFrame(data []byte) (wsControlFrame, bool) {
	if len(data) == 0 || data[0] != '{' {
		return wsControlFrame{}, false
	}
	var f wsControlFrame
	if err := json.Unmarshal(data, &f); err != nil || f.Type == "" {
		return wsControlFrame{}, false
	}
	switch f.Type {
	case "input", "resize", "ping", "shell":
		return f, true
	}
	return wsControlFrame{}, false
}

// clampTermSize 约束终端尺寸入参，超出合理范围视为未声明（返回 0，不透传给 daemon）。
func clampTermSize(cols, rows int) (int, int) {
	if cols < 2 || cols > 1000 || rows < 2 || rows > 1000 {
		return 0, 0
	}
	return cols, rows
}
