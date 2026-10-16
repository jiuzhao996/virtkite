package handler

// container_terminal_exec.go 容器终端的 Docker Engine API 交互原语：exec 实例创建/启动（劫持为
// TTY 双向流）/调窗，以及容器运行态与容器内 shell 的探测。全部经 /var/run/docker.sock 直连，
// 不fork docker CLI（CLI 对非 TTY stdin 直接拒绝）。WS 桥接与协议见 container_terminal.go。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"time"
)

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
